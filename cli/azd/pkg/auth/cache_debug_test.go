// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"bytes"
	"log"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMsalCacheTracer_LogSnapshotOnce_HashesTokenSecrets(t *testing.T) {
	t.Setenv(azdDebugMsalCacheEnv, "true")

	cacheJSON := []byte(`{
		"RefreshToken": {
			"rt-key": {
				"home_account_id": "home-1",
				"environment": "login.microsoftonline.com",
				"client_id": "client-1",
				"family_id": "",
				"secret": "super-secret-refresh-token"
			}
		},
		"AccessToken": {
			"at-key": {
				"home_account_id": "home-1",
				"realm": "tenant-1",
				"cached_at": 100,
				"expires_on": 200
			}
		},
		"Account": {
			"acct-key": {
				"home_account_id": "home-1",
				"realm": "tenant-1",
				"username": "user@example.com"
			}
		}
	}`)

	cache := &memoryCache{
		cache: map[string][]byte{currentUserCacheKey: cacheJSON},
	}
	tracer := newMsalCacheTracer(cache)

	var buf bytes.Buffer
	originalWriter := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() {
		log.SetOutput(originalWriter)
	})

	tracer.LogSnapshotOnce("test-phase")
	tracer.LogSnapshotOnce("test-phase")

	output := buf.String()
	require.NotEmpty(t, output)
	assert.Contains(t, output, "msal-cache[test-phase]: refresh_tokens=1 access_tokens=1 accounts=1")
	assert.Contains(t, output, shortDigest("super-secret-refresh-token"))
	assert.NotContains(t, output, "super-secret-refresh-token")
	assert.Equal(t, 1, strings.Count(output, "msal-cache[test-phase]: refresh_tokens=1 access_tokens=1 accounts=1"))

	// PII warning banner should appear exactly once
	assert.Contains(t, output, "WARNING: MSAL cache tracing enabled")
	assert.Equal(t, 1, strings.Count(output, "WARNING: MSAL cache tracing enabled"))
}
