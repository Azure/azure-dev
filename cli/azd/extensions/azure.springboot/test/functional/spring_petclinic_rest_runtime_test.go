// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cli_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This local password/sslmode=disable test is not Azure managed-identity or verify-full acceptance.
func assertNativeRestLocalPostgres(t *testing.T, ctx context.Context, image, projectDir string) {
	t.Helper()
	nonce := hex.EncodeToString(randBytes(t, 12))
	ownerLabel := "azd.spring.local.owner"
	dockerConfig := t.TempDir()
	docker := func(ctx context.Context, input string, environment []string, arguments ...string) (string, error) {
		// #nosec G204 -- fixed docker executable; callers supply fixed subcommands and test-created resource identities.
		command := exec.CommandContext(ctx, "docker", arguments...)
		command.Env = append(environmentWithout(os.Environ(), "DOCKER_CONFIG"), "DOCKER_CONFIG="+dockerConfig)
		command.Env = append(command.Env, environment...)
		command.Stdin = strings.NewReader(input)
		output, err := command.CombinedOutput()
		return strings.TrimSpace(string(output)), err
	}
	run := func(arguments ...string) string {
		t.Helper()
		output, err := docker(ctx, "", nil, arguments...)
		require.NoError(t, err, output)
		return output
	}
	registerCleanup := func(kind, id string) {
		t.Helper()
		require.Len(t, id, 64)
		_, err := hex.DecodeString(id)
		require.NoError(t, err)
		t.Cleanup(func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			label, err := docker(cleanupCtx, "", nil, kind, "inspect", id,
				"--format", fmt.Sprintf(`{{index .%s "%s"}}`, map[string]string{
					"container": "Config.Labels", "network": "Labels",
				}[kind], ownerLabel))
			if err != nil || label != nonce {
				t.Errorf("refusing cleanup of an unverifiable %s identity: %v", kind, err)
				return
			}
			arguments := []string{kind, "rm", id}
			if kind == "container" {
				arguments = []string{"container", "rm", "--force", "--volumes", id}
			}
			output, err := docker(cleanupCtx, "", nil, arguments...)
			if err != nil {
				t.Errorf("cleaning up this test's owned %s: %v: %s", kind, err, output)
			}
		})
	}
	network := run("network", "create", "--label", ownerLabel+"="+nonce, "spring-rest-"+nonce)
	registerCleanup("network", network)
	password := hex.EncodeToString(randBytes(t, 32))
	database, err := docker(ctx, "", []string{"POSTGRES_PASSWORD=" + password}, "run", "--detach",
		"--network", network, "--network-alias", "database", "--label", ownerLabel+"="+nonce,
		"--env", "POSTGRES_PASSWORD", "postgres:16-alpine")
	require.NoError(t, err, database)
	registerCleanup("container", database)
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		_, err := docker(ctx, "", nil, "exec", database, "pg_isready", "-U", "postgres")
		assert.NoError(collect, err)
	}, time.Minute, time.Second)
	sql := fmt.Sprintf("CREATE ROLE petclinic_app LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD '%s';\n"+
		"CREATE DATABASE petclinic;\n", password)
	output, err := docker(ctx, sql, nil, "exec", "-i", database, "psql", "-U", "postgres", "-v", "ON_ERROR_STOP=1")
	require.NoError(t, err, output)
	output, err = docker(ctx, "GRANT USAGE, CREATE ON SCHEMA public TO petclinic_app;\n", nil,
		"exec", "-i", database, "psql", "-U", "postgres", "-d", "petclinic", "-v", "ON_ERROR_STOP=1")
	require.NoError(t, err, output)

	// Replacing only the local test's external configuration removes the Entra driver map unambiguously.
	properties, err := os.ReadFile(filepath.Join(projectDir, "src", "main", "resources", "application.properties"))
	require.NoError(t, err)
	var localProperties []string
	for line := range strings.SplitSeq(string(properties), "\n") {
		key := strings.TrimSpace(line)
		if strings.HasPrefix(key, "spring.datasource.azure.") ||
			strings.HasPrefix(key, "spring.datasource.hikari.data-source-properties") {
			continue
		}
		localProperties = append(localProperties, line)
	}
	configDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "local.properties"),
		[]byte(strings.Join(localProperties, "\n")), 0o600))
	application, err := docker(ctx, "", []string{
		"POSTGRES_URL=jdbc:postgresql://database:5432/petclinic?sslmode=disable",
		"SPRING_DATASOURCE_PASSWORD=" + password,
	}, "run", "--detach", "--network", network, "--label", ownerLabel+"="+nonce,
		"--publish", "127.0.0.1::9966", "--mount", "type=bind,source="+configDir+",target=/trial,readonly",
		"--env", "POSTGRES_URL", "--env", "SPRING_DATASOURCE_PASSWORD",
		image, "--spring.config.location=file:/trial/local.properties")
	require.NoError(t, err, application)
	registerCleanup("container", application)
	localEndpoint := func() string {
		t.Helper()
		port := run("port", application, "9966/tcp")
		require.True(t, strings.HasPrefix(port, "127.0.0.1:"))
		require.NotContains(t, port, "\n")
		return "http://" + port + "/petclinic"
	}
	endpoint := localEndpoint()
	client := &http.Client{Timeout: 3 * time.Second}
	waitReady := func() {
		t.Helper()
		ready := false
		deadline := time.NewTimer(2 * time.Minute)
		defer deadline.Stop()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for !ready {
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/api/owners", nil)
			require.NoError(t, err)
			response, err := client.Do(request)
			if err == nil {
				ready = response.StatusCode == http.StatusOK
				require.NoError(t, response.Body.Close())
			}
			if ready {
				return
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-deadline.C:
				logs := strings.Split(run("logs", "--tail", "100", application), "\n")
				for i, line := range logs {
					if strings.Contains(line, "Using generated security password:") {
						logs[i] = "Using generated security password: [REDACTED]"
					}
				}
				t.Fatalf("native REST app did not become ready: %s", strings.Join(logs, "\n"))
			case <-ticker.C:
			}
		}
	}
	waitReady()
	run("exec", application, "test", "-s", "/etc/ssl/certs/ca-certificates.crt")
	require.Contains(t, run("exec", application, "java", "-version"), `version "17.`)
	account := run("exec", database, "psql", "-U", "postgres", "-d", "petclinic", "-At", "-c",
		"SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid='public.owners'::regclass")
	require.Equal(t, "petclinic_app", account)
	require.Equal(t, "f|f|f", run("exec", database, "psql", "-U", "postgres", "-At", "-c",
		"SELECT rolsuper,rolcreatedb,rolcreaterole FROM pg_roles WHERE rolname='petclinic_app'"))
	input := map[string]any{"firstName": "Local", "lastName": "Native",
		"address": "1 Test Street", "city": "TestCity", "telephone": "1234567890"}
	created := petclinicRestRequest(t, client, http.MethodPost, endpoint, "/owners", input, http.StatusCreated)
	var actual struct {
		ID   int    `json:"id"`
		City string `json:"city"`
	}
	require.NoError(t, json.Unmarshal(created, &actual))
	require.Positive(t, actual.ID)
	path := fmt.Sprintf("/owners/%d", actual.ID)
	input["city"] = "UpdatedCity"
	petclinicRestRequest(t, client, http.MethodPut, endpoint, path, input, http.StatusNoContent)
	run("restart", application)
	restartedEndpoint := localEndpoint()
	t.Logf("restart changed Docker's ephemeral host port: %t", restartedEndpoint != endpoint)
	endpoint = restartedEndpoint
	waitReady()
	read := petclinicRestRequest(t, client, http.MethodGet, endpoint, path, nil, http.StatusOK)
	require.NoError(t, json.Unmarshal(read, &actual))
	require.Equal(t, "UpdatedCity", actual.City)
	petclinicRestRequest(t, client, http.MethodDelete, endpoint, path, nil, http.StatusNoContent)
	petclinicRestRequest(t, client, http.MethodGet, endpoint, path, nil, http.StatusNotFound)
	t.Log("native REST runtime: Java 17, readable CA bundle, ordinary PostgreSQL 16 role, JSON CRUD and restart persistence")
}

func randBytes(t *testing.T, count int) []byte {
	t.Helper()
	value := make([]byte, count)
	_, err := rand.Read(value)
	require.NoError(t, err)
	return value
}
