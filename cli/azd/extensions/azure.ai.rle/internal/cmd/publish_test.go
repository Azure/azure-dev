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
	t.Chdir(t.TempDir())

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

func TestPublishTracesWireContract(t *testing.T) {
	const base = `{"name":"echo","acrImagePath":"registry.azurecr.io/echo:latest","versionBump":"Major"`
	tests := []struct {
		name     string
		enabled  bool
		changed  bool
		expected string
	}{
		{name: "omitted defaults disabled", expected: base + `}`},
		{name: "enabled", enabled: true, changed: true, expected: base + `,"traces":{"enabled":true}}`},
		{name: "explicitly disabled", changed: true, expected: base + `,"traces":{"enabled":false}}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			request := buildEnvironmentCreateRequest("echo", "registry.azurecr.io/echo:latest", "Major")
			request.Traces = publishTracesConfiguration(&rlePublishFlags{
				enableTraces:    tc.enabled,
				enableTracesSet: tc.changed,
			})
			payload, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			if string(payload) != tc.expected {
				t.Fatalf("expected %s, got %s", tc.expected, payload)
			}
			for _, removed := range []string{"lime_configuration", "project_mode", "project_endpoint"} {
				if bytes.Contains(payload, []byte(removed)) {
					t.Fatalf("request contains removed property %q: %s", removed, payload)
				}
			}
		})
	}
}

func TestPublishEnableTracesFlagBehavior(t *testing.T) {
	const endpoint = "https://account.services.ai.azure.com/api/projects/project"
	tests := []struct {
		name          string
		args          []string
		expectedTrace string
		expectedLine  string
	}{
		{name: "omitted", expectedTrace: ""},
		{
			name:          "enabled",
			args:          []string{"--enable-traces"},
			expectedTrace: `"traces":{"enabled":true}`,
			expectedLine:  "Traces enabled: true",
		},
		{
			name:          "explicitly disabled",
			args:          []string{"--enable-traces=false"},
			expectedTrace: `"traces":{"enabled":false}`,
			expectedLine:  "Traces enabled: false",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			t.Setenv(foundryProjectEndpointEnvVar, endpoint)
			t.Setenv("AZURE_CONTAINER_REGISTRY_ENDPOINT", "registry.azurecr.io")
			originalBuild, originalPush, originalClient := buildPublishImage, pushPublishImage, createRleClient
			t.Cleanup(func() {
				buildPublishImage, pushPublishImage, createRleClient = originalBuild, originalPush, originalClient
			})
			buildPublishImage = func(context.Context, io.Writer, io.Writer, string, project.BuildOptions) error {
				return nil
			}
			pushPublishImage = func(context.Context, io.Writer, io.Writer, string) error { return nil }
			createRleClient = func(projectEndpoint string) (*rleClient, error) {
				client := newRleClientWithCredential(projectEndpoint, &testTokenCredential{})
				client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
					body, err := io.ReadAll(req.Body)
					if err != nil {
						t.Fatal(err)
					}
					if tc.expectedTrace == "" {
						if bytes.Contains(body, []byte(`"traces"`)) {
							t.Fatalf("omitted flag serialized traces: %s", body)
						}
					} else if !bytes.Contains(body, []byte(tc.expectedTrace)) {
						t.Fatalf("expected %s in request: %s", tc.expectedTrace, body)
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
			command.SetArgs(tc.args)
			output := &bytes.Buffer{}
			command.SetOut(output)
			command.SetErr(output)
			if err := command.Execute(); err != nil {
				t.Fatal(err)
			}
			if tc.expectedLine == "" {
				if strings.Contains(output.String(), "Traces enabled:") {
					t.Fatalf("omitted flag reported an explicit trace value: %s", output)
				}
			} else if !strings.Contains(output.String(), tc.expectedLine) {
				t.Fatalf("expected %q in output: %s", tc.expectedLine, output)
			}
			state, err := os.ReadFile(rleStateFile)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(state, []byte("traces")) {
				t.Fatalf("trace preference was persisted: %s", state)
			}
		})
	}
}
