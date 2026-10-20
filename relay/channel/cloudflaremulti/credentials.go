package cloudflaremulti

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

const InvalidCredentials = "Invalid Cloudflare account credentials: use JSON objects with account_id and api_key"

type Credential struct {
	AccountID string `json:"account_id"`
	APIKey    string `json:"api_key"`
}

// ParseCredential resolves one selected key without changing its
// stored identity, which multi-key failure tracking uses to disable that key.
// Errors deliberately exclude the input and JSON decoder errors (credentials).
func ParseCredential(key string) (Credential, error) {
	var fields map[string]json.RawMessage
	if err := common.UnmarshalJsonStr(strings.TrimSpace(key), &fields); err != nil || len(fields) != 2 {
		return Credential{}, errors.New(InvalidCredentials)
	}
	if _, ok := fields["account_id"]; !ok {
		return Credential{}, errors.New(InvalidCredentials)
	}
	if _, ok := fields["api_key"]; !ok {
		return Credential{}, errors.New(InvalidCredentials)
	}
	var credential Credential
	if err := common.UnmarshalJsonStr(key, &credential); err != nil {
		return Credential{}, errors.New(InvalidCredentials)
	}
	credential.AccountID = strings.TrimSpace(credential.AccountID)
	credential.APIKey = strings.TrimSpace(credential.APIKey)
	if credential.AccountID == "" || credential.APIKey == "" {
		return Credential{}, errors.New(InvalidCredentials)
	}
	// Account IDs are URL path segments, never paths, queries, or URLs.
	for _, char := range credential.AccountID {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '_' || char == '-' {
			continue
		}
		return Credential{}, errors.New(InvalidCredentials)
	}
	for _, char := range credential.APIKey {
		if char <= ' ' || char >= 127 {
			return Credential{}, errors.New(InvalidCredentials)
		}
	}
	if strings.ContainsAny(credential.APIKey, "{}[]\"") {
		return Credential{}, errors.New(InvalidCredentials)
	}
	return credential, nil
}

// NormalizeKeys accepts a JSON object, an array of credential objects,
// or newline-separated objects. Persist one compact entry per line
// so the existing append, delete, polling, and disabled-key paths keep working.
func NormalizeKeys(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New(InvalidCredentials)
	}
	var entries []string
	if strings.HasPrefix(raw, "[") {
		var array []json.RawMessage
		if err := common.UnmarshalJsonStr(raw, &array); err != nil || len(array) == 0 {
			return nil, errors.New(InvalidCredentials)
		}
		for _, entry := range array {
			if !strings.HasPrefix(strings.TrimSpace(string(entry)), "{") {
				return nil, errors.New(InvalidCredentials)
			}
			entries = append(entries, string(entry))
		}
	} else if strings.HasPrefix(raw, "{") {
		var object map[string]json.RawMessage
		if err := common.UnmarshalJsonStr(raw, &object); err == nil {
			entries = []string{raw}
		}
	}
	if entries == nil {
		for entry := range strings.SplitSeq(raw, "\n") {
			if entry = strings.TrimSpace(entry); entry != "" {
				entries = append(entries, entry)
			}
		}
	}
	for index, entry := range entries {
		credential, err := ParseCredential(entry)
		if err != nil {
			return nil, err
		}
		encoded, err := common.Marshal(credential)
		if err != nil {
			return nil, errors.New(InvalidCredentials)
		}
		entries[index] = string(encoded)
	}
	return entries, nil
}
