package controller

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/cloudflaremulti"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMultiKeyEnableRestoresOnlyExhaustedChannels(t *testing.T) {
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	previousMaster, previousCache, previousRedis, previousSQLite := common.IsMasterNode, common.MemoryCacheEnabled, common.RedisEnabled, common.SQLitePath
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousType, previousLogType)
		common.IsMasterNode, common.MemoryCacheEnabled, common.RedisEnabled, common.SQLitePath = previousMaster, previousCache, previousRedis, previousSQLite
	})
	t.Setenv("SQL_DSN", os.Getenv("TEST_CHANNEL_SQL_DSN"))
	t.Setenv("LOG_SQL_DSN", "")
	common.IsMasterNode, common.MemoryCacheEnabled, common.RedisEnabled = false, false, false
	common.SQLitePath = filepath.Join(t.TempDir(), "channel.db")
	require.NoError(t, model.InitDB())
	database := model.DB
	sqlDB, err := database.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	model.LOG_DB = database
	common.SetLogDatabaseType(common.MainDatabaseType())
	require.NoError(t, database.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.User{}, &model.Log{}, &model.AuditLog{}))
	root := &model.User{Username: "multi-key-review-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	require.NoError(t, database.Create(root).Error)
	t.Cleanup(func() { require.NoError(t, database.Unscoped().Delete(root).Error) })
	versionQuery := "SELECT VERSION()"
	if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
		versionQuery = "SELECT sqlite_version()"
	}
	var version string
	require.NoError(t, database.Raw(versionQuery).Scan(&version).Error)
	t.Logf("database=%s version=%s", common.MainDatabaseType(), version)

	for _, cacheEnabled := range []bool{false, true} {
		for _, action := range []string{"enable_key", "enable_all_keys"} {
			for _, tc := range []struct {
				name           string
				initialStatus  int
				manualOverride string
				wantStatus     int
			}{
				{name: "key exhaustion restores", initialStatus: common.ChannelStatusEnabled, wantStatus: common.ChannelStatusEnabled},
				{name: "manual disable is preserved", initialStatus: common.ChannelStatusManuallyDisabled, wantStatus: common.ChannelStatusManuallyDisabled},
				{name: "manual disable after exhaustion is preserved", initialStatus: common.ChannelStatusEnabled, manualOverride: "status", wantStatus: common.ChannelStatusManuallyDisabled},
				{name: "tag disable after exhaustion is preserved", initialStatus: common.ChannelStatusEnabled, manualOverride: "tag", wantStatus: common.ChannelStatusManuallyDisabled},
			} {
				t.Run(fmt.Sprintf("cache=%t/%s/%s", cacheEnabled, action, tc.name), func(t *testing.T) {
					common.MemoryCacheEnabled = cacheEnabled
					tag := t.Name()
					channel := &model.Channel{Name: t.Name(), Type: 1, Key: "key-one\nkey-two", Status: tc.initialStatus, Models: "test-model", Group: "default", Tag: &tag,
						ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyStatusList: map[int]int{1: common.ChannelStatusManuallyDisabled}},
					}
					require.NoError(t, channel.Insert())
					t.Cleanup(func() {
						require.NoError(t, channel.Delete())
						model.InitChannelCache()
					})
					for _, operation := range []string{"disable_key", action} {
						if operation == action {
							if tc.manualOverride == "status" {
								model.UpdateChannelStatus(channel.Id, "", common.ChannelStatusManuallyDisabled, "manual operation")
							} else if tc.manualOverride == "tag" {
								require.NoError(t, model.DisableChannelByTag(tag))
							}
						}
						payload, err := common.Marshal(MultiKeyManageRequest{ChannelId: channel.Id, Action: operation, KeyIndex: common.GetPointer(0)})
						require.NoError(t, err)
						recorder := httptest.NewRecorder()
						c, _ := gin.CreateTestContext(recorder)
						c.Set("id", root.Id)
						c.Set("role", common.RoleRootUser)
						c.Request = httptest.NewRequest(http.MethodPost, "/api/channel/multi_key", bytes.NewReader(payload))
						c.Request.Header.Set("Content-Type", "application/json")
						ManageMultiKeys(c)
						var result struct {
							Success bool `json:"success"`
						}
						require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &result))
						require.True(t, result.Success, recorder.Body.String())
					}
					loaded, err := model.GetChannelById(channel.Id, true)
					require.NoError(t, err)
					assert.Equal(t, tc.wantStatus, loaded.Status)
					assert.NotContains(t, loaded.ChannelInfo.MultiKeyStatusList, 0)
					assert.NotContains(t, loaded.ChannelInfo.MultiKeyDisabledReason, 0)
					assert.NotContains(t, loaded.ChannelInfo.MultiKeyDisabledTime, 0)
					var ability model.Ability
					require.NoError(t, database.Where("channel_id = ?", channel.Id).First(&ability).Error)
					assert.Equal(t, tc.wantStatus == common.ChannelStatusEnabled, ability.Enabled)
				})
			}
		}
	}
}

func TestCloudflareMultiAccountChannelLifecycle(t *testing.T) {
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	previousMaster, previousCache, previousRedis, previousSQLite := common.IsMasterNode, common.MemoryCacheEnabled, common.RedisEnabled, common.SQLitePath
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousType, previousLogType)
		common.IsMasterNode, common.MemoryCacheEnabled, common.RedisEnabled, common.SQLitePath = previousMaster, previousCache, previousRedis, previousSQLite
	})
	t.Setenv("SQL_DSN", os.Getenv("TEST_CHANNEL_SQL_DSN"))
	t.Setenv("LOG_SQL_DSN", "")
	common.IsMasterNode, common.MemoryCacheEnabled, common.RedisEnabled = false, false, false
	common.SQLitePath = filepath.Join(t.TempDir(), "cloudflare.db")
	require.NoError(t, model.InitDB())
	database := model.DB
	sqlDB, err := database.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	model.LOG_DB = database
	common.SetLogDatabaseType(common.MainDatabaseType())
	require.NoError(t, database.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.User{}, &model.Log{}, &model.AuditLog{}))
	root := &model.User{Username: "cf-multi-account-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	require.NoError(t, database.Create(root).Error)
	t.Cleanup(func() { require.NoError(t, database.Unscoped().Delete(root).Error) })
	versionQuery := "SELECT VERSION()"
	if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
		versionQuery = "SELECT sqlite_version()"
	}
	var version string
	require.NoError(t, database.Raw(versionQuery).Scan(&version).Error)
	t.Logf("database=%s version=%s", common.MainDatabaseType(), version)

	name := "cloudflare-multi-account-regression"
	legacyName := "cloudflare-legacy-regression"
	callCloudflareChannelAPI(t, root.Id, AddChannel, AddChannelRequest{
		Mode: "single",
		Channel: &model.Channel{Name: legacyName, Type: constant.ChannelCloudflare, Status: common.ChannelStatusEnabled,
			Key: " legacy-token ", Other: "legacy-account", Models: "public-model", Group: "default"},
	})
	var legacy model.Channel
	require.NoError(t, database.Where("name = ?", legacyName).First(&legacy).Error)
	t.Cleanup(func() { require.NoError(t, legacy.Delete()) })
	assert.Equal(t, " legacy-token ", legacy.Key)
	assert.Equal(t, "legacy-account", legacy.Other)
	assert.False(t, legacy.ChannelInfo.IsMultiKey)

	mapping := `{"public-model":"@cf/test/model"}`
	callCloudflareChannelAPI(t, root.Id, AddChannel, AddChannelRequest{
		Mode: "multi_to_single", MultiKeyMode: constant.MultiKeyModePolling,
		Channel: &model.Channel{Name: name, Type: constant.ChannelCloudflareMulti, Status: common.ChannelStatusEnabled,
			Key: `[{"api_key":"secret-one","account_id":"account-one"},{"account_id":"account-two","api_key":"secret-two"}]`,
			Models: "public-model", Group: "default", ModelMapping: &mapping},
	})
	var stored model.Channel
	require.NoError(t, database.Where("name = ?", name).First(&stored).Error)
	t.Cleanup(func() { require.NoError(t, stored.Delete()) })
	assert.Equal(t, 2, stored.ChannelInfo.MultiKeySize)
	assert.Equal(t, "public-model", stored.Models)
	require.NotNil(t, stored.ModelMapping)
	assert.Equal(t, mapping, *stored.ModelMapping)
	assert.Empty(t, stored.Other)
	callCloudflareChannelAPI(t, root.Id, UpdateChannel, map[string]any{"id": stored.Id, "name": name + " renamed"})
	unchanged, err := model.GetChannelById(stored.Id, true)
	require.NoError(t, err)
	assert.Equal(t, stored.Key, unchanged.Key, "metadata edits must preserve the saved key identities")
	// The structured key must still require the existing sensitive-write
	// permission, even when the request omits the channel type.
	body, err := common.Marshal(map[string]any{
		"id": stored.Id, "key": `{"account_id":"attacker-account","api_key":"attacker-token"}`,
	})
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("id", 0)
	ctx.Set("role", common.RoleCommonUser)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/channel/", bytes.NewReader(body))
	UpdateChannel(ctx)
	var denied struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &denied))
	assert.False(t, denied.Success)
	unchanged, err = model.GetChannelById(stored.Id, true)
	require.NoError(t, err)
	assert.Equal(t, stored.Key, unchanged.Key)
	first, index, apiErr := stored.GetNextEnabledKey()
	require.Nil(t, apiErr)
	assert.Equal(t, 0, index)
	credential, err := cloudflaremulti.ParseCredential(first)
	require.NoError(t, err)
	assert.Equal(t, "account-one", credential.AccountID)
	require.True(t, model.UpdateChannelStatus(stored.Id, first, common.ChannelStatusAutoDisabled, "test upstream rejected key"))
	loaded, err := model.GetChannelById(stored.Id, true)
	require.NoError(t, err)
	second, index, apiErr := loaded.GetNextEnabledKey()
	require.Nil(t, apiErr)
	assert.Equal(t, 1, index)
	credential, err = cloudflaremulti.ParseCredential(second)
	require.NoError(t, err)
	assert.Equal(t, "account-two", credential.AccountID)
	preview := callCloudflareChannelAPI(t, root.Id, ManageMultiKeys, MultiKeyManageRequest{ChannelId: stored.Id, Action: "get_key_status"})
	assert.NotContains(t, preview.Body.String(), "secret-one")
	assert.NotContains(t, preview.Body.String(), "secret-two")
	assert.Contains(t, preview.Body.String(), "account-two")
	callCloudflareChannelAPI(t, root.Id, ManageMultiKeys, MultiKeyManageRequest{ChannelId: stored.Id, Action: "delete_key", KeyIndex: common.GetPointer(0)})
	// Partial updates must resolve the saved Cloudflare type; arrays must be
	// normalized before the existing append/dedup path, not stored as one key.
	callCloudflareChannelAPI(t, root.Id, UpdateChannel, map[string]any{
		"id": stored.Id, "key_mode": "append",
		"key": `[{"account_id":"account-three","api_key":"secret-three"}]`,
	})
	loaded, err = model.GetChannelById(stored.Id, true)
	require.NoError(t, err)
	require.Len(t, loaded.GetKeys(), 2)
	for index, accountID := range []string{"account-two", "account-three"} {
		credential, err := cloudflaremulti.ParseCredential(loaded.GetKeys()[index])
		require.NoError(t, err)
		assert.Equal(t, accountID, credential.AccountID)
	}
	assert.Equal(t, 2, loaded.ChannelInfo.MultiKeySize)
}

func callCloudflareChannelAPI(t *testing.T, rootID int, handler gin.HandlerFunc, payload any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := common.Marshal(payload)
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set("id", rootID)
	c.Set("role", common.RoleRootUser)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/channel/", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	handler(c)
	var response struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success, recorder.Body.String())
	return recorder
}

func TestCloudflareRejectsInvalidAccountsBeforeSaving(t *testing.T) {
	for _, key := range []string{
		`{"account_id":"../wrong-account","api_key":"secret-invalid"}`,
		`[{"account_id":"account-one","api_key":"secret-invalid"},{}]`,
		`{"account_id":"account-one","api_key":null}`,
	} {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		body, err := common.Marshal(AddChannelRequest{Mode: "multi_to_single", Channel: &model.Channel{Type: constant.ChannelCloudflareMulti, Name: "invalid-cloudflare", Key: key}})
		require.NoError(t, err)
		ctx.Request = httptest.NewRequest(http.MethodPost, "/api/channel/", bytes.NewReader(body))
		AddChannel(ctx)
		assert.Contains(t, recorder.Body.String(), cloudflaremulti.InvalidCredentials)
		assert.NotContains(t, recorder.Body.String(), "secret-invalid")
	}
}
