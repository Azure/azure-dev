// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package infra

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"azure.ai.content-understanding/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func choices() Choices {
	return Choices{Environment: "dev", Location: "eastus2", APIVersion: "2025-11-01",
		ModelSelection: "none", SubscriptionID: "sub", TenantID: "tenant"}
}

func TestCanonicalInfrastructureAssets(t *testing.T) {
	// Source asset hashes, with native hook updates for explicit environment binding.
	golden := map[string]string{
		"azure.yaml":                  "1d88ddc6e2fecf9e4ac03e277c401a7db6417c43dee3b4fc2d7b3a4a719f73d8",
		"hooks/postprovision.ps1":     "c52122fa5d528fc481c8d28a41ce652abad9c0562bfa707d4a8c83718cffead6",
		"hooks/postprovision.sh":      "8929a55610183fd0400af38463d7812a17c979a207de8d346068f721e07f59f8",
		"infra/main.bicep":            "b0482c22955e88706f9c4c5f709d553507627d7c8d39a94655c68bb3b66ade86",
		"infra/main.parameters.json":  "c6a46f4caa1468b7c04a14f4ad15fabeda362dc16043be9f94a4b4e7b91bcf79",
		"infra/models.json":           "37517e5f3dc66819f61f5a7bb8ace1921282415f10551d2defa5c3eb0985b570",
		"infra/modules/foundry.bicep": "18fd2b5d1ca12e75946ae317a08857f61fc5cdb05b329337036345521630c212",
	}
	directory := filepath.Join(testutil.TempDir(t), "project with spaces")
	result, err := Materialize(t.Context(), directory, choices(), false)
	require.NoError(t, err)
	assert.Len(t, result.Files, 10)
	assert.False(t, result.ReusedExistingTemplate)
	assert.Equal(t, []string{"cd '" + directory + "'", "azd auth login", "azd up"}, result.NextSteps)
	for relative, expected := range golden {
		body, err := os.ReadFile(filepath.Join(directory, relative)) // #nosec G304 -- Private test project.
		require.NoError(t, err)
		hash := sha256.Sum256(body)
		assert.Equal(t, expected, hex.EncodeToString(hash[:]), relative)
	}
	body, err := os.ReadFile(filepath.Join(directory, "README.md")) // #nosec G304 -- Private test project.
	require.NoError(t, err)
	assert.Contains(t, string(body), "native Go")
	assert.NotContains(t, string(body), "Runtime key injection")
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(directory, "hooks", "postprovision.sh"))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o111), info.Mode().Perm()&0o111)
	}
}

func TestGenerationReuseForceAndPreflight(t *testing.T) {
	directory := testutil.TempDir(t)
	_, err := Materialize(t.Context(), directory, choices(), false)
	require.NoError(t, err)
	write := func(path, body string) {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(directory, path), []byte(body), 0o600))
	}
	write(".azure/dev/.env", "# user\r\nCUSTOM=\"retained\"\r\n"+
		"CU_MODEL_SETUP_COMPLETE=\"true\"\r\nAZURE_LOCATION=\"old\"\r\n")
	write(".azure/config.json", `{"unknown":{"number":9007199254740993},"version":99}`)
	write("infra/models.json", `[{"name":"retained"}]`)
	write("azure.yaml", "customized")
	result, err := Materialize(t.Context(), directory, choices(), false)
	require.NoError(t, err)
	assert.True(t, result.ReusedExistingTemplate)
	assert.Equal(t, []string{".azure/config.json", ".azure/dev/.env"}, result.Files)
	body, err := os.ReadFile(filepath.Join(directory, ".azure/config.json")) // #nosec G304 -- Private test state.
	require.NoError(t, err)
	assert.JSONEq(t, `{"unknown":{"number":9007199254740993},"version":1,"defaultEnvironment":"dev"}`, string(body))
	assert.Contains(t, string(body), "9007199254740993")
	body, err = os.ReadFile(filepath.Join(directory, ".azure/dev/.env")) // #nosec G304 -- Private test state.
	require.NoError(t, err)
	assert.Contains(t, string(body), "CUSTOM=\"retained\"\r\n")
	assert.Contains(t, string(body), "CU_MODEL_SETUP_COMPLETE=\"true\"\r\n")
	assert.Contains(t, string(body), "AZURE_LOCATION=\"eastus2\"\r\n")
	body, err = os.ReadFile(filepath.Join(directory, "infra/models.json")) // #nosec G304 -- Private test state.
	require.NoError(t, err)
	assert.Equal(t, `[{"name":"retained"}]`, string(body))
	write(".azure/config.json", `[]`)
	_, err = Materialize(t.Context(), directory, choices(), false)
	require.Error(t, err)
	body, err = os.ReadFile(filepath.Join(directory, "azure.yaml")) // #nosec G304 -- Private test state.
	require.NoError(t, err)
	assert.Equal(t, "customized", string(body))
	result, err = Materialize(t.Context(), directory, choices(), true)
	require.NoError(t, err)
	assert.False(t, result.ReusedExistingTemplate)
	body, err = os.ReadFile(filepath.Join(directory, "infra/models.json")) // #nosec G304 -- Private test state.
	require.NoError(t, err)
	assert.Equal(t, "[]\n", string(body))
}

func TestGenerationInputAndPathSafety(t *testing.T) {
	for _, mutate := range []func(*Choices){
		func(c *Choices) { c.Environment = "../escape" },
		func(c *Choices) { c.Environment = "." },
		func(c *Choices) { c.Location = "invalid" },
		func(c *Choices) { c.FoundryPrefix = "-unsafe" },
		func(c *Choices) { c.FoundryPrefix = strings.Repeat("a", 21) },
		func(c *Choices) {
			c.FoundryPrefix, c.FoundryEndpoint = "prefix", "https://account.services.ai.azure.com"
		},
		func(c *Choices) { c.FoundryEndpoint = "http://account.services.ai.azure.com" },
		func(c *Choices) { c.FoundryEndpoint = "https://account.services.ai.azure.com" },
		func(c *Choices) {
			c.FoundryEndpoint, c.FoundryResourceGroup = "https://account.services.ai.azure.com", "existing-group"
			c.AssignRoles = true
		},
		func(c *Choices) { c.SubscriptionID = "\"$(touch injected)" },
		func(c *Choices) { c.TenantID = "" },
		func(c *Choices) { c.APIVersion = "invalid" },
		func(c *Choices) { c.ModelSelection = "none,model" },
		func(c *Choices) { c.ModelSelection = "model,,other" },
	} {
		c := choices()
		mutate(&c)
		target := filepath.Join(testutil.TempDir(t), "absent")
		_, err := Materialize(t.Context(), target, c, false)
		require.Error(t, err)
		_, err = os.Stat(target)
		assert.True(t, os.IsNotExist(err))
	}
	c := choices()
	c.Environment, c.Location, c.FoundryPrefix = "  DEV ", "East US 2", " New-Resource "
	normalized, err := c.Normalize()
	require.NoError(t, err)
	assert.Equal(t, "dev", normalized.Environment)
	assert.Equal(t, "eastus2", normalized.Location)
	assert.Equal(t, "new-resource", normalized.FoundryPrefix)
	c.FoundryPrefix, c.FoundryEndpoint = "", "https://ACCOUNT.services.ai.azure.com/"
	c.FoundryResourceGroup = "existing-group"
	normalized, err = c.Normalize()
	require.NoError(t, err)
	assert.Equal(t, "https://account.services.ai.azure.com", normalized.FoundryEndpoint)
	target := testutil.TempDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(target, "unrelated.txt"), []byte("keep"), 0o600))
	_, err = Materialize(t.Context(), target, choices(), false)
	require.Error(t, err)
	_, err = os.Stat(filepath.Join(target, "azure.yaml"))
	assert.True(t, os.IsNotExist(err))
	if runtime.GOOS != "windows" {
		other := testutil.TempDir(t)
		require.NoError(t, os.Symlink(other, filepath.Join(target, ".azure")))
		_, err = Materialize(t.Context(), target, choices(), true)
		require.Error(t, err)
		entries, err := os.ReadDir(other)
		require.NoError(t, err)
		assert.Empty(t, entries)
		_, err = os.Stat(filepath.Join(target, "azure.yaml"))
		assert.True(t, os.IsNotExist(err))
	}
}

func TestEnvironmentMergeDuplicateManagedValues(t *testing.T) {
	value := "# custom\nexport AZURE_LOCATION=old\nAZURE_LOCATION=duplicate\nCU_MODEL_SETUP_COMPLETE=true\n" +
		"CU_MODEL_SETUP_COMPLETE=false\nUNKNOWN=unchanged"
	result := MergeEnvironment(value, choices())
	assert.Equal(t, 1, strings.Count(result, "AZURE_LOCATION="))
	assert.Equal(t, 1, strings.Count(result, "CU_MODEL_SETUP_COMPLETE="))
	assert.Contains(t, result, "CU_MODEL_SETUP_COMPLETE=true\n")
	assert.Contains(t, result, "UNKNOWN=unchanged\n")
	assert.Contains(t, result, `AZD_ASSIGN_ROLES="false"`)
	assert.Equal(t, "'a'\"'\"'b'", shellQuote("a'b"))
}

func TestGeneratedShellLaunchers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process stub")
	}
	for _, shell := range []string{"sh", "pwsh"} {
		t.Run(shell, func(t *testing.T) {
			executable, err := exec.LookPath(shell)
			if err != nil {
				t.Skip("shell not installed")
			}
			for _, test := range []struct {
				name, exitCode, environment string
			}{
				{"success", "0", "prod"}, {"failure", "7", "prod"}, {"missing azd", "missing", "prod"},
				{"missing environment", "0", ""}, {"empty environment", "0", ""},
			} {
				t.Run(test.name, func(t *testing.T) {
					root := testutil.TempDir(t)
					project := filepath.Join(root, "project")
					_, err := Materialize(t.Context(), project, choices(), false)
					require.NoError(t, err)
					bin := filepath.Join(root, "bin")
					require.NoError(t, os.Mkdir(bin, 0o700))
					log := filepath.Join(root, "calls")
					if test.exitCode != "missing" {
						stub := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + shellQuote(log) + "\nexit " + test.exitCode + "\n"
						stubPath := filepath.Join(bin, "azd")
						require.NoError(t, os.WriteFile(stubPath, []byte(stub), 0o600))
						require.NoError(t, os.Chmod(stubPath, 0o700)) // #nosec G302 -- Executable test-owned CLI stub.
					}
					args := []string{filepath.Join(project, "hooks/postprovision.sh")}
					if shell == "pwsh" {
						args = []string{"-NoLogo", "-NoProfile", "-File", filepath.Join(project, "hooks/postprovision.ps1")}
					}
					command := exec.CommandContext(t.Context(), executable, args...) // #nosec G204 -- Test-owned script.
					command.Env = []string{"PATH=" + bin, "HOME=" + root, "AZD_ENVIRONMENT=dev"}
					if test.name != "missing environment" {
						command.Env = append(command.Env, "AZURE_ENV_NAME="+test.environment)
					}
					body, err := command.CombinedOutput()
					if test.environment == "" {
						require.Error(t, err)
						assert.Equal(t, 1, command.ProcessState.ExitCode(), string(body))
						assert.Contains(t, string(body), "Postprovision requires AZURE_ENV_NAME")
						assert.NoFileExists(t, log, "a missing hook environment must not fall back to dev")
						return
					}
					switch test.exitCode {
					case "0":
						require.NoError(t, err, string(body))
					case "7":
						require.Error(t, err)
						assert.Equal(t, 7, command.ProcessState.ExitCode(), string(body))
					default:
						require.Error(t, err)
						assert.Contains(t, string(body), "azd")
						assert.NoFileExists(t, log)
						return
					}
					body, err = os.ReadFile(log) // #nosec G304 -- Test-owned invocation log.
					require.NoError(t, err)
					assert.Equal(t, "ai\ncu\ninfra\n_postprovision-v1\n--environment\nprod\n", string(body))
				})
			}
		})
	}
}
