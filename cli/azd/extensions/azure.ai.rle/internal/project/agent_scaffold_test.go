// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/gorilla/websocket"
)

func TestCreateRleAgentScaffoldWritesHostedAgentFiles(t *testing.T) {
	sessionDir, err := CreateRleAgentScaffold(
		AgentScaffoldOptions{
			Kind:            AgentScaffoldKindHostedAgent,
			EnvironmentName: "support_agent",
			AgentName:       "support-agent",
			AgentVersion:    "v3",
		},
		t.TempDir(),
		false,
	)
	if err != nil {
		t.Fatal(err)
	}

	config, err := os.ReadFile(filepath.Join(sessionDir, "rle.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`name = "support_agent"`,
		`kind = "hosted_agent"`,
		`name = "support-agent"`,
		`version = "v3"`,
	} {
		if !strings.Contains(string(config), expected) {
			t.Fatalf("expected config to contain %q, got:\n%s", expected, config)
		}
	}

	server, err := os.ReadFile(filepath.Join(sessionDir, "server", "env.py"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`AGENT_NAME = "support-agent"`,
		"from openenv.core.env_server.http_server import create_app",
		"AgentHarnessEnvironment",
		`@app.post("/tools/example")`,
		`@app.post("/grade")`,
	} {
		if !strings.Contains(string(server), expected) {
			t.Fatalf("expected server to contain %q, got:\n%s", expected, server)
		}
	}
	dockerfile, err := os.ReadFile(filepath.Join(sessionDir, "Dockerfile"))
	if err != nil {
		t.Fatalf("expected Dockerfile to be created: %v", err)
	}
	for _, expected := range []string{
		"pip install --no-cache-dir openenv",
		"PIP_FALLBACK_INDEX_URL",
		"ENV ENABLE_WEB_INTERFACE=true",
		`"server.env:app"`,
	} {
		if !strings.Contains(string(dockerfile), expected) {
			t.Fatalf("expected Dockerfile to contain %q, got:\n%s", expected, dockerfile)
		}
	}
}

func TestCreateRleAgentScaffoldNormalizesBYOHBaseURL(t *testing.T) {
	sessionDir, err := CreateRleAgentScaffold(
		AgentScaffoldOptions{
			Kind:            AgentScaffoldKindBYOH,
			EnvironmentName: "customer_agent",
			BaseURL:         "https://agent.example.com/rle/",
		},
		t.TempDir(),
		false,
	)
	if err != nil {
		t.Fatal(err)
	}

	config, err := os.ReadFile(filepath.Join(sessionDir, "rle.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(config), `base_url = "https://agent.example.com/rle"`) {
		t.Fatalf("expected normalized BYOH base URL, got:\n%s", config)
	}
}

func TestCreateRleAgentScaffoldRejectsUnsafeBYOHBaseURL(t *testing.T) {
	_, err := CreateRleAgentScaffold(
		AgentScaffoldOptions{
			Kind:            AgentScaffoldKindBYOH,
			EnvironmentName: "customer_agent",
			BaseURL:         "https://user@agent.example.com",
		},
		t.TempDir(),
		false,
	)
	localError, ok := err.(*azdext.LocalError)
	if !ok || localError.Code != "rle_agent_base_url_invalid" {
		t.Fatalf("expected invalid base URL error, got %v", err)
	}
}

func TestCreateRleAgentScaffoldRunsOpenEnvRuntime(t *testing.T) {
	if os.Getenv("AZD_TEST_RLE_DOCKER_E2E") != "1" {
		t.Skip("Skipping RLE Docker integration test. Set AZD_TEST_RLE_DOCKER_E2E=1 to enable.")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("Skipping RLE Docker integration test because docker is not on PATH.")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	sessionDir, err := CreateRleAgentScaffold(
		AgentScaffoldOptions{
			Kind:            AgentScaffoldKindHostedAgent,
			EnvironmentName: "docker_agent",
			AgentName:       "docker-agent",
			AgentVersion:    "v1",
		},
		t.TempDir(),
		false,
	)
	if err != nil {
		t.Fatal(err)
	}

	suffix := fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano())
	imageName := "azd-rle-agent-scaffold-" + suffix
	containerName := "azd-rle-agent-scaffold-" + suffix
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()
		_, _ = runDockerTestCommand(cleanupCtx, "rm", "--force", containerName)
		_, _ = runDockerTestCommand(cleanupCtx, "image", "rm", "--force", imageName)
	})

	if _, err := runDockerTestCommand(ctx, "build", "--tag", imageName, sessionDir); err != nil {
		t.Fatal(err)
	}
	if _, err := runDockerTestCommand(
		ctx,
		"run",
		"--detach",
		"--name",
		containerName,
		"--publish",
		"127.0.0.1::8000",
		imageName,
	); err != nil {
		t.Fatal(err)
	}
	hostPort, err := dockerTestPublishedPort(ctx, containerName)
	if err != nil {
		t.Fatal(err)
	}
	baseURL := "http://" + hostPort
	if err := waitForDockerTestHealth(ctx, baseURL+"/health"); err != nil {
		t.Fatal(err)
	}

	httpClient := &http.Client{Timeout: 15 * time.Second}
	webResponse, err := httpClient.Get(baseURL + "/web")
	if err != nil {
		t.Fatalf("request OpenEnv web UI: %v", err)
	}
	_ = webResponse.Body.Close()
	if webResponse.StatusCode < http.StatusOK || webResponse.StatusCode >= http.StatusBadRequest {
		t.Fatalf("expected OpenEnv web UI response, got HTTP %d", webResponse.StatusCode)
	}
	for _, request := range []struct {
		path string
		body string
	}{
		{path: "/reset", body: `{"agent_input":"hello"}`},
		{path: "/step", body: `{"action":{"message":"hello"}}`},
	} {
		httpRequest, err := http.NewRequestWithContext(
			ctx,
			http.MethodPost,
			baseURL+request.path,
			strings.NewReader(request.body),
		)
		if err != nil {
			t.Fatalf("create OpenEnv %s request: %v", request.path, err)
		}
		httpRequest.Header.Set("Content-Type", "application/json")
		response, err := httpClient.Do(httpRequest)
		if err != nil {
			t.Fatalf("call OpenEnv %s endpoint: %v", request.path, err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("expected OpenEnv %s to return HTTP 200, got %d", request.path, response.StatusCode)
		}
	}

	webSocketURL := url.URL{Scheme: "ws", Host: hostPort, Path: "/ws"}
	connection, _, err := websocket.DefaultDialer.DialContext(ctx, webSocketURL.String(), nil)
	if err != nil {
		t.Fatalf("connect to OpenEnv WebSocket: %v", err)
	}
	defer connection.Close()
	for _, request := range []map[string]any{
		{"type": "reset", "data": map[string]any{"agent_input": "hello"}},
		{"type": "step", "data": map[string]any{"message": "hello"}},
	} {
		if err := connection.WriteJSON(request); err != nil {
			t.Fatalf("send OpenEnv WebSocket request: %v", err)
		}
		var response struct {
			Type string         `json:"type"`
			Data map[string]any `json:"data"`
		}
		if err := connection.ReadJSON(&response); err != nil {
			t.Fatalf("read OpenEnv WebSocket response: %v", err)
		}
		if response.Type != "observation" || response.Data == nil {
			t.Fatalf("expected OpenEnv observation response, got %#v", response)
		}
	}
}

func runDockerTestCommand(ctx context.Context, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "docker", args...) //nolint:gosec // Fixed test command shapes and generated names.
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("docker %s: %w\n%s", strings.Join(args, " "), err, output)
	}
	return output, nil
}

func dockerTestPublishedPort(ctx context.Context, containerName string) (string, error) {
	output, err := runDockerTestCommand(ctx, "port", containerName, "8000/tcp")
	if err != nil {
		return "", err
	}
	addresses := strings.Fields(string(output))
	if len(addresses) != 1 {
		return "", fmt.Errorf("expected one published port for %s, got %q", containerName, output)
	}
	host, port, err := net.SplitHostPort(addresses[0])
	if err != nil {
		return "", fmt.Errorf("parse published Docker port %q: %w", addresses[0], err)
	}
	if host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port), nil
}

func waitForDockerTestHealth(ctx context.Context, endpoint string) error {
	client := &http.Client{Timeout: 5 * time.Second}
	var lastErr error
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
			lastErr = fmt.Errorf("health endpoint returned HTTP %d", response.StatusCode)
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for OpenEnv health endpoint: %w (last error: %v)", ctx.Err(), lastErr)
		case <-time.After(time.Second):
		}
	}
}
