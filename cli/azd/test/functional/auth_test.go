// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cli_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/stretchr/testify/assert"
)

func Test_CLI_Auth_ExternalAuth(t *testing.T) {
	// running this test in parallel is ok as it uses a t.TempDir()
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	t.Logf("DIR: %s", dir)

	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir

	// We spin up a small server here to serve a fixed response to the auth request, and
	// then observe the returned key from `azd auth token --output=json` to ensure it matches
	// what we handed back.

	// nolint:gosec
	expectedToken := "THIS-IS-A-FAKE-TOKEN"
	expectedExpiresOn := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		if r.URL.Path != "/token" {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}

		if r.URL.Query().Get("api-version") != "2023-07-12-preview" {
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}

		authHeader := r.Header.Get("Authorization")
		if authHeader != "Bearer a-fake-key" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		type tokenResponse struct {
			Status    string `json:"status"`
			Token     string `json:"token"`
			ExpiresOn string `json:"expiresOn"`
		}

		response := tokenResponse{
			Status:    "success",
			Token:     expectedToken,
			ExpiresOn: expectedExpiresOn,
		}

		w.Header().Set("Content-Type", "application/json")
		err := json.NewEncoder(w).Encode(response)
		assert.NoError(t, err)
	}))

	defer server.Close()

	cli.Env = append(os.Environ(),
		fmt.Sprintf("AZD_AUTH_ENDPOINT=%s", server.URL),
		fmt.Sprintf("AZD_AUTH_KEY=%s", "a-fake-key"),
	)

	res, err := cli.RunCommand(ctx, "auth", "token", "--output=json")
	assert.NoError(t, err)
	assert.Equal(t, 0, res.ExitCode)

	var token struct {
		Token     string `json:"token"`
		ExpiresOn string `json:"expiresOn"`
	}

	err = json.Unmarshal([]byte(res.Stdout), &token)
	assert.NoError(t, err)

	assert.Equal(t, expectedToken, token.Token)
	assert.Equal(t, expectedExpiresOn, token.ExpiresOn)
}
