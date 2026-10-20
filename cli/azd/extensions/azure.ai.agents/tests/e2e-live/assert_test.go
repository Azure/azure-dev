// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package e2elive

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestResponseHasExpectedAnswer(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		text string
		want bool
	}{
		{"plain four digit", "The answer is 4.", true},
		{"bare four", "4", true},
		{"equation", "2+2=4", true},
		{"spelled word", "It is four.", true},
		{"spelled upper", "FOUR", true},
		{"parenthesized", "(4)", true},
		{"trailing period mid-sentence", "the value 4. is final", true},
		{"model name", "gpt-4o-mini", false},
		{"version", "4.1", false},
		{"decimal four", "4.0", false}, // intentional: see responseHasExpectedAnswer doc
		{"status code", "404", false},
		{"price", "$40", false},
		{"ratio", "24/7", false},
		{"fourteen", "fourteen apples", false},
		{"no answer", "I am not sure", false},
		{"empty", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := responseHasExpectedAnswer(tc.text); got != tc.want {
				t.Errorf("responseHasExpectedAnswer(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

func TestAgentResponseRegion(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		out  string
		want bool // responseHasExpectedAnswer over the sliced region
	}{
		{
			"answer scoped between markers",
			"using model gpt-4o-mini\n[agent] The answer is 4.\nServer responded in 2s (first byte: 1s)\n",
			true,
		},
		{
			"answer scoped before client elapsed",
			"[agent] The answer is 4.\nClient elapsed: 2.000s\n",
			true,
		},
		{
			"latency measurements are not part of the answer",
			"[agent] I am not sure.\nClient elapsed: 1.000s\n" +
				"Platform latency (warm): response headers 4 ms\n  preprocess 0 ms | container 4 ms\n",
			false,
		},
		{
			"stray digits outside region rejected",
			"gpt-4o-mini deployed (404 cached)\n[agent] I am not sure.\nServer responded in 4.0s\n",
			false,
		},
		{
			"standalone 4 before agent line excluded by region",
			"completed step 4\n[agent] I don't know.\nServer responded in 1s\n",
			false,
		},
		{
			"missing footer falls back to full text",
			"using gpt-4o-mini\n[agent] four",
			true,
		},
		{
			"no agent line falls back to full text",
			"the answer is four",
			true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := responseHasExpectedAnswer(agentResponseRegion(tc.out)); got != tc.want {
				t.Errorf("region(%q) -> %v, want %v", tc.out, got, tc.want)
			}
		})
	}
}

func TestSnapshotDependencyCommands(t *testing.T) {
	commands := snapshotDependencyCommands()
	require.Len(t, commands, 2)
	require.Equal(t, "add", commands[0][0])
	require.Contains(t, commands[0], "agent-framework-foundry-hosting==1.0.0b260821")
	require.Contains(t, commands[0], "azure-ai-agentserver-core==2.3.0b1")
	require.Contains(t, commands[0], "azure-ai-agentserver-responses==2.4.0b1")
	require.Equal(t, "export", commands[1][0])
	require.Contains(t, commands[1], "--locked")
	for _, command := range commands {
		require.NotContains(t, command, "--prerelease")
		require.NotContains(t, command, "--frozen")
	}
}

func TestSnapshotBuildDockerfile(t *testing.T) {
	input := "FROM ghcr.io/astral-sh/uv:0.11.7 AS uv\nFROM python:3.13-slim\n" +
		"COPY --from=uv /uv /uvx /bin/\nRUN uv sync --frozen --no-dev --no-install-project\n" +
		"CMD [\"/app/user_agent/.venv/bin/python\", \"main.py\"]\n"
	data, err := snapshotBuildDockerfile([]byte(input))
	require.NoError(t, err)
	require.Contains(t, string(data), "FROM mcr.microsoft.com/devcontainers/python@sha256:")
	require.Contains(t, string(data), "python -m pip install --no-cache-dir uv==0.11.7")
	require.Contains(t, string(data), " uv==0.11.7")
	require.NotContains(t, string(data), "ghcr.io")
	require.NotContains(t, string(data), "FROM python:")
	require.NotContains(t, string(data), "COPY --from=uv")
	require.Equal(t, 2, strings.Count(string(data), "--mount=type=secret,id=python_index,required=true"))
	require.Contains(t, string(data), "UV_NO_CACHE=1")
	require.Contains(t, string(data), "UV_DEFAULT_INDEX=\"$(cat /run/secrets/python_index)\"")
	require.Contains(t, string(data), " uv sync --frozen --no-dev --no-install-project")
	require.Contains(t, string(data), "CMD [\"/app/user_agent/.venv/bin/python\", \"main.py\"]")
	require.NotContains(t, string(data), "ARG ")
	require.NotContains(t, string(data), "ENV ")
	require.NotContains(t, string(data), "@pkgs")
	_, err = snapshotBuildDockerfile([]byte("FROM python:3.13-slim"))
	require.Error(t, err)
}

func TestSnapshotBuildProject(t *testing.T) {
	manifest := "services:\n  agent:\n    host: azure.ai.agent\n    kind: hosted\n    language: python\n    project: src/agent\n"
	project, err := snapshotBuildProject([]byte(manifest))
	require.NoError(t, err)
	require.Equal(t, "src/agent", project)
	t.Run("initialized container sample", func(t *testing.T) {
		containerManifest := strings.ReplaceAll(manifest, "language: python", "language: docker")
		project, err := snapshotBuildProject([]byte(containerManifest))
		require.NoError(t, err)
		require.Equal(t, "src/agent", project)
	})
	for _, invalid := range []string{
		strings.ReplaceAll(manifest, "src/agent", "../outside"),
		strings.ReplaceAll(manifest, "src/agent", "/outside"),
		strings.ReplaceAll(manifest, "language: python", "language: dotnet"),
		strings.ReplaceAll(manifest, "kind: hosted", "kind: prompt"),
		manifest + "  second:\n    host: azure.ai.agent\n    kind: hosted\n    language: python\n    project: src/second\n",
		"services: {}", "services: [",
	} {
		_, err := snapshotBuildProject([]byte(invalid))
		require.Error(t, err)
	}
}

func TestUpdateSnapshotSampleManifest(t *testing.T) {
	image := "registry.azurecr.io/agent@sha256:" + strings.Repeat("a", 64)
	original := []byte("services:\n  agent:\n    host: azure.ai.agent\n    kind: hosted\n")
	t.Run("updates within project root", func(t *testing.T) {
		root, err := os.OpenRoot(t.TempDir())
		require.NoError(t, err)
		defer root.Close()
		require.NoError(t, root.WriteFile("azure.yaml", original, 0o600))
		require.NoError(t, updateSnapshotSampleManifest(root, image))
		data, err := root.ReadFile("azure.yaml")
		require.NoError(t, err)
		require.Contains(t, string(data), "enableMemorySnapshot: true")
		require.Contains(t, string(data), image)
	})
	t.Run("rejects escaping manifest symlink", func(t *testing.T) {
		outside := filepath.Join(t.TempDir(), "outside.yaml")
		require.NoError(t, os.WriteFile(outside, original, 0o600))
		projectDir := t.TempDir()
		if err := os.Symlink(outside, filepath.Join(projectDir, "azure.yaml")); err != nil {
			t.Skipf("symlink creation is unavailable: %v", err)
		}
		root, err := os.OpenRoot(projectDir)
		require.NoError(t, err)
		defer root.Close()
		require.Error(t, updateSnapshotSampleManifest(root, image))
		data, err := os.ReadFile(outside)
		require.NoError(t, err)
		require.Equal(t, original, data)
	})
}

func TestEnableSampleMemorySnapshot(t *testing.T) {
	image := "registry.azurecr.io/agent@sha256:" + strings.Repeat("a", 64)
	for _, testCase := range []struct {
		name     string
		manifest string
		wantErr  string
	}{
		{"hosted", "services:\n  agent:\n    host: azure.ai.agent\n    kind: hosted\n    project: src\n", ""},
		{"referenced", "services:\n  agent:\n    host: azure.ai.agent\n    $ref: agent.yaml\n", ""},
		{"disabled", "services:\n  agent:\n    host: azure.ai.agent\n    enableMemorySnapshot: false\n", ""},
		{"docker options", "services:\n  agent:\n    host: azure.ai.agent\n    docker:\n      remoteBuild: true\n      path: Dockerfile\n", ""},
		{"invalid docker", "services:\n  agent:\n    host: azure.ai.agent\n    docker: true\n", "must be a mapping"},
		{"prompt", "services:\n  agent:\n    host: azure.ai.agent\n    kind: prompt\n", "hosted agent"},
		{"no services", "name: sample\n", "must contain services"},
		{"no agent", "services:\n  project:\n    host: azure.ai.project\n", "no azure.ai.agent"},
		{"multiple agents", "services:\n  first:\n    host: azure.ai.agent\n  second:\n    host: azure.ai.agent\n", "exactly one"},
		{"invalid yaml", "services: [", "parse sample manifest"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			data, err := enableSampleMemorySnapshot([]byte(testCase.manifest), image)
			if testCase.wantErr != "" {
				require.ErrorContains(t, err, testCase.wantErr)
				return
			}
			require.NoError(t, err)
			var before, after struct {
				Services map[string]map[string]any `yaml:"services"`
			}
			require.NoError(t, yaml.Unmarshal([]byte(testCase.manifest), &before))
			require.NoError(t, yaml.Unmarshal(data, &after))
			before.Services["agent"]["enableMemorySnapshot"] = true
			before.Services["agent"]["image"] = image
			dockerOptions := map[string]any{"imagePassthrough": true, "remoteBuild": false}
			if testCase.name == "docker options" {
				dockerOptions["path"] = "Dockerfile"
			}
			before.Services["agent"]["docker"] = dockerOptions
			require.Equal(t, before, after)
		})
	}
}

func TestSnapshotCodeDeployment(t *testing.T) {
	manifest := []byte("services:\n  agent:\n    host: azure.ai.agent\n    kind: hosted\n" +
		"    language: python\n    codeConfiguration:\n      runtime: python_3.13\n      entryPoint: main.py\n")
	updated, err := enableSampleMemorySnapshot(manifest, "")
	require.NoError(t, err)
	var before, after struct {
		Services map[string]map[string]any `yaml:"services"`
	}
	require.NoError(t, yaml.Unmarshal(manifest, &before))
	require.NoError(t, yaml.Unmarshal(updated, &after))
	before.Services["agent"]["enableMemorySnapshot"] = true
	require.Equal(t, before, after)
	_, err = enableSampleMemorySnapshot([]byte("services:\n  agent:\n    host: azure.ai.agent\n    kind: hosted\n"), "")
	require.ErrorContains(t, err, "requires codeConfiguration")
	version := []byte(`{"status":"active","definition":{"kind":"hosted","enable_memory_snapshot":true,` +
		`"code_configuration":{"runtime":"python_3.13"}}}`)
	require.NoError(t, validateSnapshotAgentVersion(version, ""))
	require.ErrorContains(t, validateSnapshotAgentVersion([]byte(
		`{"status":"active","definition":{"kind":"hosted","enable_memory_snapshot":true}}`), ""),
		"code_configuration")
}

func TestValidateSnapshotAgentVersion(t *testing.T) {
	image := "registry.azurecr.io/agent@sha256:" + strings.Repeat("a", 64)
	for _, testCase := range []struct {
		name    string
		data    string
		wantErr string
	}{
		{"enabled active", `{"status":"active","definition":{"kind":"hosted","enable_memory_snapshot":true,` +
			`"container_configuration":{"image":"` + image + `"}}}`, ""},
		{"missing image", `{"status":"active","definition":{"kind":"hosted","enable_memory_snapshot":true}}`, "pinned"},
		{"mutable image", `{"status":"active","definition":{"kind":"hosted","enable_memory_snapshot":true,` +
			`"container_configuration":{"image":"registry.azurecr.io/agent:latest"}}}`, "pinned"},
		{"different image", `{"status":"active","definition":{"kind":"hosted","enable_memory_snapshot":true,` +
			`"container_configuration":{"image":"registry.azurecr.io/other@sha256:` +
			strings.Repeat("a", 64) + `"}}}`, "does not match"},
		{"missing opt-in", `{"status":"active","definition":{"kind":"hosted"}}`, "did not confirm"},
		{"disabled", `{"status":"active","definition":{"kind":"hosted","enable_memory_snapshot":false}}`, "did not confirm"},
		{"wrong kind", `{"status":"active","definition":{"kind":"prompt","enable_memory_snapshot":true}}`, "did not confirm"},
		{"creating", `{"status":"creating","definition":{"kind":"hosted","enable_memory_snapshot":true}}`, "not active"},
		{"failed", `{"status":"failed"}`, "not active"},
		{"invalid json", `{`, "parse deployed agent version"},
		{"wrong type", `{"status":"active","definition":{"kind":"hosted","enable_memory_snapshot":"true"}}`, "parse"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			err := validateSnapshotAgentVersion([]byte(testCase.data), image)
			if testCase.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, testCase.wantErr)
			}
		})
	}
}

func TestStackedCILintDispatch(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", "..", "..", ".github", "workflows", "lint-ext-azure-ai-agents.yml")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var workflow struct {
		On map[string]yaml.Node `yaml:"on"`
	}
	require.NoError(t, yaml.Unmarshal(data, &workflow))
	require.Contains(t, workflow.On, "workflow_dispatch")
	require.Contains(t, workflow.On, "pull_request")
}

func TestSnapshotLivePipelineConfiguration(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", "..", "..", "eng", "pipelines", "ext-azure-ai-agents-live.yml")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var pipeline struct {
		Parameters []struct {
			Name    string `yaml:"name"`
			Default any    `yaml:"default"`
		} `yaml:"parameters"`
		Extends struct {
			Parameters struct {
				Stages []struct {
					Jobs []struct {
						Steps []map[string]yaml.Node `yaml:"steps"`
					} `yaml:"jobs"`
				} `yaml:"stages"`
			} `yaml:"parameters"`
		} `yaml:"extends"`
	}
	require.NoError(t, yaml.Unmarshal(data, &pipeline))
	defaults := map[string]any{}
	for _, parameter := range pipeline.Parameters {
		defaults[parameter.Name] = parameter.Default
	}
	require.Equal(t, false, defaults["memorySnapshot"])
	require.Equal(t, "", defaults["templateURL"])
	require.Equal(t, "eastus2", defaults["location"])
	require.Equal(t, "both", defaults["deployModes"])
	require.Equal(t, "", defaults["snapshotImage"])
	require.Equal(t, "", defaults["subscriptionId"])
	require.Len(t, pipeline.Extends.Parameters.Stages, 1)
	require.Len(t, pipeline.Extends.Parameters.Stages[0].Jobs, 1)
	var prerequisiteLine, firstAzureTaskLine int
	var subscriptionBoundSteps int
	for _, step := range pipeline.Extends.Parameters.Stages[0].Jobs[0].Steps {
		if displayName, exists := step["displayName"]; exists &&
			(displayName.Value == "Run Tier 2 live golden path" || displayName.Value == "Cleanup leaked Azure resources") {
			var inputs struct {
				InlineScript string `yaml:"inlineScript"`
			}
			var env map[string]yaml.Node
			inputsNode, envNode := step["inputs"], step["env"]
			require.NoError(t, inputsNode.Decode(&inputs))
			require.NoError(t, envNode.Decode(&env))
			require.Equal(t, "${{ parameters.subscriptionId }}", env["E2E_TARGET_SUBSCRIPTION"].Value)
			selectSubscription := strings.Index(inputs.InlineScript, `az account set --subscription "$E2E_TARGET_SUBSCRIPTION"`)
			require.NotEqual(t, -1, selectSubscription)
			if displayName.Value == "Run Tier 2 live golden path" {
				require.Contains(t, inputs.InlineScript, "requires an explicit preview-approved subscriptionId")
				require.Contains(t, inputs.InlineScript, "does not match the requested subscriptionId")
				readSubscription := strings.Index(inputs.InlineScript, `E2E_SUBSCRIPTION="$(az account show`)
				require.Greater(t, readSubscription, selectSubscription)
			} else {
				require.Contains(t, inputs.InlineScript, `export AZURE_SUBSCRIPTION_ID`)
				require.Contains(t, inputs.InlineScript, `az group update --subscription "$AZURE_SUBSCRIPTION_ID"`)
			}
			subscriptionBoundSteps++
		}
		if task, exists := step["task"]; exists && task.Value == "AzureCLI@2" && firstAzureTaskLine == 0 {
			firstAzureTaskLine = task.Line
		}
		if setup, exists := step["${{ if eq(parameters.memorySnapshot, true) }}"]; exists && setup.Kind == yaml.SequenceNode {
			var setupSteps []struct {
				Bash     string            `yaml:"bash"`
				Task     string            `yaml:"task"`
				Template string            `yaml:"template"`
				Env      map[string]string `yaml:"env"`
				Inputs   struct {
					VersionSpec string `yaml:"versionSpec"`
				} `yaml:"inputs"`
			}
			require.NoError(t, setup.Decode(&setupSteps))
			require.Len(t, setupSteps, 4)
			require.Equal(t, "UsePythonVersion@0", setupSteps[0].Task)
			require.Equal(t, "3.13", setupSteps[0].Inputs.VersionSpec)
			require.Equal(t, "/eng/common/pipelines/templates/steps/python-auth-dev-feed.yml", setupSteps[1].Template)
			require.Contains(t, setupSteps[2].Bash, "uv==0.11.7")
			require.Equal(t, "$(PIP_INDEX_URL)", setupSteps[2].Env["PIP_INDEX_URL"])
			require.Contains(t, setupSteps[3].Bash, "docker version")
			require.Contains(t, setupSteps[3].Bash, "code) ;;")
			require.Contains(t, setupSteps[3].Bash, "container")
			require.NotContains(t, setupSteps[3].Bash, "-z")
			prerequisiteLine = setup.Line
		}
	}
	require.Positive(t, prerequisiteLine)
	require.Positive(t, firstAzureTaskLine)
	require.Less(t, prerequisiteLine, firstAzureTaskLine)
	require.Equal(t, 2, subscriptionBoundSteps)
}

func TestSnapshotImagePython(t *testing.T) {
	for _, testCase := range []struct{ inspection, expected string }{
		{`[{"Config":{"Cmd":["/app/user_agent/.venv/bin/python","main.py"]}}]`, "/app/user_agent/.venv/bin/python"},
		{`[{"Config":{"Entrypoint":["python3.13"],"Cmd":["main.py"]}}]`, "python3.13"},
		{`[{"Config":{"Cmd":["python","main.py"]}}]`, "python"},
		{`[{"Config":{"Cmd":["uv","run","main.py"]}}]`, "python"},
	} {
		command, err := snapshotImagePython([]byte(testCase.inspection))
		require.NoError(t, err)
		require.Equal(t, testCase.expected, command)
	}
	_, err := snapshotImagePython([]byte(`[]`))
	require.Error(t, err)
}

func TestSnapshotRegistryLoginArgs(t *testing.T) {
	require.Equal(t, []string{"acr", "login", "--name", "registry"}, snapshotRegistryLoginArgs("registry", ""))
	require.Equal(t, []string{"acr", "login", "--name", "registry", "--subscription", "approved-subscription"},
		snapshotRegistryLoginArgs("registry", "approved-subscription"))
}

func TestSnapshotImageDependencyCheckArgs(t *testing.T) {
	image := "registry.azurecr.io/agent@sha256:" + strings.Repeat("a", 64)
	python := "/app/user_agent/.venv/bin/python"
	for _, testCase := range []struct {
		name    string
		hasPip  bool
		checker string
		args    []string
	}{
		{"pip environment", true, python, []string{"-m", "pip", "check"}},
		{"uv environment", false, "uv", []string{"pip", "check", "--python", python}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			expected := []string{"run", "--rm", "--network", "none", "--read-only",
				"--tmpfs", "/tmp:rw,nosuid,nodev,noexec,size=64m", "--env", "UV_NO_CACHE=1",
				"--entrypoint", testCase.checker, image}
			expected = append(expected, testCase.args...)
			require.Equal(t, expected, snapshotImageDependencyCheckArgs(image, python, testCase.hasPip))
		})
	}
}

func TestSnapshotGuideImageRequirements(t *testing.T) {
	validImage := "registry.azurecr.io/agent@sha256:" + strings.Repeat("a", 64)
	require.NoError(t, validateSnapshotImageReference(validImage))
	for _, image := range []string{
		"", "registry.azurecr.io/agent:latest", "agent@sha256:" + strings.Repeat("a", 64),
		"registry.azurecr.io/agent@sha256:abc", "registry.azurecr.io/agent@sha256:" + strings.Repeat("z", 64),
		"user:password@registry.azurecr.io/agent@sha256:" + strings.Repeat("a", 64),
		"https://" + validImage,
	} {
		require.Error(t, validateSnapshotImageReference(image))
	}
	inspection := []byte(`[{"Os":"linux","Architecture":"amd64"}]`)
	packages := []byte(`{"azure-ai-agentserver-core":"2.3.0b1","azure-ai-agentserver-responses":"2.4.0b1"}`)
	require.NoError(t, validateSnapshotImageRuntime(inspection, packages))
	for _, invalid := range []string{
		`[]`, `[{"Os":"linux","Architecture":"arm64"}]`, `[{"Os":"windows","Architecture":"amd64"}]`, `{`,
	} {
		require.Error(t, validateSnapshotImageRuntime([]byte(invalid), packages))
	}
	for _, invalid := range []string{
		`{}`, `{`, `{"azure-ai-agentserver-core":"2.2.0","azure-ai-agentserver-responses":"2.4.0b1"}`,
		`{"azure-ai-agentserver-core":"2.3.0b1","azure-ai-agentserver-responses":"2.3.0"}`,
	} {
		require.Error(t, validateSnapshotImageRuntime(inspection, []byte(invalid)))
	}
}

func TestSnapshotStatelessResponsesContract(t *testing.T) {
	body := snapshotResponsesRequest("snapshot-session")
	require.Equal(t, false, body["store"])
	require.Equal(t, false, body["stream"])
	require.Equal(t, "snapshot-session", body["agent_session_id"])
	require.NotContains(t, body, "conversation")
	data, err := json.Marshal(body)
	require.NoError(t, err)
	require.Contains(t, string(data), `"store":false`)
	require.NoError(t, validateSnapshotResponse([]byte(
		`{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"4"}]}]}`)))
	for _, invalid := range []string{
		`{`, `{"status":"failed"}`, `{"status":"completed","model":"gpt-4o","output":[]}`,
		`{"status":"completed","output":[{"type":"tool_call","content":[{"type":"output_text","text":"4"}]}]}`,
	} {
		require.Error(t, validateSnapshotResponse([]byte(invalid)))
	}
}
