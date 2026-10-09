// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"azure.ai.rle/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func TestNormalizeVersionBumpFlag(t *testing.T) {
	cases := []struct {
		name     string
		value    string
		expected string
	}{
		{name: "default major", value: "major", expected: "Major"},
		{name: "minor", value: "minor", expected: "Minor"},
		{name: "patch", value: "patch", expected: "Patch"},
		{name: "trimmed uppercase", value: "  MAJOR  ", expected: "Major"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeVersionBumpFlag(tc.value)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.expected {
				t.Fatalf("expected %q, got %q", tc.expected, got)
			}
		})
	}
}

func TestNormalizeVersionBumpFlagRejectsInvalidValue(t *testing.T) {
	_, err := normalizeVersionBumpFlag("gold")
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok {
		t.Fatalf("expected LocalError, got %T", err)
	}
	if localErr.Code != "rle_invalid_version_bump" {
		t.Fatalf("expected invalid version bump code, got %q", localErr.Code)
	}
}

func TestPublishRejectsInvalidVersionBumpBeforeResolvingState(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	command := newPublishCommand()
	command.SetArgs([]string{"--version-bump", "gold"})
	command.SetOut(&bytes.Buffer{})
	command.SetErr(&bytes.Buffer{})

	err := command.Execute()
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok {
		t.Fatalf("expected LocalError, got %T", err)
	}
	if localErr.Code != "rle_invalid_version_bump" {
		t.Fatalf("expected invalid version bump code, got %q", localErr.Code)
	}
}

func TestBuildEnvironmentCreateRequestIncludesVersionBump(t *testing.T) {
	request := buildEnvironmentCreateRequest("echo_env", "example.azurecr.io/echo_env:latest", "Patch")
	if request.VersionBump != "Patch" {
		t.Fatalf("expected version bump to be included, got %#v", request)
	}
}

func TestEnvironmentOutputUsesEnvironmentNameField(t *testing.T) {
	body, err := json.Marshal(environmentOutput{
		EnvironmentId:      "env-1",
		EnvironmentVersion: "1.0.0",
		EnvironmentName:    "echo_env",
	})
	if err != nil {
		t.Fatal(err)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["environmentName"] != "echo_env" {
		t.Fatalf("expected environmentName field, got %v", payload)
	}
	if _, exists := payload["name"]; exists {
		t.Fatalf("expected legacy name field to be omitted, got %v", payload)
	}
}

const testPublishProjectEndpoint = "https://account.services.ai.azure.com/api/projects/project"
const testLimeProjectEndpoint = "https://lime.services.ai.azure.com/api/projects/other"

func TestPublishLimeRoutingFlagsAndRequest(t *testing.T) {
	const base = `{"name":"echo","acrImagePath":"registry.azurecr.io/echo:latest","versionBump":"Major"`
	tests := []struct {
		name, mode, endpoint, expected string
		routingSet, endpointSet        bool
	}{
		{"omitted", "", "", base + `}`, false, false},
		{"legacy", "legacy", "", base + `}`, true, false},
		{"disabled", "disabled", "", base + `,"lime_configuration":{"enabled":false}}`, true, false},
		{"same project", "same-project", "",
			base + `,"lime_configuration":{"enabled":true,"project_mode":"same_project"}}`, true, false},
		{"custom", "custom", testLimeProjectEndpoint,
			base + `,"lime_configuration":{"enabled":true,"project_mode":"custom","project_endpoint":"` +
				testLimeProjectEndpoint + `"}}`, true, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			flags := &rlePublishFlags{
				limeRouting: tc.mode, limeProjectEndpoint: tc.endpoint,
				routingSet: tc.routingSet, endpointSet: tc.endpointSet,
			}
			lime, err := publishLimeConfiguration(flags, testPublishProjectEndpoint)
			if err != nil {
				t.Fatal(err)
			}
			request := buildEnvironmentCreateRequest("echo", "registry.azurecr.io/echo:latest", "Major")
			request.LimeConfiguration = lime
			payload, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			if string(payload) != tc.expected {
				t.Fatalf("expected %s, got %s", tc.expected, payload)
			}
			client := newRleClientWithCredential(testPublishProjectEndpoint, &testTokenCredential{})
			client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodPost ||
					req.URL.String() != testPublishProjectEndpoint+"/rl_environments?api-version=2025-11-15-preview" {
					t.Fatalf("unexpected method or URL: %s %s", req.Method, req.URL)
				}
				body, err := io.ReadAll(req.Body)
				if err != nil {
					t.Fatal(err)
				}
				if string(body) != tc.expected {
					t.Fatalf("expected exact HTTP JSON %s, got %s", tc.expected, body)
				}
				return &http.Response{
					StatusCode: http.StatusCreated,
					Body:       io.NopCloser(strings.NewReader(`{"id":"id","name":"echo","version":"1.0.0"}`)),
					Header:     make(http.Header),
				}, nil
			})
			if _, err := client.createV1Environment(t.Context(), request); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPublishLimeRoutingRejectsInvalidCombinationsBeforeBuild(t *testing.T) {
	tests := []struct {
		name, mode, endpoint string
	}{
		{"unknown mode", "enabled", ""},
		{"empty explicit mode", "", ""},
		{"missing endpoint", "custom", ""},
		{"endpoint without mode", "", testLimeProjectEndpoint},
		{"legacy endpoint", "legacy", testLimeProjectEndpoint},
		{"disabled endpoint", "disabled", testLimeProjectEndpoint},
		{"same project endpoint", "same-project", testLimeProjectEndpoint},
		{"empty endpoint on disabled", "disabled", ""},
		{"HTTP", "custom", "http://lime.services.ai.azure.com/api/projects/other"},
		{"userinfo", "custom", "https://user:secret@lime.services.ai.azure.com/api/projects/other"},
		{"query", "custom", testLimeProjectEndpoint + "?sig=secret"},
		{"empty query", "custom", testLimeProjectEndpoint + "?"},
		{"fragment", "custom", testLimeProjectEndpoint + "#secret"},
		{"custom port", "custom", "https://lime.services.ai.azure.com:443/api/projects/other"},
		{"trailing slash", "custom", testLimeProjectEndpoint + "/"},
		{"missing project", "custom", "https://lime.services.ai.azure.com/api/projects/"},
		{"extra path", "custom", testLimeProjectEndpoint + "/extra"},
		{"encoded path", "custom", "https://lime.services.ai.azure.com/api/projects/%2e%2e"},
		{"wrong cloud", "custom", "https://lime.services.ai.azure.us/api/projects/other"},
		{"untrusted origin", "custom", "https://lime.example.org/api/projects/other"},
		{"same as publish project", "custom", testPublishProjectEndpoint},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			t.Setenv(foundryProjectEndpointEnvVar, testPublishProjectEndpoint)
			t.Setenv("AZURE_CONTAINER_REGISTRY_ENDPOINT", "")
			command := newPublishCommand()
			args := []string{"--lime-routing", tc.mode}
			if tc.endpoint != "" || tc.name == "empty endpoint on disabled" {
				args = append(args, "--lime-project-endpoint", tc.endpoint)
			}
			if tc.name == "endpoint without mode" {
				args = []string{"--lime-project-endpoint", tc.endpoint}
			}
			command.SetArgs(args)
			output := &bytes.Buffer{}
			command.SetOut(output)
			command.SetErr(output)
			err := command.Execute()
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			if !ok || localErr.Code != "rle_invalid_lime_routing" {
				t.Fatalf("expected routing validation before registry/build, got %v", err)
			}
			if strings.Contains(output.String()+err.Error(), "secret") ||
				strings.Contains(output.String()+err.Error(), testLimeProjectEndpoint) {
				t.Fatal("routing validation exposed endpoint or credentials")
			}
			if _, err := os.Stat(rleStateFile); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid publish wrote state: %v", err)
			}
		})
	}
}

func TestPublishRoutingSuccessReportsRequestWithoutPersistingIt(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv(foundryProjectEndpointEnvVar, testPublishProjectEndpoint)
	t.Setenv("AZURE_CONTAINER_REGISTRY_ENDPOINT", "registry.azurecr.io")
	originalBuild, originalPush, originalClient := buildPublishImage, pushPublishImage, createRleClient
	t.Cleanup(func() {
		buildPublishImage, pushPublishImage, createRleClient = originalBuild, originalPush, originalClient
	})
	buildPublishImage = func(context.Context, io.Writer, io.Writer, string, project.BuildOptions) error { return nil }
	pushPublishImage = func(context.Context, io.Writer, io.Writer, string) error { return nil }
	createRleClient = func(endpoint string) (*rleClient, error) {
		client := newRleClientWithCredential(endpoint, &testTokenCredential{})
		client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(body, []byte(`"project_mode":"custom"`)) ||
				!bytes.Contains(body, []byte(`"project_endpoint":"`+testLimeProjectEndpoint+`"`)) {
				t.Fatalf("wrong request body: %s", body)
			}
			return &http.Response{
				StatusCode: http.StatusCreated,
				Body: io.NopCloser(strings.NewReader(
					`{"id":"env-1","name":"echo","version":"1.0.0"}`,
				)),
				Header: make(http.Header),
			}, nil
		})
		return client, nil
	}
	command := newPublishCommand()
	command.SetArgs([]string{"--lime-routing", "custom", "--lime-project-endpoint", testLimeProjectEndpoint})
	output := &bytes.Buffer{}
	command.SetOut(output)
	command.SetErr(output)
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Lime routing requested: custom") ||
		strings.Contains(output.String(), testLimeProjectEndpoint) {
		t.Fatalf("unsafe or missing routing status: %s", output)
	}
	state, err := os.ReadFile(rleStateFile)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(state, []byte("lime")) || bytes.Contains(state, []byte(testLimeProjectEndpoint)) {
		t.Fatalf("routing was persisted in state: %s", state)
	}
}

func TestPublishRoutingFailedRequestPreservesLegacyState(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv(foundryProjectEndpointEnvVar, testPublishProjectEndpoint)
	t.Setenv("AZURE_CONTAINER_REGISTRY_ENDPOINT", "registry.azurecr.io")
	legacy := []byte("{\"name\":\"echo\",\"projectEndpoint\":\"" + testPublishProjectEndpoint + "\"}\n")
	if err := os.WriteFile(rleStateFile, legacy, 0600); err != nil {
		t.Fatal(err)
	}
	originalBuild, originalPush, originalClient := buildPublishImage, pushPublishImage, createRleClient
	t.Cleanup(func() {
		buildPublishImage, pushPublishImage, createRleClient = originalBuild, originalPush, originalClient
	})
	buildCalls, pushCalls := 0, 0
	buildPublishImage = func(context.Context, io.Writer, io.Writer, string, project.BuildOptions) error {
		buildCalls++
		return nil
	}
	pushPublishImage = func(context.Context, io.Writer, io.Writer, string) error {
		pushCalls++
		return nil
	}
	createRleClient = func(endpoint string) (*rleClient, error) {
		client := newRleClientWithCredential(endpoint, &testTokenCredential{})
		client.httpClient.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusBadRequest,
				Body: io.NopCloser(strings.NewReader(
					`{"message":"` + testLimeProjectEndpoint + ` token=secret Authorization: Bearer secret"}`,
				)),
				Header: make(http.Header),
			}, nil
		})
		return client, nil
	}
	command := newPublishCommand()
	command.SetArgs([]string{"--lime-routing", "custom", "--lime-project-endpoint", testLimeProjectEndpoint})
	output := &bytes.Buffer{}
	command.SetOut(output)
	command.SetErr(output)
	err := command.Execute()
	if err == nil || buildCalls != 1 || pushCalls != 1 {
		t.Fatalf("expected failed publish after build/push, got err=%v build=%d push=%d", err, buildCalls, pushCalls)
	}
	if strings.Contains(output.String()+err.Error(), testLimeProjectEndpoint) ||
		strings.Contains(output.String()+err.Error(), "secret") {
		t.Fatal("failed publish exposed endpoint or service credentials")
	}
	state, err := os.ReadFile(rleStateFile)
	if err != nil || !bytes.Equal(state, legacy) {
		t.Fatalf("failed publish changed legacy state: %s (%v)", state, err)
	}
}
