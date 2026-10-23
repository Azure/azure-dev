// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"azure.ai.rle/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func TestMain(m *testing.M) {
	if capture := os.Getenv("RLE_TEST_PUBLISH_AZ_CAPTURE"); capture != "" {
		if len(os.Args) < 3 || os.Args[1] != "acr" || os.Args[2] != "build" {
			os.Exit(1)
		}
		cwd, err := os.Getwd()
		if err != nil {
			os.Exit(1)
		}
		data, err := json.Marshal(struct {
			Args []string
			Cwd  string
		}{os.Args[1:], cwd})
		//nolint:gosec // Test-owned capture path supplied to the fake Azure CLI subprocess.
		if err != nil || os.WriteFile(capture, data, 0600) != nil {
			os.Exit(1)
		}
		if os.Getenv("RLE_TEST_PUBLISH_AZ_FAIL") == "1" {
			os.Exit(1)
		}
		os.Exit(0)
	}
	// The publish regression test uses this binary instead of a real container runtime.
	if os.Getenv("RLE_TEST_PUBLISH_CONTAINER_RUNTIME") == "1" {
		if len(os.Args) > 1 && (os.Args[1] == "build" || os.Args[1] == "push") {
			if capture := os.Getenv("RLE_TEST_PUBLISH_CONTAINER_CAPTURE"); capture != "" {
				//nolint:gosec // Test-owned capture path supplied to the fake container runtime.
				f, err := os.OpenFile(capture, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
				if err != nil {
					os.Exit(1)
				}
				encodeErr := json.NewEncoder(f).Encode(os.Args[1:])
				closeErr := f.Close()
				if encodeErr != nil || closeErr != nil {
					os.Exit(1)
				}
			}
			if os.Getenv("RLE_TEST_PUBLISH_CONTAINER_FAIL") == os.Args[1] {
				os.Exit(1)
			}
			os.Exit(0)
		}
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func TestPublishDisplaysReleaseIdentityAndPreservesJSON(t *testing.T) {
	for _, tc := range []struct {
		name       string
		remote     bool
		fail       bool
		localFail  string
		dockerfile string
		protocol   project.HarnessProtocol
	}{
		{name: "local"},
		{name: "local server Dockerfile", dockerfile: "server/Dockerfile"},
		{name: "local BYOH responses", protocol: project.HarnessProtocolResponses},
		{name: "local BYOH invocation", protocol: project.HarnessProtocolRleInvocation},
		{name: "local build failure", localFail: "build"},
		{name: "local push failure", localFail: "push"},
		{name: "remote", remote: true},
		{name: "remote build failure", remote: true, fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			t.Setenv("AZURE_CONTAINER_REGISTRY_ENDPOINT", "example.azurecr.io")
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("AZD_CONTAINER_RUNTIME", executable)
			t.Setenv("RLE_TEST_PUBLISH_CONTAINER_RUNTIME", "1")
			localCapture := filepath.Join(dir, "container-calls.jsonl")
			t.Setenv("RLE_TEST_PUBLISH_CONTAINER_CAPTURE", localCapture)
			t.Setenv("RLE_TEST_PUBLISH_CONTAINER_FAIL", tc.localFail)
			capture := ""
			if tc.remote {
				capture = usePublishAzureCLI(t)
				t.Setenv("AZD_CONTAINER_RUNTIME", filepath.Join(dir, "missing-local-runtime"))
				if tc.fail {
					t.Setenv("RLE_TEST_PUBLISH_AZ_FAIL", "1")
				}
			}
			config := project.RleConfig{
				SchemaVersion: ptrTo(project.CurrentRleManifestSchemaVersion),
				Rle: project.RleManifest{
					Name: "math_rl", Version: "1.0.0",
				},
				Harness: project.RleHarnessManifest{
					Type: project.HarnessTypeGym,
				},
				Model: &project.RleModelDefaults{Name: new("test-model")},
			}
			if tc.protocol != "" {
				config.Harness = project.RleHarnessManifest{
					Type: project.HarnessTypeBYOH, Protocol: tc.protocol,
					Endpoint: new("https://agent.example.test/invoke"),
				}
			}
			if err := project.WriteRleConfig(dir, config); err != nil {
				t.Fatal(err)
			}
			dockerfile := tc.dockerfile
			if dockerfile == "" {
				dockerfile = "Dockerfile"
			}
			if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, dockerfile)), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, dockerfile), []byte("FROM scratch\n"), 0600); err != nil {
				t.Fatal(err)
			}
			published := environmentResource{
				Id: "opaque-environment-id", Name: config.Rle.Name, Version: config.Rle.Version,
				Harness: rleHarness{
					Type: string(config.Harness.Type), Protocol: string(config.Harness.Protocol), Endpoint: config.Harness.Endpoint,
				},
				AcrImagePath: "example.azurecr.io/project-1-math-rl:1.0.0",
				Model:        config.Model, CreatedAt: "2026-07-30T04:00:00Z", UpdatedAt: "2026-07-30T05:00:00Z",
				SchemaVersion: config.SchemaVersion,
			}
			requestCount := 0
			controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestCount++
				switch {
				case r.Method == http.MethodGet && r.URL.Path == testFoundryProjectPath+environmentCollectionPath+"/math_rl":
					http.NotFound(w, r)
				case r.Method == http.MethodPost && r.URL.Path == testFoundryProjectPath+environmentCollectionPath:
					var request v1EnvironmentRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
						return
					}
					if request.Name != published.Name || request.Version != published.Version ||
						!reflect.DeepEqual(request.Harness, published.Harness) || len(request.Tasks) != 0 ||
						request.AcrImagePath != published.AcrImagePath {
						t.Errorf("unexpected publish request: %#v", request)
					}
					w.Header().Set("Content-Type", "application/json")
					if err := json.NewEncoder(w).Encode(published); err != nil {
						t.Error(err)
					}
				default:
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
			}))
			defer controlPlane.Close()
			stubRleClientEndpoint(t, controlPlane.URL)

			command := newPublishCommand()
			if tc.remote {
				command.SetArgs([]string{"--remote"})
			}
			var stdout, stderr bytes.Buffer
			command.SetOut(&stdout)
			command.SetErr(&stderr)
			publishErr := command.Execute()
			if tc.remote {
				args, cwd := readPublishAzureCLI(t, capture)
				want := []string{"acr", "build", "--registry", "example", "--image", "project-1-math-rl:1.0.0",
					"--file", "Dockerfile", "--platform", "linux/amd64", "."}
				if !slices.Equal(args, want) || cwd != dir {
					t.Fatalf("unexpected remote build: args=%v cwd=%q", args, cwd)
				}
				if _, err := os.Stat(localCapture); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("remote publish must not invoke the local runtime: %v", err)
				}
			} else {
				//nolint:gosec // Fake container runtime writes this test-owned capture file.
				f, err := os.Open(localCapture)
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				decoder := json.NewDecoder(f)
				var build []string
				if err := decoder.Decode(&build); err != nil {
					t.Fatal(err)
				}
				want := []string{"build", "-t", published.AcrImagePath, "-f", filepath.Join(dir, dockerfile), dir}
				if !slices.Equal(build, want) {
					t.Fatalf("local build changed: got %v, want %v", build, want)
				}
				if tc.localFail != "build" {
					var push []string
					if err := decoder.Decode(&push); err != nil {
						t.Fatal(err)
					}
					if !slices.Equal(push, []string{"push", published.AcrImagePath}) {
						t.Fatalf("local push changed: %v", push)
					}
				}
				var extra []string
				if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
					t.Fatalf("unexpected extra local command: %v, %v", extra, err)
				}
			}
			if tc.fail || tc.localFail != "" {
				if publishErr == nil || requestCount != 1 || strings.Contains(stdout.String(), "Published environment") {
					t.Fatalf("failed build must not register a release: err=%v requests=%d stdout=%s",
						publishErr, requestCount, stdout.String())
				}
				return
			}
			if publishErr != nil {
				t.Fatalf("publish failed: %v; stderr: %s", publishErr, stderr.String())
			}
			if requestCount != 2 {
				t.Fatalf("expected preflight and publish requests, got %d", requestCount)
			}
			const success = "\nPublished environment 'math_rl' version 1.0.0\n"
			_, details, found := strings.Cut(stdout.String(), success)
			if !found {
				t.Fatalf("expected exact success line %q, got %s", success, stdout.String())
			}
			var payload map[string]any
			if err := json.Unmarshal([]byte(details), &payload); err != nil {
				t.Fatalf("expected JSON after success line, got %s: %v", details, err)
			}
			for field, want := range map[string]string{
				"environmentId": published.Id, "environmentName": published.Name, "environmentVersion": published.Version,
				"type": published.Harness.Type, "acrImage": published.AcrImagePath,
				"foundryProjectEndpoint": "https://account.services.ai.azure.com/api/projects/project-1",
				"createdAt":              published.CreatedAt, "updatedAt": published.UpdatedAt,
				"schema_version": *published.SchemaVersion,
			} {
				if payload[field] != want {
					t.Errorf("%s = %v, want %q", field, payload[field], want)
				}
			}
			if model, ok := payload["model"].(map[string]any); !ok || model["name"] == nil {
				t.Fatalf("expected model defaults to remain in JSON, got %#v", payload["model"])
			}
			t.Logf("Captured publish output:\n%s", stdout.String())
		})
	}
}

func usePublishAzureCLI(t *testing.T) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	name := "az"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	// Hard links share Windows' lock on the running test binary and can prevent cleanup.
	//nolint:gosec // Source is the running test binary returned by os.Executable.
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // Test-owned executable shim requires owner execute permission on Unix.
	if err := os.WriteFile(filepath.Join(bin, name), data, 0700); err != nil {
		t.Fatal(err)
	}
	capture := filepath.Join(t.TempDir(), "azure-cli.json")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("RLE_TEST_PUBLISH_AZ_CAPTURE", capture)
	return capture
}

func TestPublishAzureCLIUsesIndependentExecutable(t *testing.T) {
	usePublishAzureCLI(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	shim, err := exec.LookPath("az")
	if err != nil {
		t.Fatal(err)
	}
	originalInfo, err := os.Stat(executable)
	if err != nil {
		t.Fatal(err)
	}
	shimInfo, err := os.Stat(shim)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(originalInfo, shimInfo) {
		t.Fatal("Azure CLI shim must not share the running test executable's Windows file lock")
	}
}

func readPublishAzureCLI(t *testing.T, capture string) ([]string, string) {
	t.Helper()
	data, err := os.ReadFile(capture) //nolint:gosec // Test-owned capture path created under t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	var invocation struct {
		Args []string
		Cwd  string
	}
	if err := json.Unmarshal(data, &invocation); err != nil {
		t.Fatal(err)
	}
	return invocation.Args, invocation.Cwd
}

func TestPrepareHarborTasksRemote(t *testing.T) {
	capture := usePublishAzureCLI(t)
	t.Setenv("AZURE_CONTAINER_REGISTRY_ENDPOINT", "example.azurecr.io")
	t.Setenv("AZD_CONTAINER_RUNTIME", filepath.Join(t.TempDir(), "missing-local-runtime"))
	task := filepath.Join(t.TempDir(), "hello")
	for name, contents := range map[string]string{
		"task.toml": "", "instruction.md": "Say hello", "environment/Dockerfile": "FROM scratch\n", "tests/test.sh": "true\n",
	} {
		path := filepath.Join(task, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	bundleRequests := 0
	client := newHarborTestClient(func(r *http.Request) (*http.Response, error) {
		bundleRequests++
		return harborTestResponse(http.StatusOK, `{"path":"task-data/hello.tar.gz","exists":true}`), nil
	})
	action := &publishAction{
		cmd: newPublishCommand(), flags: &rlePublishFlags{taskDirs: []string{task}, remote: true},
	}
	action.cmd.SetContext(t.Context())
	action.cmd.SetOut(&bytes.Buffer{})
	action.cmd.SetErr(&bytes.Buffer{})
	tasks, err := action.prepareHarborTasks(client, "https://account.services.ai.azure.com/api/projects/project-1")
	if err != nil {
		t.Fatal(err)
	}
	args, cwd := readPublishAzureCLI(t, capture)
	if cwd != filepath.Join(task, "environment") || len(args) != 11 ||
		!strings.HasPrefix(args[5], "project-1-harbor-tasks:task-") {
		t.Fatalf("Harbor build must upload only environment/: args=%v cwd=%q", args, cwd)
	}
	if len(tasks) != 1 || tasks[0].Name != "hello" ||
		tasks[0].AcrImagePath != "example.azurecr.io/"+args[5] ||
		tasks[0].Bundle.Path != "task-data/hello.tar.gz" || bundleRequests != 1 {
		t.Fatalf("unexpected prepared tasks or bundle upload count: %+v, %d", tasks, bundleRequests)
	}
}

func TestBuildEnvironmentCreateRequestMapsManifestConfiguration(t *testing.T) {
	endpoint := "https://acct.services.ai.azure.com/api/projects/proj/agents/support-agent/endpoint/protocols/openai/responses"
	schemaVersion := project.CurrentRleManifestSchemaVersion
	modelName := "Qwen/Qwen3-32B"
	numberOfEpochs := 3
	config := project.RleConfig{
		SchemaVersion: &schemaVersion,
		Rle: project.RleManifest{
			Name:    "support_rle",
			Version: "1.0.1",
		},
		Harness: project.RleHarnessManifest{
			Type:     project.HarnessTypeBYOH,
			Endpoint: &endpoint,
			Protocol: project.HarnessProtocolResponses,
		},
		Model: &project.RleModelDefaults{Name: &modelName},
		Train: &project.RleTrainDefaults{
			Grpo: &project.RleGrpoDefaults{
				NumEpochs: &numberOfEpochs,
			},
		},
	}

	request := buildEnvironmentCreateRequest(config, "example.azurecr.io/support_rle:1.0.1")
	if request.Name != "support_rle" ||
		request.Version != "1.0.1" ||
		request.Harness.Type != "byoh" ||
		request.Harness.Protocol != "responses" ||
		request.Harness.Endpoint == nil || *request.Harness.Endpoint != endpoint ||
		request.SchemaVersion == nil || *request.SchemaVersion != project.CurrentRleManifestSchemaVersion ||
		request.Model == nil ||
		request.Model.Name == nil || *request.Model.Name != modelName {
		t.Fatalf("expected manifest data to map to create request, got %#v", request)
	}

	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	harness, ok := payload["harness"].(map[string]any)
	if !ok {
		t.Fatalf("expected harness object, got %#v", payload["harness"])
	}
	for key, expected := range map[string]string{
		"type":     "byoh",
		"protocol": "responses",
		"endpoint": endpoint,
	} {
		if harness[key] != expected {
			t.Fatalf("expected harness.%s=%q, got %#v", key, expected, harness[key])
		}
	}
	for key, expected := range map[string]string{
		"name":           "support_rle",
		"version":        "1.0.1",
		"schema_version": project.CurrentRleManifestSchemaVersion,
	} {
		if payload[key] != expected {
			t.Fatalf("expected %s=%q, got %#v", key, expected, payload[key])
		}
	}
	for _, removed := range []string{"subtype", "agent_name", "agent_version", "invocation_url"} {
		if _, exists := harness[removed]; exists {
			t.Fatalf("expected request to omit %s, got %s", removed, data)
		}
	}
	if _, exists := payload["versionBump"]; exists {
		t.Fatalf("expected explicit version request to omit versionBump, got %s", data)
	}
	if _, exists := payload["metadata"]; exists {
		t.Fatalf("expected request to omit metadata, got %s", data)
	}
	model, ok := payload["model"].(map[string]any)
	if !ok || model["name"] != modelName {
		t.Fatalf("expected model default, got %#v", payload["model"])
	}
	train, ok := payload["train"].(map[string]any)
	if !ok {
		t.Fatalf("expected train defaults, got %#v", payload["train"])
	}
	grpo, ok := train["grpo"].(map[string]any)
	if !ok || grpo["num_epochs"] != float64(numberOfEpochs) {
		t.Fatalf("expected snake_case grpo payload, got %#v", train["grpo"])
	}
}

func TestBuildEnvironmentCreateRequestMapsByohHarnessConfiguration(t *testing.T) {
	endpoint := "https://harness.example.com/rollouts/"
	config := project.RleConfig{Rle: project.RleManifest{
		Name:    "customer_harness",
		Version: "1.0.1",
	}, Harness: project.RleHarnessManifest{
		Type:     project.HarnessTypeBYOH,
		Endpoint: &endpoint,
		Protocol: project.HarnessProtocolRleInvocation,
	}}

	request := buildEnvironmentCreateRequest(config, "example.azurecr.io/customer_harness:1.0.1")
	if request.Harness.Type != "byoh" ||
		request.Harness.Protocol != "rle-invocation" ||
		request.Harness.Endpoint == nil || *request.Harness.Endpoint != endpoint {
		t.Fatalf("expected byoh manifest data to map to create request, got %#v", request)
	}
}

func TestResolvePublishImageUsesManifestVersion(t *testing.T) {
	t.Setenv("AZURE_CONTAINER_REGISTRY_ENDPOINT", "example.azurecr.io")

	image, err := resolvePublishImage(
		"code_rl",
		"1.2.0",
		"https://account.services.ai.azure.com/api/projects/project-name",
	)
	if err != nil {
		t.Fatal(err)
	}
	if image != "example.azurecr.io/project-name-code-rl:1.2.0" {
		t.Fatalf("unexpected versioned image name %q", image)
	}
}

func TestResolvePublishImageRequiresAcrRegistryForManifest(t *testing.T) {
	t.Setenv("AZURE_CONTAINER_REGISTRY_ENDPOINT", "")
	_, err := resolvePublishImage(
		"code_rl",
		"1.0.0",
		"https://account.services.ai.azure.com/api/projects/project-name",
	)
	var localErr *azdext.LocalError
	if !errors.As(err, &localErr) || localErr.Code != "rle_acr_registry_required" {
		t.Fatalf("expected ACR registry error, got %v", err)
	}
}

func TestVerifyPublishedEnvironmentRequiresManifestIdentity(t *testing.T) {
	config := project.RleConfig{Rle: project.RleManifest{
		Name:    "code_rl",
		Version: "1.0.0",
	}, Harness: project.RleHarnessManifest{
		Type: project.HarnessTypeGym,
	}}
	err := verifyPublishedEnvironment(config, &environmentResource{
		Name:    "code_rl",
		Version: "1.0.1",
		Harness: rleHarness{Type: "gym"},
	})
	var localErr *azdext.LocalError
	if !errors.As(err, &localErr) || localErr.Code != "rle_published_environment_mismatch" {
		t.Fatalf("expected published identity mismatch, got %v", err)
	}
}

func TestVerifyPublishedEnvironmentRequiresManifestDefaults(t *testing.T) {
	schemaVersion := project.CurrentRleManifestSchemaVersion
	modelName := "Qwen/Qwen3-32B"
	differentModelName := "Qwen/Qwen3-14B"
	config := project.RleConfig{
		SchemaVersion: &schemaVersion,
		Rle: project.RleManifest{
			Name:    "code_rl",
			Version: "1.0.0",
		},
		Harness: project.RleHarnessManifest{
			Type: project.HarnessTypeGym,
		},
		Model: &project.RleModelDefaults{Name: &modelName},
	}
	err := verifyPublishedEnvironment(config, &environmentResource{
		Name:          "code_rl",
		Version:       "1.0.0",
		Harness:       rleHarness{Type: "gym"},
		SchemaVersion: &schemaVersion,
		Model:         &project.RleModelDefaults{Name: &differentModelName},
	})
	var localErr *azdext.LocalError
	if !errors.As(err, &localErr) || localErr.Code != "rle_published_environment_mismatch" {
		t.Fatalf("expected published defaults mismatch, got %v", err)
	}
}

func TestPublishGymManifestOmitsEnvironmentProtocol(t *testing.T) {
	dir := t.TempDir()
	content := `[rle]
name = "math_rl"
version = "1.0.0"

[harness]
type = "gym"
`
	if err := os.WriteFile(filepath.Join(dir, project.RleConfigFile), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := project.LoadRleConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(buildEnvironmentCreateRequest(config, "registry.azurecr.io/math:1.0.0"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(body)), "protocol") {
		t.Fatalf("the request must not carry an environment protocol, got %s", body)
	}
	published := &environmentResource{
		Name: "math_rl", Version: "1.0.0", Harness: rleHarness{Type: "gym"},
	}
	if err := verifyPublishedEnvironment(config, published); err != nil {
		t.Fatal(err)
	}
}
func TestPublishRequiresManifestBeforeProjectConfiguration(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	t.Setenv(foundryProjectEndpointEnvVar, "")

	command := newPublishCommand()
	err := command.Execute()
	var localErr *azdext.LocalError
	if !errors.As(err, &localErr) || localErr.Code != "rle_manifest_missing" {
		t.Fatalf("expected missing manifest error, got %v", err)
	}
}

func TestEnvironmentOutputUsesEnvironmentNameField(t *testing.T) {
	body, err := json.Marshal(environmentOutput{
		EnvironmentId:      "env-1",
		EnvironmentVersion: "1.0.0",
		EnvironmentName:    "echo_env",
		Type:               "gym",
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

func TestResolvePublishTargetRequiresInitialManifestVersion(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	t.Setenv(foundryProjectEndpointEnvVar, "https://account.services.ai.azure.com/api/projects/project")
	if err := project.WriteRleConfig(tempDir, project.RleConfig{
		Rle: project.RleManifest{
			Name:    "code_rl",
			Version: "0.1.0",
		},
		Harness: project.RleHarnessManifest{
			Type: project.HarnessTypeGym,
		},
	}); err != nil {
		t.Fatal(err)
	}

	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet ||
			r.URL.Path != testFoundryProjectPath+environmentCollectionPath+"/code_rl" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		http.NotFound(w, r)
	}))
	defer controlPlane.Close()
	stubRleClientEndpoint(t, controlPlane.URL)

	_, _, _, _, err := resolvePublishTarget(t.Context())
	var localErr *azdext.LocalError
	if !errors.As(err, &localErr) || localErr.Code != "rle_manifest_initial_version_invalid" {
		t.Fatalf("expected invalid initial version error, got %v", err)
	}

	if _, err := project.LoadRleConfig(tempDir); err != nil {
		t.Fatalf("expected manifest to remain unchanged after failed preflight: %v", err)
	}
}
