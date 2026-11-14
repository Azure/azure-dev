// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cli_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/stretchr/testify/require"
)

const (
	petclinicRepository              = "https://github.com/XiaofeiCao/azd-spring-petclinic"
	petclinicBranch                  = "main"
	petclinicTemplateCommit          = "215d42251c4960c67bd86167a0a67ef8a24cf3ff"
	proposedTemplateRepo             = "https://github.com/XiaofeiCao/petclinic-rest-azd"
	proposedTemplateCommit           = "6110dca64d6bbaed2998faa92cfb5941b725a849"
	proposedTemplateAdminNameBinding = "${DATABASE_ADMIN_NAME=azd-preprovision-pending}"
	proposedTemplateAdminIDBinding   = "${DATABASE_ADMIN_OBJECT_ID=00000000-0000-0000-0000-000000000000}"
	gitMetadataAccommodation         = "Packaging Maven project without Git commit metadata because the repository has no commits."
)

type petclinicSourceManifest struct {
	Repository    string            `json:"repository"`
	Commit        string            `json:"commit"`
	HashAlgorithm string            `json:"hashAlgorithm"`
	Files         map[string]string `json:"files"`
}

func verifyPetclinicTemplate(t *testing.T, projectDir string) {
	t.Helper()
	finalYaml, err := os.ReadFile(filepath.Join(projectDir, "azure.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(finalYaml), "host: springbootapp")
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

func verifyProposedPetclinicTemplate(t *testing.T, projectDir string) {
	t.Helper()
	finalYaml, err := os.ReadFile(filepath.Join(projectDir, "azure.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(finalYaml), "host: springbootapp")
	require.NotContains(t, string(finalYaml), "database:")
	require.NotContains(t, string(finalYaml), "docker:")
	require.NotContains(t, string(finalYaml), "hooks:")

	mainParameters, err := os.ReadFile(filepath.Join(projectDir, "infra", "main.parameters.json"))
	require.NoError(t, err)
	var parameters struct {
		Parameters map[string]struct {
			Value string `json:"value"`
		} `json:"parameters"`
	}
	require.NoError(t, json.Unmarshal(mainParameters, &parameters))
	require.Equal(t, "${AZURE_USE_VNET=false}", parameters.Parameters["useVirtualNetwork"].Value)
	require.Equal(t, proposedTemplateAdminIDBinding,
		parameters.Parameters["databaseAdministratorObjectId"].Value)
	require.Equal(t, proposedTemplateAdminNameBinding,
		parameters.Parameters["databaseAdministratorName"].Value)

	mainBicep, err := os.ReadFile(filepath.Join(projectDir, "infra", "main.bicep"))
	require.NoError(t, err)
	mainText := strings.ReplaceAll(string(mainBicep), "\r\n", "\n")
	require.Contains(t, mainText, "param useVirtualNetwork bool = false")
	require.Contains(t, mainText, "principalId: bootstrap.outputs.principalId")
	require.Contains(t, mainText, "dependsOn: [registryAccess]")
	require.Contains(t, mainText, "output APPLICATION_PRINCIPAL_ID string = application.outputs.principalId")

	bootstrapBicep, err := os.ReadFile(filepath.Join(projectDir, "infra", "bootstrap.bicep"))
	require.NoError(t, err)
	bootstrapText := strings.ReplaceAll(string(bootstrapBicep), "\r\n", "\n")
	require.Contains(t, bootstrapText, "@onlyIfNotExists()")
	require.Contains(t, bootstrapText, "type: 'SystemAssigned'")
	require.Contains(t, bootstrapText, "image: 'mcr.microsoft.com/azuredocs/containerapps-helloworld:latest'")
	require.Contains(t, bootstrapText, "output principalId string = app.identity.principalId")
	require.Contains(t, bootstrapText, "output image string = app.properties.template.containers[0].image")

	registryAccessBicep, err := os.ReadFile(filepath.Join(projectDir, "infra", "registry-access.bicep"))
	require.NoError(t, err)
	require.Contains(t, string(registryAccessBicep), "scope: registry")
	require.Contains(t, string(registryAccessBicep), "'7f951dda-4ed3-4680-a7ca-43fe172d538d'")

	applicationParameters, err := os.ReadFile(filepath.Join(projectDir, "infra", "application.parameters.json"))
	require.NoError(t, err)
	require.Contains(t, string(applicationParameters), "${SERVICE_SPRING_PETCLINIC_REST_APP_IMAGE_NAME}")

	applicationBicep, err := os.ReadFile(filepath.Join(projectDir, "infra", "application.bicep"))
	require.NoError(t, err)
	applicationText := strings.ReplaceAll(string(applicationBicep), "\r\n", "\n")
	require.Contains(t, applicationText, "sslmode=verify-full")
	require.Contains(t, applicationText, "azure.managedIdentityEnabled=true")
	require.Contains(t, applicationText, "name: 'SPRING_PROFILES_ACTIVE', value: 'azure-postgres,spring-data-jpa'")
	require.Contains(t, applicationText, "type: 'SystemAssigned'")
	require.Contains(t, applicationText, "registries: [{ server: registryEndpoint, identity: 'system' }]")
	require.NotContains(t, applicationText, "SPRING_DATASOURCE_PASSWORD")

	for file, content := range map[string]string{
		"main.bicep":        mainText,
		"bootstrap.bicep":   bootstrapText,
		"application.bicep": applicationText,
	} {
		for _, legacy := range []string{
			"UserAssigned", "userAssignedIdentities", "CLIENT_ID", "azure.clientId", "identityClientId", "identityId",
		} {
			require.NotContains(t, content, legacy, "%s must not retain a user-identity selector", file)
		}
	}

	azureProfile, err := os.ReadFile(
		filepath.Join(projectDir, "src", "main", "resources", "application-azure-postgres.properties"))
	require.NoError(t, err)
	require.NotContains(t, string(azureProfile), "spring.datasource.password")

	localProfile, err := os.ReadFile(
		filepath.Join(projectDir, "src", "main", "resources", "application-postgres.properties"))
	require.NoError(t, err)
	require.Contains(t, string(localProfile), "spring.datasource.password")

	databaseBicep, err := os.ReadFile(filepath.Join(projectDir, "infra", "database.bicep"))
	require.NoError(t, err)
	databaseText := strings.ReplaceAll(string(databaseBicep), "\r\n", "\n")
	require.Contains(t, databaseText, "passwordAuth: 'Disabled'")
	require.Contains(t, databaseText, "activeDirectoryAuth: 'Enabled'")
	require.Contains(t, databaseText, "name: 'require_secure_transport'\n  dependsOn: [database]")
	require.Contains(t, databaseText, "@batchSize(1)\nresource firewall ")
	require.Contains(t, databaseText, "dependsOn: [requireTls]")
	require.Contains(t, databaseText, "dependsOn: [firewall]")
	require.Contains(t, databaseText, "name: 'aca-${replace(ip, '.', '-')}'")
	require.NotContains(t, databaseText, "bootstrap-local-ip")
	require.NotContains(t, databaseText, "startIpAddress: '0.0.0.0'")
	require.NotContains(t, databaseText, "endIpAddress: '255.255.255.255'")

	for _, path := range []string{
		"Configure-Database.ps1",
		"Initialize-DatabaseIdentity.ps1",
		"Prepare-Database.ps1",
		"Sync-DatabaseFirewall.ps1",
		"Update-DatabaseFirewall.ps1",
		"database-identity.sql",
	} {
		_, err := os.Stat(filepath.Join(projectDir, "scripts", path))
		require.ErrorIs(t, err, os.ErrNotExist)
	}
}

func Test_CLI_SpringExtension_BundleInstallsWithoutBaseTarget(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()

	installSpringExtensionFixture(t, ctx, tempDirWithDiagnostics(t))
}

func Test_CLI_SpringPetclinic_StagedPackage(t *testing.T) {
	if os.Getenv("AZD_TEST_SPRING_PETCLINIC_PACKAGE") != "1" {
		t.Skip("set AZD_TEST_SPRING_PETCLINIC_PACKAGE=1 to run the stock-CLI container package gate")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Minute)
	defer cancel()
	configDir := tempDirWithDiagnostics(t)
	installSpringExtensionFixture(t, ctx, configDir)
	projectDir := stagePetclinicTemplate(t, ctx)
	envName := "petclinic-auto-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	referencePrefix := "petclinic-rest/spring-petclinic-rest-app-" + envName + ":"
	require.Empty(t, nativeImageTags(t, ctx, referencePrefix))
	registerNativeImageCleanup(t, referencePrefix)
	cli := initAuthoredPetclinicProject(t, ctx, configDir, projectDir, envName, "automatic")
	result, err := cli.RunCommand(ctx, "package", "--no-prompt")
	require.NoError(t, err, result.Stdout+result.Stderr)
	require.NotContains(t, result.Stdout+result.Stderr, gitMetadataAccommodation)

	require.FileExists(t, filepath.Join(cli.WorkingDirectory, "Dockerfile"))
	require.NoDirExists(t, filepath.Join(cli.WorkingDirectory, ".azd-springboot"))
	require.NotContains(t, strings.ToLower(result.Stdout+result.Stderr), "buildpack")
	image := verifyProposedPackagedImage(t, ctx, envName)
	verifyH2ContainerStartupAt(t, ctx, image, "9966", "/petclinic/actuator/health")
}

func Test_CLI_SpringPetclinic_PublishedTemplatePackage(t *testing.T) {
	if os.Getenv("AZD_TEST_SPRING_PETCLINIC_REMOTE") != "1" {
		t.Skip("set AZD_TEST_SPRING_PETCLINIC_REMOTE=1 to run the published-template package gate")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Minute)
	defer cancel()
	configDir := tempDirWithDiagnostics(t)
	installSpringExtensionFixture(t, ctx, configDir)
	for _, mode := range []string{"h2", "postgresql"} {
		t.Run(mode, func(t *testing.T) {
			envName := "petclinic-remote-" + mode + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
			referencePrefix := "spring-petclinic/petclinic-" + envName + ":"
			require.Empty(t, nativeImageTags(t, ctx, referencePrefix))
			registerNativeImageCleanup(t, referencePrefix)
			cli := initPetclinicTemplate(t, ctx, configDir, envName, mode)
			verifyUnbornGitHead(t, ctx, cli.WorkingDirectory)
			result, err := cli.RunCommand(ctx, "package", "--no-prompt")
			require.NoError(t, err, result.Stdout+result.Stderr)
			require.Contains(t, result.Stdout+result.Stderr, gitMetadataAccommodation)

			image := verifyPackagedImage(t, ctx, envName)
			require.NotEmpty(t, image)
			require.NoDirExists(t, filepath.Join(cli.WorkingDirectory, ".azd-springboot"))
		})
	}
}

func verifyUnbornGitHead(t *testing.T, ctx context.Context, projectDir string) {
	t.Helper()
	command := osexec.CommandContext(
		ctx,
		"git",
		"--no-optional-locks",
		"status",
		"--porcelain=v2",
		"--branch",
		"--untracked-files=no",
	)
	command.Dir = projectDir
	command.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	require.Contains(t, strings.ReplaceAll(string(output), "\r\n", "\n"), "# branch.oid (initial)\n")
}

func nativeImageTags(t *testing.T, ctx context.Context, referencePrefix string) []string {
	t.Helper()
	// #nosec G204 -- callers supply an exact nonce-scoped image prefix as an argument, never shell syntax.
	command := osexec.CommandContext(ctx, "docker", "image", "ls", "--filter",
		"reference="+referencePrefix+"*", "--format", "{{.Repository}}:{{.Tag}}")
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	tags := strings.Fields(string(output))
	for _, tag := range tags {
		require.True(t, strings.HasPrefix(tag, referencePrefix), "refuse an image outside this test's exact scope")
	}
	return tags
}

func registerNativeImageCleanup(t *testing.T, referencePrefix string) {
	t.Helper()
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		for _, image := range nativeImageTags(t, cleanupCtx, referencePrefix) {
			// #nosec G204 -- nativeImageTags verifies every tag against this test's exact nonce-scoped prefix.
			output, err := osexec.CommandContext(
				cleanupCtx, "docker", "image", "rm", "--no-prune", image,
			).CombinedOutput()
			if err != nil {
				t.Errorf("removing this trial's exact image tag: %v: %s", err, output)
			}
		}
	})
}

func initAuthoredPetclinicProject(
	t *testing.T,
	ctx context.Context,
	configDir, projectDir, envName, _ string,
) *azdcli.CLI {
	t.Helper()
	cli := newSpringCLI(t)
	cli.WorkingDirectory = projectDir
	cli.Env = environmentWithout(cli.Env,
		"AZD_ALLOW_NON_EMPTY_FOLDER",
		"SPRING_DATABASE_TYPE",
		"SPRING_DATABASE_PASSWORD",
		"AZD_CONFIG_DIR",
	)
	cli.Env = append(cli.Env, "AZD_CONFIG_DIR="+configDir)
	_, err := cli.RunCommand(ctx, "init", "-e", envName, "--no-prompt")
	require.NoError(t, err)
	require.DirExists(t, filepath.Join(projectDir, ".azure", envName))
	verifyProposedPetclinicTemplate(t, projectDir)
	return cli
}

func initPetclinicTemplate(
	t *testing.T,
	ctx context.Context,
	configDir, envName, mode string,
	extraEnv ...string,
) *azdcli.CLI {
	t.Helper()
	verifyPetclinicBranch(t, ctx)
	cli := initPetclinicTemplateWithArgs(
		t,
		ctx,
		configDir,
		envName,
		mode,
		[]string{
			"init", "-t", petclinicRepository, "--branch", petclinicBranch,
			".", "-e", envName, "--no-prompt",
		},
		extraEnv...,
	)
	verifyPetclinicBranch(t, ctx)
	return cli
}

func initPetclinicTemplateWithArgs(
	t *testing.T,
	ctx context.Context,
	configDir, envName, mode string,
	initArgs []string,
	extraEnv ...string,
) *azdcli.CLI {
	t.Helper()
	projectDir := tempDirWithDiagnostics(t)
	repositoryRoot := filepath.Clean(filepath.Join(azdSourcePath(t), "..", ".."))
	if strings.EqualFold(filepath.VolumeName(repositoryRoot), filepath.VolumeName(projectDir)) {
		relative, err := filepath.Rel(repositoryRoot, projectDir)
		require.NoError(t, err)
		require.False(t, filepath.IsLocal(relative), "Petclinic must be initialized outside Azure Dev")
	}
	directory, err := os.Open(projectDir)
	require.NoError(t, err)
	_, err = directory.Readdirnames(1)
	require.ErrorIs(t, err, io.EOF, "template initialization must start in an empty directory")
	require.NoError(t, directory.Close())
	cli := newSpringCLI(t)
	cli.WorkingDirectory = projectDir
	cli.Env = environmentWithout(cli.Env,
		"AZD_ALLOW_NON_EMPTY_FOLDER",
		"SPRING_DATABASE_TYPE",
		"SPRING_DATABASE_PASSWORD",
		"AZD_CONFIG_DIR",
	)
	cli.Env = append(cli.Env, "AZD_CONFIG_DIR="+configDir)
	cli.Env = append(cli.Env, extraEnv...)
	_, err = cli.RunCommand(ctx, initArgs...)
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(projectDir, "pom.xml"))
	require.FileExists(t, filepath.Join(projectDir, "mvnw"))
	require.FileExists(t, filepath.Join(projectDir, "LICENSE.txt"))
	require.FileExists(t, filepath.Join(projectDir, "README.md"))
	require.FileExists(t, filepath.Join(projectDir, ".dockerignore"))
	require.NoFileExists(t, filepath.Join(projectDir, "Dockerfile"))
	require.DirExists(t, filepath.Join(projectDir, ".azure", envName))
	verifyPetclinicTemplate(t, projectDir)
	verifyPetclinicSourceManifest(t, projectDir)
	_, err = cli.RunCommand(ctx, "env", "set", "SPRING_DATABASE_TYPE", mode)
	require.NoError(t, err)
	return cli
}

func stagePetclinicTemplate(t *testing.T, ctx context.Context) string {
	t.Helper()
	templateDir := tempDirWithDiagnostics(t)
	parent := filepath.Dir(templateDir)
	require.NoError(t, os.Remove(templateDir))

	// #nosec G204 -- fixed upstream URL/ref and argument-vector execution into a fresh test-owned destination.
	command := osexec.CommandContext(
		ctx,
		"git", "clone", "--quiet", proposedTemplateRepo, templateDir,
	)
	output, err := command.CombinedOutput()
	require.NoError(t, err, "cloning the pinned Petclinic template: %s", output)
	require.True(t, filepath.IsAbs(parent))

	// #nosec G204 -- fixed git arguments and test-owned clone, without shell interpolation.
	checkout := osexec.CommandContext(ctx, "git", "-C", templateDir, "checkout", "--quiet", proposedTemplateCommit)
	output, err = checkout.CombinedOutput()
	require.NoError(t, err, "checking out the proposed Petclinic revision: %s", output)

	// #nosec G204 -- fixed git arguments and test-owned clone, without shell interpolation.
	head := osexec.CommandContext(ctx, "git", "-C", templateDir, "rev-parse", "HEAD")
	output, err = head.CombinedOutput()
	require.NoError(t, err, "reading the staged Petclinic commit: %s", output)
	require.Equal(t, proposedTemplateCommit, strings.TrimSpace(string(output)))

	azureYamlPath := filepath.Join(templateDir, "azure.yaml")
	content, err := os.ReadFile(azureYamlPath)
	require.NoError(t, err)
	const newRequirement = `azd: ">=1.37.2"`
	require.Equal(t, 1, bytes.Count(content, []byte(newRequirement)))

	// #nosec G204 -- fixed git arguments and verified fixture directory, without shell interpolation.
	status := osexec.CommandContext(ctx, "git", "-C", templateDir, "status", "--short")
	output, err = status.CombinedOutput()
	require.NoError(t, err, "checking staged Petclinic changes: %s", output)
	require.Empty(t, strings.TrimSpace(string(output)))
	return templateDir
}

func verifyPackagedImage(t *testing.T, ctx context.Context, envName string) string {
	t.Helper()
	reference := "spring-petclinic/petclinic-" + envName + ":*"
	// #nosec G204 -- inventory-only Docker arguments; reference is a generated tag filter, not shell input.
	command := osexec.CommandContext(
		ctx,
		"docker",
		"image",
		"ls",
		"--format",
		"{{.Repository}}:{{.Tag}}",
		"--filter",
		"reference="+reference,
	)
	output, err := command.CombinedOutput()
	require.NoError(t, err, "listing packaged Spring image: %s", output)
	images := strings.Fields(string(output))
	require.NotEmpty(t, images, "expected a packaged container image matching %s", reference)
	return images[0]
}

func verifyProposedPackagedImage(t *testing.T, ctx context.Context, envName string) string {
	t.Helper()
	reference := "petclinic-rest/spring-petclinic-rest-app-" + envName + ":*"
	// #nosec G204 -- inventory-only Docker arguments; reference is a generated tag filter, not shell input.
	command := osexec.CommandContext(
		ctx,
		"docker",
		"image",
		"ls",
		"--format",
		"{{.Repository}}:{{.Tag}}",
		"--filter",
		"reference="+reference,
	)
	output, err := command.CombinedOutput()
	require.NoError(t, err, "listing packaged Spring image: %s", output)
	images := strings.Fields(string(output))
	require.NotEmpty(t, images, "expected a packaged container image matching %s", reference)
	return images[0]
}

func verifyPetclinicBranch(t *testing.T, ctx context.Context) {
	t.Helper()
	ref := "refs/heads/" + petclinicBranch
	command := osexec.CommandContext(ctx, "git", "ls-remote", "--exit-code", petclinicRepository, ref)
	output, err := command.CombinedOutput()
	require.NoError(t, err, "reading the Petclinic testing branch: %s", output)
	fields := strings.Fields(string(output))
	require.Len(t, fields, 2, "expected exactly one published Petclinic branch ref")
	require.Equal(t, ref, fields[1])
	require.Equal(t, petclinicTemplateCommit, fields[0],
		"published Petclinic branch drifted; review its changes before updating the template revision pin")
}

func environmentWithout(environment []string, keys ...string) []string {
	result := make([]string, 0, len(environment))
	for _, entry := range environment {
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
	evidenceDir := retainSpringAcceptanceSource(t, extensionDir)
	bundlePath := filepath.Join(tempDirWithDiagnostics(t), "azure.springboot_0.1.0.zip")

	cli := newSpringCLI(t)
	cli.WorkingDirectory = extensionDir
	cli.Env = environmentWithout(cli.Env, "AZD_CONFIG_DIR")
	cli.Env = append(cli.Env, "AZD_CONFIG_DIR="+configDir)

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
	if evidenceDir != "" {
		source, err := os.Open(bundlePath)
		require.NoError(t, err)
		// #nosec G703 -- fixed bundle name in the fresh private evidence directory created below.
		target, err := os.OpenFile(filepath.Join(evidenceDir, "bundle.zip"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			require.NoError(t, source.Close())
			require.NoError(t, err)
		}
		_, copyErr := io.Copy(target, source)
		require.NoError(t, errors.Join(copyErr, source.Close(), target.Close()))
	}

	_, err = cli.RunCommand(ctx, "extension", "install", bundlePath)
	require.NoError(t, err)

	var config struct {
		Extension struct {
			Installed map[string]*struct {
				Source    string `json:"source"`
				Providers []struct {
					Name       string  `json:"name"`
					Type       string  `json:"type"`
					BaseTarget *string `json:"baseTarget"`
				} `json:"providers"`
			} `json:"installed"`
		} `json:"extension"`
	}

	configPath := filepath.Join(configDir, "config.json")
	// #nosec G703 -- fixed config.json under the isolated azd test configuration directory.
	content, err := os.ReadFile(configPath)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(content, &config))
	installed, ok := config.Extension.Installed["azure.springboot"]
	require.True(t, ok, "bundle-installed extension missing from config")
	require.NotNil(t, installed)
	require.Equal(t, "bundle", installed.Source)
	require.Len(t, installed.Providers, 1)
	require.Equal(t, "springbootapp", installed.Providers[0].Name)
	require.Equal(t, "service-target", installed.Providers[0].Type)
	require.Nil(t, installed.Providers[0].BaseTarget)
}

func retainSpringAcceptanceSource(t *testing.T, source string) string {
	t.Helper()
	root := os.Getenv("AZD_TEST_SPRING_ARTIFACTS_DIR")
	if root == "" {
		return ""
	}
	require.True(t, filepath.IsAbs(root), "acceptance evidence must use an explicitly selected absolute directory")
	directory, err := os.MkdirTemp(root, "spring-native-")
	require.NoError(t, err)
	// #nosec G703 -- fixed source subdirectory under a fresh private os.MkdirTemp evidence directory.
	require.NoError(t, os.CopyFS(filepath.Join(directory, "source"), extensionSourceFS{os.DirFS(source)}))
	// #nosec G703 -- fixed receipt filename under that same fresh private evidence directory.
	require.NoError(t, os.WriteFile(filepath.Join(directory, "test-name.txt"), []byte(t.Name()+"\n"), 0o600))
	t.Logf("retained exact acceptance build inputs and bundle: %s", directory)
	return directory
}

func verifyH2ContainerStartupAt(
	t *testing.T,
	ctx context.Context,
	image, containerPort, healthPath string,
) {
	t.Helper()
	name := "azd-springboot-h2-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	const ownerLabel = "azd.spring.h2.owner"
	dockerEnvironment := append(environmentWithout(os.Environ(), "DOCKER_CONFIG"),
		"DOCKER_CONFIG="+tempDirWithDiagnostics(t))
	// #nosec G204 -- verified packaged image and generated ownership/name arguments, without shell interpolation.
	run := osexec.CommandContext(
		ctx,
		"docker",
		"run",
		"--detach",
		"--name",
		name,
		"--label",
		ownerLabel+"="+name,
		"--publish",
		"127.0.0.1::"+containerPort,
		image,
		"--management.endpoint.health.show-details=always",
	)
	run.Env = dockerEnvironment
	output, err := run.CombinedOutput()
	require.NoError(t, err, "starting packaged H2 container: %s", output)
	containerID := strings.TrimSpace(string(output))
	require.Len(t, containerID, 64)
	_, err = hex.DecodeString(containerID)
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		// #nosec G204 -- validated 64-character hexadecimal Docker ID; this read verifies its exact ownership label.
		inspect := osexec.CommandContext(cleanupCtx, "docker", "inspect", "--format",
			`{{index .Config.Labels "azd.spring.h2.owner"}}`, containerID)
		inspect.Env = dockerEnvironment
		label, err := inspect.CombinedOutput()
		if err != nil || strings.TrimSpace(string(label)) != name {
			t.Errorf("refusing cleanup of an unverifiable H2 container: %v", err)
			return
		}
		// #nosec G204 -- validated Docker ID with the exact test-owned label verified immediately above.
		remove := osexec.CommandContext(cleanupCtx, "docker", "rm", "--force", containerID)
		remove.Env = dockerEnvironment
		output, err := remove.CombinedOutput()
		if err != nil {
			t.Errorf("removing this test's owned H2 container: %v: %s", err, output)
		}
	})

	// #nosec G204 -- validated test-owned Docker container ID and fixed UID inspection.
	uidCommand := osexec.CommandContext(ctx, "docker", "exec", containerID, "id", "-u")
	uidCommand.Env = dockerEnvironment
	uidOutput, err := uidCommand.CombinedOutput()
	require.NoError(t, err)
	uid, err := strconv.Atoi(strings.TrimSpace(string(uidOutput)))
	require.NoError(t, err)
	require.Positive(t, uid, "generated Spring image must run as non-root")

	// #nosec G204 -- validated Docker container ID and fixed inventory arguments, without shell interpolation.
	portCommand := osexec.CommandContext(ctx, "docker", "port", containerID, containerPort+"/tcp")
	portCommand.Env = dockerEnvironment
	output, err = portCommand.CombinedOutput()
	require.NoError(t, err, "reading H2 container port: %s", output)
	address := strings.TrimSpace(string(output))
	require.NotEmpty(t, address)
	if after, ok := strings.CutPrefix(address, "0.0.0.0:"); ok {
		address = "127.0.0.1:" + after
	}
	endpoint := "http://" + address + healthPath

	client := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(4 * time.Minute)
	var lastResponse string
	for time.Now().Before(deadline) {
		request, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if requestErr != nil {
			require.NoError(t, requestErr)
		}
		response, requestErr := client.Do(request)
		if requestErr == nil {
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
			closeErr := response.Body.Close()
			if readErr == nil {
				lastResponse = string(body)
				if response.StatusCode == http.StatusOK && strings.Contains(lastResponse, `"status":"UP"`) {
					require.NoError(t, closeErr)
					require.Contains(t, lastResponse, `"database":"H2"`)
					return
				}
			}
		}
		select {
		case <-ctx.Done():
			require.NoError(t, ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}
	// #nosec G204 -- validated Docker container ID and read-only diagnostics, without shell interpolation.
	logCommand := osexec.CommandContext(ctx, "docker", "logs", containerID)
	logCommand.Env = dockerEnvironment
	logs, _ := logCommand.CombinedOutput()
	t.Fatalf("packaged H2 container did not become healthy; last response %q; logs:\n%s", lastResponse, logs)
}

func installExtensionDeveloperToolFastStub(t *testing.T, ctx context.Context, configDir string) {
	t.Helper()
	binaryName := "microsoft-azd-extensions"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	relativePath := filepath.Join("extensions", "microsoft.azd.extensions", binaryName)
	absolutePath := filepath.Join(configDir, relativePath)
	// #nosec G703 -- fixed developer-tool path under the isolated test configuration directory.
	require.NoError(t, os.MkdirAll(filepath.Dir(absolutePath), 0o755))

	// #nosec G204 G702 -- fixed package and fresh test-owned output path, without shell interpolation.
	build := osexec.CommandContext(ctx, "go", "build", "-o", absolutePath, ".")
	build.Dir = filepath.Join(azdSourcePath(t), "extensions", "microsoft.azd.extensions")
	output, err := build.CombinedOutput()
	require.NoError(t, err, "building developer extension fast stub: %s", output)

	config := map[string]any{}
	configPath := filepath.Join(configDir, "config.json")
	// #nosec G703 -- fixed config.json under the isolated test configuration directory.
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
	// #nosec G703 -- fixed config.json under the isolated test configuration directory.
	require.NoError(t, os.WriteFile(configPath, content, 0o600))
}

func verifyPetclinicSourceManifest(t *testing.T, projectDir string) {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(projectDir, "source-revision.json"))
	require.NoError(t, err)
	var manifest petclinicSourceManifest
	require.NoError(t, json.Unmarshal(content, &manifest))
	require.Equal(t, "https://github.com/spring-projects/spring-petclinic", manifest.Repository)
	require.Equal(t, "500158f732419217507c7656904b8e6aa1bcc0d6", manifest.Commit)
	require.Equal(t, "sha256-lf", manifest.HashAlgorithm)
	for _, relative := range []string{
		"pom.xml",
		"mvnw",
		"LICENSE.txt",
		"src/main/java/org/springframework/samples/petclinic/PetClinicApplication.java",
		"src/main/resources/application.properties",
		"src/main/resources/application-postgres.properties",
		"src/main/resources/db/postgres/schema.sql",
		"src/main/resources/db/postgres/data.sql",
	} {
		require.Contains(t, manifest.Files, relative, "source manifest must cover the selected upstream files")
	}

	for relative, expected := range manifest.Files {
		content, err := os.ReadFile(filepath.Join(projectDir, filepath.FromSlash(relative)))
		require.NoError(t, err, relative)
		content = bytes.ReplaceAll(content, []byte("\r\n"), []byte("\n"))
		sum := sha256.Sum256(content)
		actual := strings.ToLower(hex.EncodeToString(sum[:]))
		require.Equal(t, expected, actual, "upstream source drifted at %s", relative)
	}
}
