// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func TestShowTelemetryIdentity(t *testing.T) {
	tests := []struct {
		name      string
		telemetry string
		scope     string
		runID     string
		format    string
	}{
		{"readable environment", `{"run_scope":"Environment","lime_run_id":"rle-v1|p=one|e=two|v=1.0","run_id_format":"readable"}`,
			"Environment", "rle-v1|p=one|e=two|v=1.0", "readable"},
		{"opaque environment", `{"run_scope":"Environment",` +
			`"lime_run_id":"rle-v1-sha256|h=abcdef","run_id_format":"opaque"}`,
			"Environment", "rle-v1-sha256|h=abcdef", "opaque"},
		{"rollout", `{"run_scope":"Rollout"}`, "Rollout", "Per rollout", "Unavailable"},
		{"disabled", `{"run_scope":"Disabled"}`, "Disabled", "Disabled", "Unavailable"},
		{"invalid version", `null`, "Unavailable", "Unavailable", "Unavailable"},
		{"older service", ``, "Unavailable", "Unavailable", "Unavailable"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(foundryProjectEndpointEnvVar, "https://account.services.ai.azure.com/api/projects/project")
			descriptor := ""
			if test.telemetry != "" {
				descriptor = `,"telemetry":` + test.telemetry
			}
			resource := `{"id":"version-id","name":"echo_env","version":"1.0.0"` + descriptor + `}`
			for _, exact := range []bool{false, true} {
				for _, format := range []string{"default", "json"} {
					t.Run(fmt.Sprintf("exact=%t/%s", exact, format), func(t *testing.T) {
						count := 0
						server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							count++
							expectedPath := testFoundryProjectPath + environmentCollectionPath + "/echo_env/versions"
							if exact {
								expectedPath += "/1.0.0"
							}
							if r.Method != http.MethodGet || r.URL.Path != expectedPath {
								t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
								http.Error(w, "unexpected path", http.StatusBadRequest)
								return
							}
							if r.URL.Query().Get("api-version") != foundryAPIVersion {
								t.Errorf("missing API version: %s", r.URL.RawQuery)
							}
							if exact {
								if r.URL.Query().Has("limit") || r.URL.Query().Has("continuationToken") {
									t.Errorf("exact GET must not paginate: %s", r.URL.RawQuery)
								}
								_, _ = w.Write([]byte(resource))
							} else {
								if r.URL.Query().Get("limit") != fmt.Sprint(environmentListPageSize) {
									t.Errorf("missing history limit: %s", r.URL.RawQuery)
								}
								_, _ = w.Write([]byte(`{"data":[` + resource + `]}`))
							}
						}))
						defer server.Close()
						stubRleClientEndpoint(t, server.URL)

						cmd := newShowCommand(&format)
						args := []string{"echo_env"}
						if exact {
							args = append(args, "--version", "1.0.0")
						}
						cmd.SetArgs(args)
						var output bytes.Buffer
						cmd.SetOut(&output)
						cmd.SetErr(&output)
						if err := cmd.Execute(); err != nil {
							t.Fatal(err)
						}
						if count != 1 {
							t.Fatalf("expected one request, got %d", count)
						}
						if format == "default" {
							for _, expected := range []string{test.scope, test.runID} {
								if !strings.Contains(output.String(), expected) {
									t.Errorf("expected %q in output %q", expected, output.String())
								}
							}
							if exact {
								for _, expected := range []string{"Run scope", "Lime run ID", "Run ID format", test.format} {
									if !strings.Contains(output.String(), expected) {
										t.Errorf("expected %q in detail output %q", expected, output.String())
									}
								}
							} else if !strings.Contains(output.String(), "RUN SCOPE") ||
								!strings.Contains(output.String(), "LIME RUN ID") {
								t.Errorf("expected history columns, got %q", output.String())
							}
							return
						}
						var parsed any
						if err := json.Unmarshal(output.Bytes(), &parsed); err != nil {
							t.Fatalf("invalid JSON: %s: %v", output.String(), err)
						}
						if exact {
							if _, ok := parsed.(map[string]any); !ok {
								t.Fatalf("expected detail object: %s", output.String())
							}
						} else if versions, ok := parsed.([]any); !ok || len(versions) != 1 {
							t.Fatalf("expected history array: %s", output.String())
						}
						if test.telemetry == "" || test.telemetry == "null" {
							if strings.Contains(output.String(), `"telemetry"`) ||
								strings.Contains(output.String(), `"lime_run_id"`) {
								t.Errorf("invented telemetry field: %s", output.String())
							}
						} else {
							var expected map[string]any
							if err := json.Unmarshal([]byte(test.telemetry), &expected); err != nil {
								t.Fatal(err)
							}
							for key, value := range expected {
								pair, err := json.Marshal(key)
								if err != nil {
									t.Fatal(err)
								}
								encoded, err := json.Marshal(value)
								if err != nil {
									t.Fatal(err)
								}
								if !strings.Contains(output.String(), string(pair)+": "+string(encoded)) &&
									!strings.Contains(output.String(), string(pair)+":"+string(encoded)) {
									t.Errorf("missing full %s=%v: %s", key, value, output.String())
								}
							}
							if test.scope == "Rollout" && strings.Contains(output.String(), `"lime_run_id"`) {
								t.Errorf("invented rollout ID: %s", output.String())
							}
						}
					})
				}
			}
		})
	}
}

func TestShowExactVersionEscapesPathAndPropagatesErrors(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		wantCode   string
		wantStatus int
	}{
		{"success", http.StatusOK, "", 0},
		{"missing", http.StatusNotFound, "rle_environment_version_not_found", 0},
		{"unauthorized", http.StatusUnauthorized, "", http.StatusUnauthorized},
		{"service failure", http.StatusInternalServerError, "", http.StatusInternalServerError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(foundryProjectEndpointEnvVar, "https://account.services.ai.azure.com/api/projects/project")
			count := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count++
				want := testFoundryProjectPath + environmentCollectionPath + "/a%2Fb/versions/1%2F2%3F%23"
				if r.Method != http.MethodGet || r.URL.EscapedPath() != want {
					t.Errorf("unexpected exact-version route %s %s, want %s", r.Method, r.URL.EscapedPath(), want)
				}
				if r.URL.Query().Get("api-version") != foundryAPIVersion {
					t.Errorf("missing API version: %s", r.URL.RawQuery)
				}
				w.WriteHeader(test.status)
				if test.status == http.StatusOK {
					_, _ = w.Write([]byte(`{"id":"id","version":"1/2?#"}`))
				} else {
					_, _ = w.Write([]byte(`{"code":"Failure","message":"failed"}`))
				}
			}))
			defer server.Close()
			stubRleClientEndpoint(t, server.URL)
			format := "json"
			cmd := newShowCommand(&format)
			cmd.SetArgs([]string{"a/b", "--version", "1/2?#"})
			var output bytes.Buffer
			cmd.SetOut(&output)
			cmd.SetErr(&output)
			err := cmd.Execute()
			if count != 1 {
				t.Fatalf("expected one exact GET, got %d", count)
			}
			if test.wantCode != "" {
				localErr, ok := errors.AsType[*azdext.LocalError](err)
				if !ok || localErr.Code != test.wantCode {
					t.Fatalf("expected %s local error, got %v", test.wantCode, err)
				}
			} else if test.wantStatus != 0 {
				serviceErr, ok := errors.AsType[*azdext.ServiceError](err)
				if !ok || serviceErr.StatusCode != test.wantStatus {
					t.Fatalf("expected HTTP %d service error, got %v", test.wantStatus, err)
				}
			} else if err != nil || !strings.Contains(output.String(), `"version": "1/2?#"`) {
				t.Fatalf("unexpected success: %v, %s", err, output.String())
			}
		})
	}
}

func TestShowMixedHistoryUsesOnlyPagedGETAndOmitsUnsafeFields(t *testing.T) {
	t.Setenv(foundryProjectEndpointEnvVar, "https://account.services.ai.azure.com/api/projects/project")
	longID := "rle-v1-sha256|h=" + strings.Repeat("a", 96)
	count := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		if r.Method != http.MethodGet ||
			r.URL.Path != testFoundryProjectPath+environmentCollectionPath+"/echo_env/versions" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"data":[
			{"id":"one","version":"1.0","telemetry":{"run_scope":"Environment",
				"lime_run_id":"` + longID + `","run_id_format":"opaque",
				"project_endpoint":"https://secret.example","authorization":"secret-token"}},
			{"id":"two","version":"2.0","telemetry":{"run_scope":"Rollout"}},
			{"id":"three","version":"3.0"}]}`))
	}))
	defer server.Close()
	stubRleClientEndpoint(t, server.URL)

	for _, format := range []string{"default", "json"} {
		t.Run(format, func(t *testing.T) {
			cmd := newShowCommand(&format)
			cmd.SetArgs([]string{"echo_env"})
			var output bytes.Buffer
			cmd.SetOut(&output)
			cmd.SetErr(&output)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output.String(), longID) {
				t.Fatalf("expected complete server ID in %s output: %s", format, output.String())
			}
			for _, secret := range []string{"secret.example", "secret-token", "project_endpoint", "authorization"} {
				if strings.Contains(output.String(), secret) {
					t.Errorf("unexpected unsafe field %q in output: %s", secret, output.String())
				}
			}
			if format == "default" && (!strings.Contains(output.String(), "Per rollout") ||
				!strings.Contains(output.String(), "Unavailable")) {
				t.Errorf("expected mixed history labels: %s", output.String())
			}
		})
	}
	if count != 2 {
		t.Fatalf("expected one history GET per invocation, got %d for two invocations", count)
	}
}

func TestShowRejectsEmptyExplicitVersion(t *testing.T) {
	format := "default"
	cmd := newShowCommand(&format)
	cmd.SetArgs([]string{"env", "--version", " "})
	err := cmd.Execute()
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok || localErr.Code != "rle_environment_version_required" {
		t.Fatalf("expected version validation error, got %v", err)
	}
	if localErr.Suggestion != "Provide a semantic version, for example --version 2.1.0." {
		t.Fatalf("expected actionable version suggestion, got %q", localErr.Suggestion)
	}
}

func TestGetEnvironmentVersionPathEscapesSegments(t *testing.T) {
	for _, test := range []struct{ name, version string }{
		{"space", "1 2"},
		{"reserved", "v/?#"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				want := testFoundryProjectPath + environmentCollectionPath +
					"/env%2Fname/versions/" + url.PathEscape(test.version)
				if r.URL.EscapedPath() != want {
					t.Errorf("path %q, want %q", r.URL.EscapedPath(), want)
				}
				_, _ = w.Write([]byte(`{"id":"id"}`))
			}))
			defer server.Close()
			client := testRleClientForServer(t, server.URL)
			if _, err := client.getEnvironmentVersion(t.Context(), "env/name", test.version); err != nil {
				t.Fatal(err)
			}
		})
	}
}
