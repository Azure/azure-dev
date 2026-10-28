// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cli_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/stretchr/testify/require"
)

const petclinicRepository = "https://github.com/spring-projects/spring-petclinic"

type petclinicSourceManifest struct {
	Commit string            `json:"commit"`
	Files  map[string]string `json:"files"`
}

func Test_CLI_SpringPetclinic_FixtureContracts(t *testing.T) {
	verifyPetclinicTemplate(t, petclinicFixturePath(t, "overlay"))
	baselineYaml, err := os.ReadFile(filepath.Join(petclinicFixturePath(t, "baseline"), "azure.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(baselineYaml), "host: containerapp")
	require.Contains(t, string(baselineYaml), "preprovision:")
	require.NotContains(t, string(baselineYaml), "azure.springboot")
	for _, script := range []string{"preprovision.ps1", "preprovision.sh"} {
		content, err := os.ReadFile(filepath.Join(petclinicFixturePath(t, "baseline"), "scripts", script))
		require.NoError(t, err)
		text := string(content)
		require.Contains(t, text, "cannot be explicitly empty")
		require.Contains(t, text, "conflicts with the persisted value")
		require.Contains(t, text, "azd-env-name")
		require.Contains(t, text, "Microsoft.DBforPostgreSQL/flexibleServers")
		require.Contains(t, text, "Existing infrastructure has no local password")
		require.Greater(
			t,
			strings.LastIndex(text, "azd env set SPRING_DATABASE_TYPE"),
			strings.Index(text, "cannot be explicitly empty"),
			"database mode must not be persisted before password validation in %s",
			script,
		)
	}
}

func verifyPetclinicTemplate(t *testing.T, projectDir string) {
	t.Helper()
	finalYaml, err := os.ReadFile(filepath.Join(projectDir, "azure.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(finalYaml), "host: azure.springboot")
	require.Contains(t, string(finalYaml), "type: ${SPRING_DATABASE_TYPE:-h2}")
	require.NotContains(t, string(finalYaml), "docker:")
	require.NotContains(t, string(finalYaml), "hooks:")

	for _, file := range []string{"main.parameters.json", "petclinic.parameters.json"} {
		content, err := os.ReadFile(filepath.Join(projectDir, "infra", file))
		require.NoError(t, err)
		var parameters struct {
			Parameters map[string]struct {
				Value string `json:"value"`
			} `json:"parameters"`
		}
		require.NoError(t, json.Unmarshal(content, &parameters))
		require.Equal(t, "${SPRING_DATABASE_TYPE:-h2}", parameters.Parameters["databaseType"].Value)
		require.Equal(t, "${SPRING_DATABASE_PASSWORD:-}", parameters.Parameters["databasePassword"].Value)
	}

	serviceBicep, err := os.ReadFile(filepath.Join(projectDir, "infra", "petclinic.bicep"))
	require.NoError(t, err)
	serviceText := strings.ReplaceAll(string(serviceBicep), "\r\n", "\n")
	require.Contains(t, serviceText, "@secure()\nparam databasePassword")
	require.Contains(t, serviceText, "sslmode=verify-full")
	require.Contains(t, serviceText, "name: 'MANAGEMENT_ENDPOINT_HEALTH_GROUP_READINESS_INCLUDE'")
	require.Contains(t, serviceText, "value: 'readinessState,db'")
	require.NotContains(t, serviceText, "MANAGEMENT_ENDPOINT_HEALTH_GROUP_LIVENESS_INCLUDE")
	require.Contains(t, serviceText, "secretRef: 'database-password'")
	require.Contains(t, serviceText, "maxReplicas: 1")

	rootBicep, err := os.ReadFile(filepath.Join(projectDir, "infra", "resources.bicep"))
	require.NoError(t, err)
	rootText := strings.ReplaceAll(string(rootBicep), "\r\n", "\n")
	require.Contains(t, rootText, "name: 'MANAGEMENT_ENDPOINT_HEALTH_GROUP_READINESS_INCLUDE'")
	require.Contains(t, rootText, "value: 'readinessState,db'")
	require.NotContains(t, rootText, "MANAGEMENT_ENDPOINT_HEALTH_GROUP_LIVENESS_INCLUDE")
}

func Test_CLI_SpringExtension_BundleInstallPreservesBaseTarget(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()

	installSpringExtensionFixture(t, ctx, tempDirWithDiagnostics(t))
}

func Test_CLI_SpringPetclinic_InitFromExactUpstreamURL(t *testing.T) {
	if os.Getenv("AZD_TEST_SPRING_PETCLINIC_INIT") != "1" {
		t.Skip("set AZD_TEST_SPRING_PETCLINIC_INIT=1 to run the network-dependent upstream init test")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	configDir := tempDirWithDiagnostics(t)
	installSpringExtensionFixture(t, ctx, configDir)
	for _, mode := range []string{"h2", "postgresql"} {
		t.Run(mode, func(t *testing.T) {
			cli := initPetclinicTemplate(t, ctx, configDir, "petclinic-"+mode, mode)
			if os.Getenv("AZD_TEST_SPRING_PETCLINIC_BUILD") == "1" {
				build, err := cli.RunCommand(ctx, "build", "--no-prompt")
				require.NoError(t, err)
				dockerfile, err := os.ReadFile(filepath.Join(cli.WorkingDirectory, "Dockerfile"))
				require.NoError(t, err)
				require.Contains(t, string(dockerfile), "Generated by azure.springboot")
				require.Contains(t, string(dockerfile), "USER spring:spring")
				require.NotContains(t, strings.ToLower(build.Stdout+build.Stderr), "buildpack")
			}
		})
	}
}

func initPetclinicTemplate(
	t *testing.T,
	ctx context.Context,
	configDir, envName, mode string,
	extraEnv ...string,
) *azdcli.CLI {
	t.Helper()
	projectDir := tempDirWithDiagnostics(t)
	require.NoError(t, os.CopyFS(projectDir, os.DirFS(petclinicFixturePath(t, "overlay"))))
	preserved := map[string][]byte{}
	for _, relative := range []string{
		".dockerignore",
		"README.fixture.md",
		"source-revision.json",
		"azure.yaml",
		filepath.Join("infra", "main.bicep"),
		filepath.Join("infra", "main.parameters.json"),
		filepath.Join("infra", "resources.bicep"),
		filepath.Join("infra", "petclinic.bicep"),
		filepath.Join("infra", "petclinic.parameters.json"),
	} {
		data, err := os.ReadFile(filepath.Join(projectDir, relative))
		require.NoError(t, err)
		preserved[relative] = data
	}
	cli := newSpringCLI(t)
	cli.WorkingDirectory = projectDir
	cli.Env = append(environmentWithout(
		"AZD_ALLOW_NON_EMPTY_FOLDER",
		"SPRING_DATABASE_TYPE",
		"SPRING_DATABASE_PASSWORD",
	), cli.Env...)
	cli.Env = append(cli.Env, "AZD_CONFIG_DIR="+configDir, "AZD_ALLOW_NON_EMPTY_FOLDER=1")
	cli.Env = append(cli.Env, extraEnv...)
	_, err := cli.RunCommand(
		ctx, "init", "-t", petclinicRepository,
		".", "-e", envName, "--no-prompt",
	)
	require.NoError(t, err)
	for relative, expected := range preserved {
		actual, err := os.ReadFile(filepath.Join(projectDir, relative))
		require.NoError(t, err)
		require.Equal(t, expected, actual, "%s changed during init", relative)
	}
	require.FileExists(t, filepath.Join(projectDir, "pom.xml"))
	require.FileExists(t, filepath.Join(projectDir, "mvnw"))
	require.FileExists(t, filepath.Join(projectDir, "LICENSE.txt"))
	require.FileExists(t, filepath.Join(projectDir, "README.fixture.md"))
	require.DirExists(t, filepath.Join(projectDir, ".azure", envName))
	verifyPetclinicTemplate(t, projectDir)
	verifyPetclinicSourceManifest(t, projectDir)
	_, err = cli.RunCommand(ctx, "env", "set", "SPRING_DATABASE_TYPE", mode)
	require.NoError(t, err)
	return cli
}

func environmentWithout(keys ...string) []string {
	result := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if slices.ContainsFunc(keys, func(key string) bool {
			return strings.EqualFold(name, key)
		}) {
			continue
		}
		result = append(result, entry)
	}
	return result
}

func installSpringExtensionFixture(t *testing.T, ctx context.Context, configDir string) {
	t.Helper()
	extensionSourceDir := filepath.Join(azdSourcePath(t), "extensions", "azure.springboot")
	extensionDir := tempDirWithDiagnostics(t)
	require.NoError(t, os.CopyFS(extensionDir, extensionSourceFS{os.DirFS(extensionSourceDir)}))
	bundlePath := filepath.Join(tempDirWithDiagnostics(t), "azure.springboot_0.1.0.zip")

	cli := newSpringCLI(t)
	cli.WorkingDirectory = extensionDir
	cli.Env = append(os.Environ(), cli.Env...)
	cli.Env = append(
		cli.Env,
		"AZD_CONFIG_DIR="+configDir,
		"AZURE_DEV_COLLECT_TELEMETRY=no",
		"NO_COLOR=1",
		"PATH="+filepath.Dir(cli.AzdPath)+string(os.PathListSeparator)+os.Getenv("PATH"),
	)

	installExtensionDeveloperToolFastStub(t, ctx, configDir)

	_, err := cli.RunCommand(
		ctx,
		"x", "build",
		"--skip-install",
	)
	require.NoError(t, err)

	_, err = cli.RunCommand(
		ctx,
		"x", "pack",
		"--bundle",
		"--output", bundlePath,
	)
	require.NoError(t, err)
	require.FileExists(t, bundlePath)

	_, err = cli.RunCommand(ctx, "extension", "install", bundlePath)
	require.NoError(t, err)

	var config struct {
		Extension struct {
			Installed map[string]*struct {
				Source    string `json:"source"`
				Providers []struct {
					BaseTarget *string `json:"baseTarget"`
				} `json:"providers"`
			} `json:"installed"`
		} `json:"extension"`
	}
	configPath := filepath.Join(configDir, "config.json")
	content, err := os.ReadFile(configPath)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(content, &config))
	installed, ok := config.Extension.Installed["azure.springboot"]
	require.True(t, ok, "bundle-installed extension missing from config")
	require.NotNil(t, installed)
	require.Equal(t, "bundle", installed.Source)
	require.Len(t, installed.Providers, 1)
	require.NotNil(t, installed.Providers[0].BaseTarget)
	require.Equal(t, "containerapp", *installed.Providers[0].BaseTarget)
}

func installExtensionDeveloperToolFastStub(t *testing.T, ctx context.Context, configDir string) {
	t.Helper()
	binaryName := "microsoft-azd-extensions"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	relativePath := filepath.Join("extensions", "microsoft.azd.extensions", binaryName)
	absolutePath := filepath.Join(configDir, relativePath)
	require.NoError(t, os.MkdirAll(filepath.Dir(absolutePath), 0o755))

	build := osexec.CommandContext(ctx, "go", "build", "-o", absolutePath, ".")
	build.Dir = filepath.Join(azdSourcePath(t), "extensions", "microsoft.azd.extensions")
	output, err := build.CombinedOutput()
	require.NoError(t, err, "building developer extension fast stub: %s", output)

	config := map[string]any{}
	configPath := filepath.Join(configDir, "config.json")
	if content, err := os.ReadFile(configPath); err == nil {
		require.NoError(t, json.Unmarshal(content, &config))
	} else {
		require.ErrorIs(t, err, os.ErrNotExist)
	}
	extensionSection, _ := config["extension"].(map[string]any)
	if extensionSection == nil {
		extensionSection = map[string]any{}
		config["extension"] = extensionSection
	}
	installed, _ := extensionSection["installed"].(map[string]any)
	if installed == nil {
		installed = map[string]any{}
		extensionSection["installed"] = installed
	}
	installed["microsoft.azd.extensions"] = &extensions.Extension{
		Id:          "microsoft.azd.extensions",
		Namespace:   "x",
		Version:     "0.14.0",
		Path:        relativePath,
		DisplayName: "azd extensions Developer Kit",
		Description: "Local functional-test developer tooling.",
		Usage:       "azd x <command> [options]",
		Capabilities: []extensions.CapabilityType{
			extensions.CustomCommandCapability,
			extensions.MetadataCapability,
		},
	}
	content, err := json.Marshal(config)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(configPath, content, 0o600))
}

func verifyPetclinicSourceManifest(t *testing.T, projectDir string) {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(projectDir, "source-revision.json"))
	require.NoError(t, err)
	var manifest petclinicSourceManifest
	require.NoError(t, json.Unmarshal(content, &manifest))
	require.Equal(t, "500158f732419217507c7656904b8e6aa1bcc0d6", manifest.Commit)

	for relative, expected := range manifest.Files {
		content, err := os.ReadFile(filepath.Join(projectDir, filepath.FromSlash(relative)))
		require.NoError(t, err, relative)
		content = bytes.ReplaceAll(content, []byte("\r\n"), []byte("\n"))
		sum := sha256.Sum256(content)
		actual := strings.ToLower(hex.EncodeToString(sum[:]))
		require.Equal(t, expected, actual, "upstream source drifted at %s", relative)
	}
}
