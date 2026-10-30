// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"azure.ai.rle/internal/project"
)

type manifestImageClient struct {
	project.TaskArtifactClient
	mu    sync.Mutex
	calls int
	err   error
}

func (c *manifestImageClient) Digest(context.Context, string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	return "sha256:" + strings.Repeat("a", 64), c.err
}

func TestPublishArtifactsParallelSignedUploadsBeforeRegistration(t *testing.T) {
	for _, failure := range []string{"success", "upload failure", "finalize failure"} {
		t.Run(failure, func(t *testing.T) {
			filename, raw, imageClient := publishArtifactFixture(t)
			names := []string{"first", "second", "third", "fourth"}
			manifest := project.TaskArtifactManifest{SchemaVersion: 1}
			for _, name := range names {
				manifest.Tasks = append(manifest.Tasks, project.TaskArtifact{
					TaskName: name, LocalBundlePath: "task.tar.gz", AcrImagePath: "example.azurecr.io/custom/image:v1",
				})
			}
			encoded, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filename, encoded, 0o600); err != nil {
				t.Fatal(err)
			}
			config := project.RleConfig{
				SchemaVersion: new(project.CurrentRleManifestSchemaVersion),
				Rle:           project.RleManifest{Name: "harbor_example", Version: "1.0.0"},
				Harness:       project.RleHarnessManifest{Type: project.HarnessTypePackage, Name: "opencode"},
				Task:          &project.RleTaskManifest{Format: project.TaskFormatHarbor},
			}
			if err := project.WriteRleConfig(".", config); err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			active, peak, started, finalized, registrations := 0, 0, 0, 0, 0
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet:
					http.NotFound(w, r)
				case strings.HasSuffix(r.URL.Path, "/tasks/upload-url"):
					_ = json.NewEncoder(w).Encode(harborUploadURLResponse{
						Path: "task-data/full.tar.gz", UploadURL: "https://storage.test/signed/blob", StagingPath: "stage",
					})
				case r.Method == http.MethodPut && r.URL.Path == "/signed/blob":
					body, err := io.ReadAll(r.Body)
					if err != nil || !bytes.Equal(body, raw) {
						t.Errorf("signed upload must preserve original bytes: %v", err)
					}
					mu.Lock()
					active++
					started++
					peak = max(peak, active)
					if started == 2 {
						close(release)
					}
					mu.Unlock()
					select {
					case <-release:
					case <-r.Context().Done():
					}
					mu.Lock()
					active--
					mu.Unlock()
					if failure == "upload failure" {
						http.Error(w, "upload failed", http.StatusInternalServerError)
					} else {
						w.WriteHeader(http.StatusCreated)
					}
				case strings.HasSuffix(r.URL.Path, "/tasks/finalize"):
					mu.Lock()
					finalized++
					mu.Unlock()
					if failure == "finalize failure" {
						http.Error(w, "finalization failed", http.StatusInternalServerError)
						return
					}
					_, _ = w.Write([]byte("{}"))
				case r.Method == http.MethodPost && r.URL.Path == testFoundryProjectPath+environmentCollectionPath:
					mu.Lock()
					registrations++
					done := finalized
					mu.Unlock()
					if done != len(names) {
						t.Errorf("registration must wait for all finalizations, got %d", done)
					}
					var request v1EnvironmentRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
					}
					if len(request.Tasks) != len(names) {
						t.Errorf("unexpected registered task count: %d", len(request.Tasks))
					} else {
						for index, task := range request.Tasks {
							if task.Name != names[index] || task.Bundle.Sha256 != harborBundleHash(raw) {
								t.Errorf("parallel uploads changed task order or bundle identity: %+v", request.Tasks)
							}
						}
					}
					_ = json.NewEncoder(w).Encode(environmentResource{
						Name: config.Rle.Name, Version: config.Rle.Version, SchemaVersion: config.SchemaVersion,
						Harness: rleHarness{Type: "package", Name: "opencode"},
					})
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					http.Error(w, "unexpected", http.StatusBadRequest)
				}
			}))
			defer server.Close()
			stubRleClientEndpoint(t, server.URL)
			command := newPublishCommand()
			command.SetArgs([]string{"--task-manifest", filename, "--concurrency", "2"})
			command.SetOut(&bytes.Buffer{})
			command.SetErr(&bytes.Buffer{})
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			err = command.ExecuteContext(ctx)
			mu.Lock()
			defer mu.Unlock()
			if peak != 2 || imageClient.calls != len(names) {
				t.Fatalf("expected two concurrent uploads and image-only verification: peak=%d calls=%d err=%v",
					peak, imageClient.calls, err)
			}
			if failure != "success" {
				if err == nil || registrations != 0 {
					t.Fatalf("upload failure must prevent registration: registrations=%d err=%v", registrations, err)
				}
			} else if err != nil || finalized != len(names) || registrations != 1 {
				t.Fatalf("expected all bundles finalized before one registration: finalized=%d registrations=%d err=%v",
					finalized, registrations, err)
			}
		})
	}
}

func publishArtifactFixture(t *testing.T) (string, []byte, *manifestImageClient) {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("AZURE_CONTAINER_REGISTRY_ENDPOINT", "")
	t.Setenv("AZD_CONTAINER_RUNTIME", filepath.Join(dir, "missing-runtime"))
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	for name, contents := range map[string]string{
		"task.toml": "", "instruction.md": "instruction", "environment/Dockerfile": "FROM scratch\n",
		"tests/test.sh": "#!/bin/sh\n", "solution/solve.sh": "keep original archive intact",
	} {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(contents))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(contents)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("task.tar.gz", archive.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(dir, "artifacts.json")
	if err := os.WriteFile(filename, []byte(`{"schema_version":1,"tasks":[{
		"task_name":"my-task","local_bundle_path":"task.tar.gz",
		"acr_image_path":"example.azurecr.io/custom/image:v1"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	client := &manifestImageClient{}
	oldFactory := newTaskArtifactClient
	t.Cleanup(func() { newTaskArtifactClient = oldFactory })
	newTaskArtifactClient = func() project.TaskArtifactClient { return client }
	return filename, archive.Bytes(), client
}

func TestPublishArtifactsPrepareOnlyNeedsNoRleConfigOrEndpoint(t *testing.T) {
	filename, _, client := publishArtifactFixture(t)
	t.Setenv(foundryProjectEndpointEnvVar, "")
	command := newPublishCommand()
	command.SetArgs([]string{"--prepare-only", "--task-manifest", filename})
	var stdout, stderr bytes.Buffer
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if client.calls != 1 || !strings.Contains(stdout.String(), "Prepared 1 Harbor task(s)") ||
		strings.Contains(stderr.String(), "paid cloud") {
		t.Fatalf("prebuilt preparation must only verify the image: calls=%d stdout=%s stderr=%s",
			client.calls, stdout.String(), stderr.String())
	}
	if _, err := os.Stat("rle.toml"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("prepare-only must not create or require rle.toml")
	}
}

func TestPublishArtifactsRejectInvalidRecoveryFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--retry-failed", "--task-manifest", "tasks.json"},
		{"--prepare-only", "--retry-failed"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			command := newPublishCommand()
			command.SetArgs(args)
			command.SetOut(&bytes.Buffer{})
			command.SetErr(&bytes.Buffer{})
			if err := command.ExecuteContext(t.Context()); err == nil {
				t.Fatal("invalid recovery flags must fail before resolving cloud configuration")
			}
		})
	}
}

func TestPublishArtifactsUsesFullBundleAndPinnedImage(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "publish", true: "image validation failure"}[fail], func(t *testing.T) {
			filename, raw, imageClient := publishArtifactFixture(t)
			if fail {
				imageClient.err = errors.New("image does not exist")
			}
			config := project.RleConfig{
				SchemaVersion: new(project.CurrentRleManifestSchemaVersion),
				Rle:           project.RleManifest{Name: "harbor_example", Version: "1.0.0"},
				Harness:       project.RleHarnessManifest{Type: project.HarnessTypePackage, Name: "opencode"},
				Task:          &project.RleTaskManifest{Format: project.TaskFormatHarbor},
			}
			if err := project.WriteRleConfig(".", config); err != nil {
				t.Fatal(err)
			}
			published := environmentResource{
				Name: config.Rle.Name, Version: config.Rle.Version, Harness: rleHarness{Type: "package", Name: "opencode"},
				SchemaVersion: config.SchemaVersion,
			}
			uploads, registrations := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet:
					http.NotFound(w, r)
				case strings.HasSuffix(r.URL.Path, "/tasks/upload-url"):
					uploads++
					var input map[string]string
					if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
						t.Error(err)
					}
					if input["sha256"] != harborBundleHash(raw) {
						t.Error("upload must use the original full archive hash")
					}
					_, _ = w.Write([]byte(`{"path":"task-data/full.tar.gz","exists":true}`))
				case r.Method == http.MethodPost && r.URL.Path == testFoundryProjectPath+environmentCollectionPath:
					registrations++
					var request v1EnvironmentRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
					}
					if request.AcrImagePath != "" || len(request.Tasks) != 1 ||
						request.Tasks[0].Name != "my-task" ||
						request.Tasks[0].AcrImagePath != "example.azurecr.io/custom/image@sha256:"+strings.Repeat("a", 64) ||
						request.Tasks[0].Bundle.Sha256 != harborBundleHash(raw) {
						t.Errorf("unexpected registration: %+v", request)
					}
					if err := json.NewEncoder(w).Encode(published); err != nil {
						t.Error(err)
					}
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					http.Error(w, "unexpected", http.StatusBadRequest)
				}
			}))
			defer server.Close()
			stubRleClientEndpoint(t, server.URL)
			command := newPublishCommand()
			command.SetArgs([]string{"--task-manifest", filename})
			command.SetOut(&bytes.Buffer{})
			command.SetErr(&bytes.Buffer{})
			err := command.ExecuteContext(t.Context())
			if fail {
				if err == nil || uploads != 0 || registrations != 0 {
					t.Fatalf("image verification failure must prevent upload and registration: %v", err)
				}
			} else if err != nil || uploads != 1 || registrations != 1 {
				t.Fatalf("expected one upload check and final registration: uploads=%d registrations=%d err=%v",
					uploads, registrations, err)
			}
		})
	}
}
