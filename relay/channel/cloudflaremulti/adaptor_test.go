package cloudflaremulti_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay"
	"github.com/QuantumNous/new-api/relay/channel/cloudflaremulti"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeCloudflareCredentials(t *testing.T) {
	tests := []struct {
		name, input string
		want        []string
	}{
		{name: "JSON array compacts each independent account", input: `[
			{"api_key":" token-one ","account_id":" account-one "},
			{"account_id":"account-two","api_key":"token-two"}
		]`, want: []string{`{"account_id":"account-one","api_key":"token-one"}`, `{"account_id":"account-two","api_key":"token-two"}`}},
		{name: "multiline object is a single key", input: "{\n\"account_id\":\"account-one\",\n\"api_key\":\"token-one\"\n}", want: []string{`{"account_id":"account-one","api_key":"token-one"}`}},
		{name: "newline objects", input: "{\"api_key\":\"token-one\",\"account_id\":\"account-one\"}\n\n{\"account_id\":\"account-two\",\"api_key\":\"token-two\"}\n", want: []string{`{"account_id":"account-one","api_key":"token-one"}`, `{"account_id":"account-two","api_key":"token-two"}`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keys, err := cloudflaremulti.NormalizeKeys(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.want, keys)
		})
	}
	for _, input := range []string{
		"", "plain-key-without-an-account", "[]", "null", `["plain-key"]`,
		`{"account_id":"account","api_key":"do-not-leak","proxy":"http://proxy:8080"}`,
		`{"account_id":null,"api_key":"do-not-leak"}`,
		`{"account_id":"account","api_key":null}`,
		`{"account_id":"account","api_key":42}`,
		`{"account_id":"../another-account","api_key":"do-not-leak"}`,
		`{"account_id":"account?query","api_key":"do-not-leak"}`,
		`{"account_id":"account","api_key":"do-not-leak\r\nX-Injected: yes"}`,
		`{"account_id":"account","api_key":"do-not-leak"`,
	} {
		t.Run("invalid/"+input, func(t *testing.T) {
			_, err := cloudflaremulti.NormalizeKeys(input)
			require.Error(t, err)
			assert.EqualError(t, err, cloudflaremulti.InvalidCredentials)
			assert.NotContains(t, err.Error(), "do-not-leak")
		})
	}
}

func TestCloudflareSelectedAccountRequest(t *testing.T) {
	service.InitHttpClient()
	t.Cleanup(service.ResetProxyClientCache)
	type upstreamRequest struct {
		path, authorization, override string
	}
	received := make(chan upstreamRequest, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- upstreamRequest{r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("X-Selected-Key")}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(upstream.Close)
	for _, tt := range []struct {
		name, key, account, token, endpoint string
		mode, channelType                  int
	}{
		{"legacy channel", "legacy-token", "shared-account", "legacy-token", "v1/chat/completions", relayconstant.RelayModeChatCompletions, constant.ChannelCloudflare},
		{"first account", `{"api_key":"token-one","account_id":"account-one"}`, "account-one", "token-one", "v1/chat/completions", relayconstant.RelayModeChatCompletions, constant.ChannelCloudflareMulti},
		{"second account embeddings", `{"account_id":"account-two","api_key":"token-two"}`, "account-two", "token-two", "v1/embeddings", relayconstant.RelayModeEmbeddings, constant.ChannelCloudflareMulti},
		{"responses", `{"account_id":"account-one","api_key":"token-one"}`, "account-one", "token-one", "v1/responses", relayconstant.RelayModeResponses, constant.ChannelCloudflareMulti},
		{"native completion", `{"account_id":"account-two","api_key":"token-two"}`, "account-two", "token-two", "run/@cf/test/model", relayconstant.RelayModeCompletions, constant.ChannelCloudflareMulti},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
			common.SetContextKey(c, constant.ContextKeyChannelKey, tt.key)
			info := &relaycommon.RelayInfo{RelayMode: tt.mode, ChannelMeta: &relaycommon.ChannelMeta{
				ChannelBaseUrl: upstream.URL, ApiVersion: "shared-account", ApiKey: tt.key, UpstreamModelName: "@cf/test/model",
				HeadersOverride: map[string]any{"X-Selected-Key": "{api_key}"},
			}}
			apiType, ok := common.ChannelType2APIType(tt.channelType)
			require.True(t, ok)
			adaptor := relay.GetAdaptor(apiType)
			require.NotNil(t, adaptor)
			adaptor.Init(info)
			result, err := adaptor.DoRequest(c, info, strings.NewReader(`{}`))
			require.NoError(t, err)
			response, ok := result.(*http.Response)
			require.True(t, ok)
			_, err = io.Copy(io.Discard, response.Body)
			require.NoError(t, err)
			require.NoError(t, response.Body.Close())
			request := <-received
			assert.Equal(t, "/client/v4/accounts/"+tt.account+"/ai/"+tt.endpoint, request.path)
			assert.Equal(t, "Bearer "+tt.token, request.authorization)
			assert.Equal(t, tt.token, request.override)
			assert.Equal(t, tt.key, common.GetContextKeyString(c, constant.ContextKeyChannelKey), "key failure tracking must retain the selected stored entry")
		})
	}
}

func TestCloudflareInvalidCredentialIsNotSent(t *testing.T) {
	adaptor := &cloudflaremulti.Adaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: `{"account_id":"account","api_key":"secret\ninvalid"}`}}
	adaptor.Init(info)
	_, err := adaptor.GetRequestURL(info)
	require.EqualError(t, err, cloudflaremulti.InvalidCredentials)
	headers := http.Header{}
	assert.EqualError(t, adaptor.SetupRequestHeader(nil, &headers, info), cloudflaremulti.InvalidCredentials)
	assert.Empty(t, headers)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	_, err = adaptor.DoRequest(c, info, strings.NewReader(`{}`))
	require.ErrorContains(t, err, cloudflaremulti.InvalidCredentials)
}
