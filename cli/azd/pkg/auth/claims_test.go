// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// helper: build a minimal JWT from a claims map.
func buildTestJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString(
		[]byte(`{"alg":"none","typ":"JWT"}`))
	body, err := json.Marshal(claims)
	require.NoError(t, err)
	payload := base64.RawURLEncoding.EncodeToString(body)
	sig := base64.RawURLEncoding.EncodeToString(
		[]byte("fakesig"))
	return fmt.Sprintf("%s.%s.%s", header, payload, sig)
}

// ---------- TokenClaims.LocalAccountId ----------

func TestTokenClaims_LocalAccountId(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		claims TokenClaims
		wantID string
	}{
		{
			"oid_present",
			TokenClaims{Oid: "oid-123", Subject: "sub-456"},
			"oid-123",
		},
		{
			"oid_empty_fallback_to_sub",
			TokenClaims{Subject: "sub-456"},
			"sub-456",
		},
		{
			"both_empty",
			TokenClaims{},
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.wantID, tt.claims.LocalAccountId())
		})
	}
}

// ---------- TokenClaims.DisplayUsername ----------

func TestTokenClaims_DisplayUsername(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		claims TokenClaims
		want   string
	}{
		{
			"preferred_username_v2",
			TokenClaims{
				PreferredUsername: "user@example.com",
				UniqueName:        "legacy@example.com",
			},
			"user@example.com",
		},
		{
			"fallback_to_unique_name_v1",
			TokenClaims{UniqueName: "legacy@example.com"},
			"legacy@example.com",
		},
		{
			"both_empty",
			TokenClaims{},
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.claims.DisplayUsername())
		})
	}
}

// ---------- GetClaimsFromAccessToken ----------

func TestGetClaimsFromAccessToken(t *testing.T) {
	t.Parallel()

	t.Run("full_claims", func(t *testing.T) {
		t.Parallel()
		token := buildTestJWT(t, map[string]any{
			"oid":                "oid-abc",
			"tid":                "tenant-xyz",
			"preferred_username": "user@contoso.com",
			"sub":                "sub-123",
			"name":               "Test User",
			"iss":                "https://login.microsoftonline.com/tid/v2.0",
		})

		claims, err := GetClaimsFromAccessToken(token)
		require.NoError(t, err)
		assert.Equal(t, "oid-abc", claims.Oid)
		assert.Equal(t, "tenant-xyz", claims.TenantId)
		assert.Equal(t, "user@contoso.com",
			claims.PreferredUsername)
		assert.Equal(t, "sub-123", claims.Subject)
		assert.Equal(t, "Test User", claims.Name)
	})

	t.Run("malformed_not_jwt", func(t *testing.T) {
		t.Parallel()
		_, err := GetClaimsFromAccessToken("not-a-jwt")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "malformed")
	})

	t.Run("malformed_two_segments", func(t *testing.T) {
		t.Parallel()
		_, err := GetClaimsFromAccessToken("a.b")
		require.Error(t, err)
	})

	t.Run("bad_base64_payload", func(t *testing.T) {
		t.Parallel()
		// Valid 3-segment structure but payload is not
		// valid base64url.
		_, err := GetClaimsFromAccessToken("aaa.!!!.ccc")
		require.Error(t, err)
	})

	t.Run("invalid_json_payload", func(t *testing.T) {
		t.Parallel()
		notJSON := base64.RawURLEncoding.EncodeToString(
			[]byte("not json"))
		token := fmt.Sprintf("aaa.%s.ccc", notJSON)
		_, err := GetClaimsFromAccessToken(token)
		require.Error(t, err)
	})

	t.Run("empty_claims", func(t *testing.T) {
		t.Parallel()
		token := buildTestJWT(t, map[string]any{})
		claims, err := GetClaimsFromAccessToken(token)
		require.NoError(t, err)
		assert.Empty(t, claims.Oid)
		assert.Empty(t, claims.TenantId)
	})
}

// ---------- LoginScopes / LoginScopesFull ----------

func TestLoginScopes(t *testing.T) {
	t.Parallel()

	c := cloud.AzurePublic()
	scopes := LoginScopes(c)

	require.Len(t, scopes, 1)
	assert.Contains(t, scopes[0], "management.azure.com")
	assert.True(t, strings.HasSuffix(scopes[0], "//.default"))
}

func TestLoginScopesFull(t *testing.T) {
	t.Parallel()

	c := cloud.AzurePublic()
	scopes := LoginScopesFull(c)

	require.Len(t, scopes, 2)
	for _, s := range scopes {
		assert.True(t, strings.HasSuffix(s, "//.default"),
			"scope %q should end with //.default", s)
	}
	// The two scopes should differ (one from Endpoint, one
	// from Audience with trailing slash removed).
	assert.NotEqual(t, scopes[0], scopes[1])
}

// ---------- memoryCache ----------

func TestMemoryCache_ReadAndSet(t *testing.T) {
	t.Parallel()

	t.Run("read_missing_key_no_inner", func(t *testing.T) {
		t.Parallel()
		mc := &memoryCache{cache: map[string][]byte{}}
		_, err := mc.Read("missing")
		require.Error(t, err)
	})

	t.Run("set_and_read_round_trip", func(t *testing.T) {
		t.Parallel()
		mc := &memoryCache{cache: map[string][]byte{}}
		err := mc.Set("key1", []byte("value1"))
		require.NoError(t, err)

		val, err := mc.Read("key1")
		require.NoError(t, err)
		assert.Equal(t, []byte("value1"), val)
	})

	t.Run("set_same_value_is_noop", func(t *testing.T) {
		t.Parallel()
		inner := &countingCache{}
		mc := &memoryCache{
			cache: map[string][]byte{},
			inner: inner,
		}
		err := mc.Set("k", []byte("v"))
		require.NoError(t, err)
		assert.Equal(t, 1, inner.setCalls)

		// Set same value again — inner should NOT be called.
		err = mc.Set("k", []byte("v"))
		require.NoError(t, err)
		assert.Equal(t, 1, inner.setCalls)
	})

	t.Run("set_different_value_propagates", func(t *testing.T) {
		t.Parallel()
		inner := &countingCache{}
		mc := &memoryCache{
			cache: map[string][]byte{},
			inner: inner,
		}
		err := mc.Set("k", []byte("v1"))
		require.NoError(t, err)
		assert.Equal(t, 1, inner.setCalls)

		err = mc.Set("k", []byte("v2"))
		require.NoError(t, err)
		assert.Equal(t, 2, inner.setCalls)
	})

	t.Run("read_falls_through_to_inner", func(t *testing.T) {
		t.Parallel()
		inner := &countingCache{
			data: map[string][]byte{
				"from-inner": []byte("inner-val"),
			},
		}
		mc := &memoryCache{
			cache: map[string][]byte{},
			inner: inner,
		}
		val, err := mc.Read("from-inner")
		require.NoError(t, err)
		assert.Equal(t, []byte("inner-val"), val)
	})

	t.Run("concurrent_read_and_set", func(t *testing.T) {
		// Use an inner cache to exercise the full Set path (read old → inner.Set → map write).
		inner := &memoryCache{cache: map[string][]byte{}}
		mc := &memoryCache{cache: map[string][]byte{}, inner: inner}

		var wg sync.WaitGroup
		for i := range 8 {
			writerID := i
			wg.Go(func() {
				for j := range 200 {
					value := fmt.Appendf(nil, "value-%d-%d", writerID, j)
					_ = mc.Set("shared-key", value)
					_, _ = mc.Read("shared-key")
				}
			})
		}

		wg.Wait()

		// Both the in-memory map and the inner cache must agree.
		val, err := mc.Read("shared-key")
		require.NoError(t, err)
		assert.NotEmpty(t, val)

		innerVal, err := inner.Read("shared-key")
		require.NoError(t, err)
		assert.Equal(t, val, innerVal)
	})

}

// ---------- fixedMarshaller ----------

func TestFixedMarshaller(t *testing.T) {
	t.Parallel()

	t.Run("marshal_returns_current_value", func(t *testing.T) {
		t.Parallel()
		fm := &fixedMarshaller{val: []byte("hello")}
		data, err := fm.Marshal()
		require.NoError(t, err)
		assert.Equal(t, []byte("hello"), data)
	})

	t.Run("unmarshal_sets_value", func(t *testing.T) {
		t.Parallel()
		fm := &fixedMarshaller{}
		err := fm.Unmarshal([]byte("new-data"))
		require.NoError(t, err)
		data, err := fm.Marshal()
		require.NoError(t, err)
		assert.Equal(t, []byte("new-data"), data)
	})

	t.Run("nil_initial_value", func(t *testing.T) {
		t.Parallel()
		fm := &fixedMarshaller{}
		data, err := fm.Marshal()
		require.NoError(t, err)
		assert.Nil(t, data)
	})
}

// ---------- AuthFailedError ----------

func TestAuthFailedError_Error_NonHTTP(t *testing.T) {
	t.Parallel()

	inner := errors.New("some MSAL error")
	e := &AuthFailedError{innerErr: inner}

	msg := e.Error()
	assert.Contains(t, msg, "failed to authenticate")
	assert.Contains(t, msg, "some MSAL error")
}

func TestAuthFailedError_Error_WithParsedResponse(t *testing.T) {
	t.Parallel()

	e := &AuthFailedError{
		RawResp: &http.Response{},
		Parsed: &AadErrorResponse{
			Error:            "invalid_grant",
			ErrorDescription: "Token expired",
		},
		innerErr: errors.New("wrapped"),
	}

	msg := e.Error()
	assert.Contains(t, msg, "failed to authenticate")
	assert.Contains(t, msg, "invalid_grant")
	assert.Contains(t, msg, "Token expired")
}

func TestAuthFailedError_Error_UnparsedHTTP(t *testing.T) {
	t.Parallel()

	body := io.NopCloser(strings.NewReader(
		`{"error":"server_error"}`))
	resp := &http.Response{
		StatusCode: 500,
		Status:     "500 Internal Server Error",
		Body:       body,
		Request: &http.Request{
			Method: "POST",
			URL: &url.URL{
				Scheme: "https",
				Host:   "login.microsoftonline.com",
				Path:   "/tenant/oauth2/token",
			},
		},
	}

	e := &AuthFailedError{
		RawResp:  resp,
		Parsed:   nil,
		innerErr: errors.New("wrapped"),
	}

	msg := e.Error()
	assert.Contains(t, msg, "failed to authenticate")
	assert.Contains(t, msg, "POST")
	assert.Contains(t, msg, "login.microsoftonline.com")
	assert.Contains(t, msg, "500")
}

func TestAuthFailedError_Unwrap(t *testing.T) {
	t.Parallel()

	inner := errors.New("root cause")
	e := &AuthFailedError{innerErr: inner}
	assert.Equal(t, inner, e.Unwrap())
}

func TestAuthFailedError_NonRetriable(t *testing.T) {
	t.Parallel()
	// NonRetriable is a marker method — just confirm it
	// doesn't panic.
	e := &AuthFailedError{innerErr: errors.New("err")}
	e.NonRetriable()
}

// ---------- ReLoginRequiredError ----------

func TestReLoginRequiredError_Error(t *testing.T) {
	t.Parallel()

	e := &ReLoginRequiredError{errText: "token expired"}
	assert.Equal(t, "token expired", e.Error())
}

func TestReLoginRequiredError_NonRetriable(t *testing.T) {
	t.Parallel()
	e := &ReLoginRequiredError{}
	e.NonRetriable() // marker — should not panic
}

func TestNewReLoginRequiredError(t *testing.T) {
	t.Parallel()

	t.Run("nil_response_returns_false", func(t *testing.T) {
		t.Parallel()
		err, ok := newReLoginRequiredError(
			nil, nil, cloud.AzurePublic(), "")
		assert.Nil(t, err)
		assert.False(t, ok)
	})

	t.Run("unrelated_error_returns_false", func(t *testing.T) {
		t.Parallel()
		resp := &AadErrorResponse{
			Error:            "server_error",
			ErrorDescription: "something else",
		}
		err, ok := newReLoginRequiredError(
			resp, nil, cloud.AzurePublic(), "")
		assert.Nil(t, err)
		assert.False(t, ok)
	})

	t.Run("invalid_grant_returns_error", func(t *testing.T) {
		t.Parallel()
		resp := &AadErrorResponse{
			Error:            "invalid_grant",
			ErrorDescription: "AADSTS700082: expired",
		}
		err, ok := newReLoginRequiredError(
			resp,
			[]string{"https://management.azure.com//.default"},
			cloud.AzurePublic(),
			"",
		)
		assert.True(t, ok)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "AADSTS700082")
	})

	t.Run("interaction_required_returns_error", func(t *testing.T) {
		t.Parallel()
		resp := &AadErrorResponse{
			Error:            "interaction_required",
			ErrorDescription: "need consent",
		}
		err, ok := newReLoginRequiredError(
			resp,
			[]string{"https://management.azure.com//.default"},
			cloud.AzurePublic(),
			"",
		)
		assert.True(t, ok)
		require.Error(t, err)
	})

	t.Run("extra_scopes_appended_to_login_cmd", func(t *testing.T) {
		t.Parallel()
		resp := &AadErrorResponse{
			Error:            "invalid_grant",
			ErrorDescription: "expired",
		}
		err, ok := newReLoginRequiredError(
			resp,
			[]string{
				"https://management.azure.com//.default",
				"https://graph.microsoft.com//.default",
			},
			cloud.AzurePublic(),
			"",
		)
		assert.True(t, ok)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "expired")
	})

	t.Run("error_code_70043_sets_login_expired", func(t *testing.T) {
		t.Parallel()
		resp := &AadErrorResponse{
			Error:            "invalid_grant",
			ErrorDescription: "AADSTS70043: expired",
			ErrorCodes:       []int{70043},
		}
		err, ok := newReLoginRequiredError(
			resp, nil, cloud.AzurePublic(), "")
		assert.True(t, ok)
		require.Error(t, err)
	})

	t.Run("error_code_700082_sets_login_expired", func(t *testing.T) {
		t.Parallel()
		resp := &AadErrorResponse{
			Error:            "invalid_grant",
			ErrorDescription: "AADSTS700082: expired",
			ErrorCodes:       []int{700082},
		}
		err, ok := newReLoginRequiredError(resp, nil, cloud.AzurePublic(), "")
		assert.True(t, ok)
		require.Error(t, err)

		var errWithSuggestion *internal.ErrorWithSuggestion
		require.True(t, errors.As(err, &errWithSuggestion))
		assert.Contains(t, errWithSuggestion.Suggestion, "login expired")
	})

	t.Run("error_code_50005_adds_device_code_flag", func(t *testing.T) {
		t.Parallel()
		resp := &AadErrorResponse{
			Error:            "interaction_required",
			ErrorDescription: "conditional access",
			ErrorCodes:       []int{50005},
		}
		err, ok := newReLoginRequiredError(
			resp, nil, cloud.AzurePublic(), "")
		assert.True(t, ok)
		require.Error(t, err)
	})

	t.Run("tenant_id_included_in_suggestion", func(t *testing.T) {
		t.Parallel()
		resp := &AadErrorResponse{
			Error:            "invalid_grant",
			ErrorDescription: "AADSTS70043: expired",
			ErrorCodes:       []int{70043},
		}
		tenantID := "72f988bf-86f1-41af-91ab-2d7cd011db47"
		err, ok := newReLoginRequiredError(
			resp, nil, cloud.AzurePublic(), tenantID)
		assert.True(t, ok)
		require.Error(t, err)

		errWithSuggestion, ok := errors.AsType[*internal.ErrorWithSuggestion](err)
		require.True(t, ok)
		assert.Contains(t, errWithSuggestion.Suggestion, "--tenant-id "+tenantID)
	})
}

// ---------- helpers ----------

// countingCache is a test spy that records Set calls and
// supports pre-seeded Read data.
type countingCache struct {
	setCalls int
	data     map[string][]byte
}

func (c *countingCache) Read(key string) ([]byte, error) {
	if c.data != nil {
		if v, ok := c.data[key]; ok {
			return v, nil
		}
	}
	return nil, errCacheKeyNotFound
}

func (c *countingCache) Set(_ string, _ []byte) error {
	c.setCalls++
	return nil
}
