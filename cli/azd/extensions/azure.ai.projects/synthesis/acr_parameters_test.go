// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"azure.ai.projects/synthesis/internal/testutil"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockexec"
	"github.com/stretchr/testify/require"
)

const parameterRegistryID = "/subscriptions/sub/resourceGroups/shared/providers/Microsoft.ContainerRegistry/registries/registry"

type parameterCompiler struct {
	calls int
	build func(string, []string) (bicep.BuildResult, error)
}

func (c *parameterCompiler) BuildBicepParam(
	_ context.Context, path string, env []string,
) (bicep.BuildResult, error) {
	c.calls++
	return c.build(path, env)
}

func parameterInput(t *testing.T, root string) Input {
	t.Helper()
	//nolint:gosec // isolated project fixture
	raw, err := os.ReadFile(filepath.Join(root, "azure.yaml"))
	require.NoError(t, err)
	return Input{ProjectRoot: root, RawAzureYAML: raw, Env: map[string]string{
		"AZURE_SUBSCRIPTION_ID": "sub", "AZURE_RESOURCE_GROUP": "foundry", "AZURE_ENV_NAME": "test",
	}}
}

func TestARMParameterValueCaseInsensitive(t *testing.T) {
	template := map[string]any{"parameters": map[string]any{
		"ExistingACRResourceID": map[string]any{"defaultValue": parameterRegistryID},
	}}
	for _, name := range []string{"existingAcrResourceId", "EXISTINGACRRESOURCEID", "ExistingAcrResourceId"} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, parameterRegistryID, ARMParameterValue(template, nil, name))
			parameters := map[string]any{
				strings.ToUpper(name): map[string]any{"value": "explicit"},
			}
			require.Equal(t, "explicit", ARMParameterValue(template, parameters, name))
			parameters[strings.ToUpper(name)] = map[string]any{"value": ""}
			require.Equal(t, "", ARMParameterValue(template, parameters, name))
		})
	}
}

func TestACRParametersDisabledJSON(t *testing.T) {
	t.Setenv(ACRReuseEnvVar, "")
	for _, test := range []struct {
		name     string
		value    string
		env      map[string]string
		disabled bool
	}{
		{"literal", parameterRegistryID, nil, true},
		{"substitution", "${SHARED_REGISTRY}", map[string]string{"SHARED_REGISTRY": parameterRegistryID}, true},
		{"default", "${UNSET_REGISTRY=" + parameterRegistryID + "}", nil, true},
		{"process environment", "${UNSET_REGISTRY}", nil, true},
		{"explicit empty", "${SHARED_REGISTRY}", map[string]string{"SHARED_REGISTRY": ""}, false},
		{"create", "", nil, false},
	} {
		for _, layer := range []bool{false, true} {
			name := test.name + "/" + map[bool]string{false: "root", true: "layer"}[layer]
			t.Run(name, func(t *testing.T) {
				root := t.TempDir()
				testutil.ACRParameters(t, root, layer, "json", test.value)
				in := parameterInput(t, root)
				maps.Copy(in.Env, test.env)
				t.Setenv("UNSET_REGISTRY", parameterRegistryID)
				t.Setenv("SHARED_REGISTRY", parameterRegistryID)
				before := testutil.Snapshot(t, root)
				err := requireACRReuseForOnDiskInput(t.Context(), in, nil)
				if test.disabled {
					local, ok := errors.AsType[*azdext.LocalError](err)
					require.True(t, ok, "%v", err)
					require.Equal(t, "feature_not_enabled", local.Code)
					require.Equal(t, azdext.LocalErrorCategoryCompatibility, local.Category)
					require.Contains(t, local.Message, ACRReuseEnvVar+"=true")
				} else {
					require.NoError(t, err)
				}
				require.Equal(t, before, testutil.Snapshot(t, root))
			})
		}
	}
}

func TestACRParametersBicepExpressionsAndPrecedence(t *testing.T) {
	t.Setenv(ACRReuseEnvVar, "")
	for _, id := range []string{"", parameterRegistryID} {
		for _, layer := range []bool{false, true} {
			name := map[bool]string{false: "create", true: "reuse"}[id != ""] + "/" +
				map[bool]string{false: "root", true: "layer"}[layer]
			t.Run(name, func(t *testing.T) {
				root := t.TempDir()
				testutil.ACRParameters(t, root, layer, "json", parameterRegistryID)
				dir, module := filepath.Join(root, "infra"), "main"
				if layer {
					dir, module = filepath.Join(dir, "foundry"), "project"
				}
				path := filepath.Join(dir, module+".bicepparam")
				source := "using './" + module + ".bicep'\n" +
					"param existingAcrResourceId = readEnvironmentVariable('SHARED_REGISTRY')\n"
				require.NoError(t, os.WriteFile(path, []byte(source), 0o600))
				in := parameterInput(t, root)
				in.Env["SHARED_REGISTRY"] = id
				beforeEnv, beforeFiles := maps.Clone(in.Env), testutil.Snapshot(t, root)
				compiler := &parameterCompiler{build: func(gotPath string, env []string) (bicep.BuildResult, error) {
					require.Equal(t, path, gotPath)
					require.Contains(t, env, "SHARED_REGISTRY="+id)
					parameters, err := json.Marshal(map[string]any{
						"parameters": map[string]any{"existingAcrResourceId": map[string]any{"value": id}},
					})
					require.NoError(t, err)
					envelope, err := json.Marshal(map[string]string{
						"templateJson": `{"parameters":{"existingAcrResourceId":{"defaultValue":"` +
							parameterRegistryID + `"}},"resources":[]}`,
						"parametersJson": string(parameters),
					})
					require.NoError(t, err)
					return bicep.BuildResult{Compiled: string(envelope)}, nil
				}}
				err := requireACRReuseForOnDiskInput(t.Context(), in, compiler)
				if id != "" {
					local, ok := errors.AsType[*azdext.LocalError](err)
					require.True(t, ok, "%v", err)
					require.Equal(t, "feature_not_enabled", local.Code)
				} else {
					require.NoError(t, err, "Bicep parameters override both JSON and template defaults")
				}
				require.Equal(t, 1, compiler.calls)
				require.Equal(t, beforeEnv, in.Env)
				require.Equal(t, beforeFiles, testutil.Snapshot(t, root))
			})
		}
	}
}

func TestACRParametersInvalidInputs(t *testing.T) {
	t.Setenv(ACRReuseEnvVar, "")
	for _, raw := range []string{
		"{", `{"parameters":[]}`, `{"parameters":{"existingAcrResourceId":true}}`,
		`{"parameters":{"existingAcrResourceId":{"value":false}}}`,
		`{"parameters":{"existingAcrResourceId":{"reference":{"secretName":"registry"}}}}`,
		`{"parameters":{"EXISTINGACRRESOURCEID":true}}`,
		`{"parameters":{"ExistingACRResourceID":{"value":false}}}`,
		`{"parameters":{"ExistingACRResourceID":{"reference":{"secretName":"registry"}}}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			root := t.TempDir()
			testutil.ACRParameters(t, root, false, "json", "")
			require.NoError(t, os.WriteFile(filepath.Join(root, "infra", "main.parameters.json"), []byte(raw), 0o600))
			before := testutil.Snapshot(t, root)
			err := requireACRReuseForOnDiskInput(t.Context(), parameterInput(t, root), nil)
			local, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok, "%v", err)
			require.Equal(t, "ondisk_parameters_invalid", local.Code)
			require.Equal(t, azdext.LocalErrorCategoryValidation, local.Category)
			require.Equal(t, before, testutil.Snapshot(t, root))
		})
	}
}

func TestACRParametersSupportedTargetOnly(t *testing.T) {
	t.Setenv(ACRReuseEnvVar, "")
	for _, test := range []struct {
		name   string
		change func(*Input)
	}{
		{"native root", func(in *Input) {
			in.RawAzureYAML = []byte(strings.ReplaceAll(string(in.RawAzureYAML), "microsoft.foundry", "bicep"))
		}},
		{"native root with unrelated reference", func(in *Input) {
			in.RawAzureYAML = []byte("infra:\n  provider: bicep\nservices:\n  app:\n    $ref: absent.yaml\n")
		}},
		{"brownfield", func(in *Input) {
			in.Env["USE_EXISTING_AI_PROJECT"] = "true"
		}},
		{"endpoint", func(in *Input) {
			in.RawAzureYAML = []byte(strings.ReplaceAll(string(in.RawAzureYAML),
				"host: azure.ai.project", "host: azure.ai.project\n    endpoint: https://account.services.ai.azure.com/api/projects/project"))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			testutil.ACRParameters(t, root, false, "json", parameterRegistryID)
			in := parameterInput(t, root)
			test.change(&in)
			require.NoError(t, requireACRReuseForOnDiskInput(t.Context(), in, nil))
		})
	}
	t.Run("unrelated root and layer files", func(t *testing.T) {
		root := t.TempDir()
		testutil.ACRParameters(t, root, true, "json", "")
		require.NoError(t, os.WriteFile(filepath.Join(root, "infra", "main.bicepparam"), []byte("invalid"), 0o600))
		require.NoError(t, os.Mkdir(filepath.Join(root, "infra", "app"), 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(root, "infra", "app", "main.bicepparam"), []byte("invalid"), 0o600))
		require.NoError(t, requireACRReuseForOnDiskInput(t.Context(), parameterInput(t, root), nil))
	})
	t.Run("parameter file without a source", func(t *testing.T) {
		root := t.TempDir()
		testutil.ACRParameters(t, root, false, "json", parameterRegistryID)
		require.NoError(t, os.Remove(filepath.Join(root, "infra", "main.bicep")))
		require.NoError(t, requireACRReuseForOnDiskInput(t.Context(), parameterInput(t, root), nil))
	})
}

func TestACRParametersInvalidTarget(t *testing.T) {
	t.Setenv(ACRReuseEnvVar, "")
	for _, config := range []string{
		"  path: ../outside\n", "  path: ..\\outside\n", "  path: C:\\outside\n",
		"  module: ../main\n", "  module: main.bicep\n",
		"  layers:\n    - provider: microsoft.foundry\n",
	} {
		root := t.TempDir()
		in := Input{ProjectRoot: root, Env: map[string]string{},
			RawAzureYAML: []byte("infra:\n  provider: microsoft.foundry\n" + config)}
		err := requireACRReuseForOnDiskInput(t.Context(), in, nil)
		local, ok := errors.AsType[*azdext.LocalError](err)
		require.True(t, ok, "%v", err)
		require.Equal(t, "invalid_service_config", local.Code)
	}
}

type parameterDownloadTransport struct {
	calls int
}

func (s *parameterDownloadTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	s.calls++
	return &http.Response{
		StatusCode: http.StatusOK, Header: http.Header{},
		Body: io.NopCloser(strings.NewReader("test compiler")), Request: request,
	}, nil
}

func TestACRParametersCompilerDependencies(t *testing.T) {
	for _, selection := range []struct {
		command string
		id      string
	}{
		{command: "build"},
		{command: "build-params"},
		{command: "build-params", id: parameterRegistryID},
	} {
		for _, cached := range []bool{false, true} {
			name := selection.command + "/" + map[bool]string{false: "create", true: "reuse"}[selection.id != ""] +
				"/" + map[bool]string{false: "missing compiler", true: "cached compiler"}[cached]
			t.Run(name, func(t *testing.T) {
				t.Setenv(ACRReuseEnvVar, "")
				root := t.TempDir()
				testutil.ACRParameters(t, root, false, "json", "")
				source := filepath.Join(root, "infra", "main.bicep")
				if selection.command == "build-params" {
					source += "param"
					require.NoError(t, os.WriteFile(source, []byte("using './main.bicep'\n"+
						"param existingAcrResourceId = readEnvironmentVariable('SHARED_REGISTRY')\n"), 0o600))
				}
				config := filepath.Join(t.TempDir(), "config")
				cache := filepath.Join(t.TempDir(), "modules")
				t.Setenv("AZD_CONFIG_DIR", config)
				t.Setenv("BICEP_CACHE_ROOT_DIRECTORY", cache)
				t.Setenv("AZD_BICEP_TOOL_PATH", "")
				toolName := "bicep"
				if runtime.GOOS == "windows" {
					toolName += ".exe"
				}
				tool := filepath.Join(config, "bin", toolName)
				if cached {
					require.NoError(t, os.MkdirAll(filepath.Dir(tool), 0o750))
					require.NoError(t, os.WriteFile(tool, []byte("cached compiler"), 0o600))
				}
				download := &parameterDownloadTransport{}
				previousClient := http.DefaultClient
				http.DefaultClient = &http.Client{Transport: download}
				t.Cleanup(func() { http.DefaultClient = previousClient })
				in := parameterInput(t, root)
				in.Env["SHARED_REGISTRY"] = selection.id
				beforeFiles, beforeValues := testutil.Snapshot(t, root), maps.Clone(in.Env)
				template := `{"parameters":{"existingAcrResourceId":{"defaultValue":""}},"resources":[]}`
				parameters := `{"parameters":{"existingAcrResourceId":{"value":"` + selection.id + `"}}}`
				envelope, err := json.Marshal(map[string]string{
					"templateJson": template, "parametersJson": parameters,
				})
				require.NoError(t, err)
				versions, builds := 0, 0
				runner := mockexec.NewMockCommandRunner()
				runner.When(func(exec.RunArgs, string) bool { return true }).
					RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
						require.Equal(t, tool, args.Cmd)
						if len(args.Args) == 1 && args.Args[0] == "--version" {
							versions++
							return exec.RunResult{Stdout: fmt.Sprintf("Bicep CLI version %s (abcdef0123)",
								bicep.Version.String())}, nil
						}
						builds++
						require.Equal(t, []string{selection.command, source, "--stdout"}, args.Args)
						if selection.command == "build-params" {
							require.Contains(t, args.Env, "SHARED_REGISTRY="+selection.id)
						}
						require.NoError(t, os.MkdirAll(cache, 0o750))
						require.NoError(t, os.WriteFile(filepath.Join(cache, "restored-module"),
							[]byte("module"), 0o600))
						if selection.command == "build" {
							return exec.RunResult{Stdout: template}, nil
						}
						return exec.RunResult{Stdout: string(envelope)}, nil
					})
				console := input.NewConsole(false, false, input.Writers{Output: io.Discard},
					input.ConsoleHandles{}, nil, nil)
				compiler := bicep.NewCli(console, runner)
				if selection.command == "build" {
					_, err = compiler.Build(t.Context(), source)
				} else {
					err = requireACRReuseForOnDiskInput(t.Context(), in, compiler)
				}
				if selection.id != "" {
					requireACRDisabled(t, err)
				} else {
					require.NoError(t, err)
				}
				require.Equal(t, 1, versions)
				require.Equal(t, 1, builds)
				require.Equal(t, map[bool]int{false: 1, true: 0}[cached], download.calls)
				require.FileExists(t, tool)
				require.FileExists(t, filepath.Join(cache, "restored-module"))
				require.NoFileExists(t, filepath.Join(config, "config.json"))
				require.Equal(t, beforeFiles, testutil.Snapshot(t, root))
				require.Equal(t, beforeValues, in.Env)
			})
		}
	}
}

func TestACRParametersInvalidCompilerOverride(t *testing.T) {
	t.Setenv(ACRReuseEnvVar, "")
	root := t.TempDir()
	testutil.ACRParameters(t, root, false, "json", "")
	source := filepath.Join(root, "infra", "main.bicepparam")
	require.NoError(t, os.WriteFile(source, []byte("using './main.bicep'\n"), 0o600))
	t.Setenv("AZD_BICEP_TOOL_PATH", filepath.Join(root, "missing-bicep"))
	before := testutil.Snapshot(t, root)
	err := requireACRReuseForOnDiskInput(t.Context(), parameterInput(t, root), NewBicepCompiler())
	local, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok, "%v", err)
	require.Equal(t, "ondisk_bicep_compile_failed", local.Code)
	require.Equal(t, before, testutil.Snapshot(t, root))
}

func TestACRParametersEnabledSkipsPreflight(t *testing.T) {
	t.Setenv(ACRReuseEnvVar, "true")
	root := t.TempDir()
	testutil.ACRParameters(t, root, false, "bicepparam", parameterRegistryID)
	compiler := &parameterCompiler{}
	require.NoError(t, requireACRReuseForOnDiskInput(t.Context(), parameterInput(t, root), compiler))
	require.Zero(t, compiler.calls)
}

func TestACRParametersCompilerResults(t *testing.T) {
	t.Setenv(ACRReuseEnvVar, "")
	for _, test := range []struct {
		name, template, parameters, code string
		buildErr                         error
	}{
		{"template default",
			`{"parameters":{"existingAcrResourceId":{"defaultValue":"` + parameterRegistryID + `"}}}`,
			`{"parameters":{}}`, "feature_not_enabled", nil},
		{"invalid expression", "", "", "ondisk_bicep_compile_failed", errors.New("invalid expression")},
		{"empty template", "", `{}`, "ondisk_bicep_parse_failed", nil},
		{"invalid template", "{", `{}`, "ondisk_bicep_parse_failed", nil},
		{"invalid parameters", `{}`, `{"parameters":[]}`, "ondisk_parameters_invalid", nil},
		{"invalid selector", `{}`, `{"parameters":{"existingAcrResourceId":{"value":true}}}`,
			"ondisk_parameters_invalid", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			testutil.ACRParameters(t, root, false, "bicepparam", "")
			compiler := &parameterCompiler{build: func(string, []string) (bicep.BuildResult, error) {
				envelope, err := json.Marshal(map[string]string{
					"templateJson": test.template, "parametersJson": test.parameters,
				})
				require.NoError(t, err)
				return bicep.BuildResult{Compiled: string(envelope)}, test.buildErr
			}}
			before := testutil.Snapshot(t, root)
			err := requireACRReuseForOnDiskInput(t.Context(), parameterInput(t, root), compiler)
			local, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok, "%v", err)
			require.Equal(t, test.code, local.Code)
			require.Equal(t, before, testutil.Snapshot(t, root))
		})
	}
}
