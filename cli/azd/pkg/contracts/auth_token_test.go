// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package contracts

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRFC3339TimeJson(t *testing.T) {
	tm, err := time.Parse(time.RFC3339Nano, "2023-01-09T06:39:00.313323855Z")

	require.NoError(t, err)

	stdRes, err := json.Marshal(tm)
	require.NoError(t, err)

	cusRes, err := json.Marshal(RFC3339Time(tm))
	require.NoError(t, err)

	assert.Equal(t, `"2023-01-09T06:39:00.313323855Z"`, string(stdRes))
	assert.Equal(t, `"2023-01-09T06:39:00Z"`, string(cusRes))
}

func TestStatusResultJsonWithExpiresOn(t *testing.T) {
	tm, err := time.Parse(time.RFC3339, "2026-03-22T14:30:00Z")
	require.NoError(t, err)

	expiresOn := RFC3339Time(tm)
	res := StatusResult{
		Status:    AuthStatusAuthenticated,
		Type:      AccountTypeUser,
		Email:     "user@example.com",
		ExpiresOn: &expiresOn,
	}

	data, err := json.Marshal(res)
	require.NoError(t, err)

	var parsed StatusResult
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, AuthStatusAuthenticated, parsed.Status)
	assert.Equal(t, AccountTypeUser, parsed.Type)
	assert.Equal(t, "user@example.com", parsed.Email)
	require.NotNil(t, parsed.ExpiresOn)
	assert.Equal(t, tm, time.Time(*parsed.ExpiresOn))
}

func TestStatusResultJsonWithoutExpiresOn(t *testing.T) {
	res := StatusResult{
		Status: AuthStatusUnauthenticated,
	}

	data, err := json.Marshal(res)
	require.NoError(t, err)

	assert.NotContains(t, string(data), "expiresOn")

	var parsed StatusResult
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, AuthStatusUnauthenticated, parsed.Status)
	assert.Nil(t, parsed.ExpiresOn)
}
