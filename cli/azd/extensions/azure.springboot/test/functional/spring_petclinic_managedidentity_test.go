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
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	azdazure "github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

type fixtureSourceManifest struct {
	Repository    string `json:"repository"`
	Commit        string `json:"commit"`
	HashAlgorithm string `json:"hashAlgorithm"`
	Files         map[string]string
	Overrides     map[string]struct {
		Fixture string `json:"fixture"`
	} `json:"overrides"`
}

func runFixtureBicep(t *testing.T, args ...string) ([]byte, string, error) {
	t.Helper()
	executable, err := exec.LookPath("bicep")
	prefix := []string{}
	if err != nil {
		executable, err = exec.LookPath("az")
		if err != nil {
			t.Skip("Bicep or Azure CLI is required for fixture compilation")
		}
		prefix = append(prefix, "bicep")
	}
	// #nosec G204 -- executable is a trusted local Bicep frontend; paths are test-owned.
	command := exec.Command(executable, append(prefix, args...)...)
	command.Env = append(os.Environ(), "NO_COLOR=1")
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	return output, stderr.String(), err
}

func TestSpringPetclinicManagedIdentityFixtureIsPortable(t *testing.T) {
	root := managedIdentityFixturePath(t)
	var document map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(readFixtureFile(t, root, "azure.yaml")), &document))
	services := fixtureObject(t, document["services"])
	require.Contains(t, services, "petclinic")
	require.NotContains(t, document, "hooks")

	mainParameters := readParameterValues(t, filepath.Join(root, "infra", "main.parameters.json"))
	mainAuth := fixtureObject(t, mainParameters["databaseAuthentication"])
	require.Equal(t, "${SPRING_DATABASE_AUTHENTICATION:-managedidentity}", mainAuth["type"])
	require.Equal(t, "${SPRING_DATABASE_ADMINISTRATOR_NAME:-}", mainAuth["administratorName"])
	require.Equal(t, "${SPRING_DATABASE_ADMINISTRATOR_OBJECT_ID:-}", mainAuth["administratorPrincipalId"])
	require.Equal(t, "${SPRING_DATABASE_PASSWORD:-}", mainAuth["administratorPassword"])
	require.Equal(t, "${SPRING_DATABASE_APP_PASSWORD:-}", mainAuth["applicationPassword"])
	resources := readFixtureFile(t, root, "infra", "resources.bicep")
	require.Contains(t, resources, "@secure()\n@discriminator('type')\nparam databaseAuthentication")
	require.Contains(t, resources, "administratorPrincipalId: ''")
	require.Contains(t, resources, "administratorName: '' | 'petclinicadmin'")
	require.Contains(t, resources, "@minLength(8)")
	require.NotContains(t, resources, "\nassert ")
	serviceParameters := readParameterValues(t, filepath.Join(root, "infra", "petclinic.parameters.json"))
	require.Equal(t, "${SERVICE_PETCLINIC_IDENTITY_ID}", serviceParameters["identityId"])
	serviceAuth := fixtureObject(t, serviceParameters["databaseAuthentication"])
	require.Equal(t, "${SPRING_DATABASE_AUTHENTICATION:-managedidentity}", serviceAuth["type"])
	require.Equal(t, "${SPRING_DATABASE_APP_PASSWORD:-}", serviceAuth["applicationPassword"])

	properties := readFixtureFile(t, root, "src", "main", "resources", "application-postgres.properties")
	require.Contains(t, properties, "spring.sql.init.mode=always")
	require.Contains(t, properties, "spring.jpa.hibernate.ddl-auto=none")
	for _, module := range []string{"resources", "petclinic"} {
		bicep := readFixtureFile(t, root, "infra", module+".bicep")
		require.NotContains(t, bicep, "value: 'never'")
		require.Contains(t, bicep, "name: 'SPRING_SQL_INIT_MODE'\n    value: 'always'")
	}
	pom := readFixtureFile(t, root, "pom.xml")
	for _, expected := range []string{
		"<artifactId>azure-core-http-jdk-httpclient</artifactId>",
		"<artifactId>azure-core-http-netty</artifactId>", "<exclusions>",
		"<id>detect-git-head</id>", `failifexecutionfails="false"`,
		`<arg value="--verify" />`, `<arg value="HEAD" />`,
		`<condition property="git.commit.id.skip" value="false" else="true">`,
		"<skip>${git.commit.id.skip}</skip>",
	} {
		require.Contains(t, pom, expected)
	}
	for _, removed := range []string{
		"database/initialization.json", "scripts/InitializeDatabase.java", "scripts/initialize-database.ps1",
	} {
		require.NoFileExists(t, filepath.Join(root, filepath.FromSlash(removed)))
	}
	verifyManagedIdentitySourceManifest(t, root)
	require.NoDirExists(t, filepath.Join(root, "target"))
}

func TestSpringPetclinicDatabaseAuthenticationParametersAreTopLevelSecure(t *testing.T) {
	for _, name := range []string{"main", "resources", "petclinic"} {
		t.Run(name, func(t *testing.T) {
			root := managedIdentityFixturePath(t)
			compiled, diagnostics, err := runFixtureBicep(t, "build",
				"--file", filepath.Join(root, "infra", name+".bicep"), "--stdout")
			require.NoError(t, err, diagnostics)
			var template azdazure.ArmTemplate
			require.NoError(t, json.Unmarshal(compiled, &template))
			parameter, exists := template.Parameters["databaseAuthentication"]
			require.True(t, exists)
			require.Equal(t, "secureObject", parameter.Type)
			require.Empty(t, parameter.Ref)
			require.True(t, parameter.Secure(), "azd v1.37.2 did not classify %s as secure", name)
		})
	}
}

func TestSpringPetclinicPasswordRerunAuthenticationPayloadIsValid(t *testing.T) {
	root := managedIdentityFixturePath(t)
	parameters := readParameterValues(t, filepath.Join(root, "infra", "main.parameters.json"))
	require.Equal(t, "${SPRING_DATABASE_ADMINISTRATOR_NAME:-}",
		fixtureObject(t, parameters["databaseAuthentication"])["administratorName"])
	project := tempDirWithDiagnostics(t)
	infra := filepath.Join(project, "infra")
	require.NoError(t, os.CopyFS(infra, os.DirFS(filepath.Join(root, "infra"))))
	rerun := `using './main.bicep'
param environmentName = 'password-rerun'
param location = 'eastus'
param databaseAuthentication = {
  type: 'password'
  administratorName: 'petclinicadmin'
  administratorPrincipalId: ''
  administratorPassword: 'aaaaaaaa'
  applicationPassword: 'bbbbbbbb'
}
`
	path := filepath.Join(infra, "password-rerun.bicepparam")
	require.NoError(t, os.WriteFile(path, []byte(rerun), 0o600))
	_, diagnostics, err := runFixtureBicep(t, "build-params", "--file", path, "--stdout")
	require.NoError(t, err, diagnostics)
	invalid := strings.Replace(rerun, "administratorName: 'petclinicadmin'",
		"administratorName: 'silently-ignored-admin'", 1)
	require.NoError(t, os.WriteFile(path, []byte(invalid), 0o600))
	_, diagnostics, err = runFixtureBicep(t, "build-params", "--file", path, "--stdout")
	require.Error(t, err)
	require.Contains(t, diagnostics, "silently-ignored-admin")
}

func Test_CLI_SpringPetclinicManagedIdentity_LocalMaterialization(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Minute)
	defer cancel()
	configDir := tempDirWithDiagnostics(t)
	installSpringExtensionFixture(t, ctx, configDir)
	fixture := managedIdentityFixturePath(t)
	t.Run("authored project", func(t *testing.T) {
		projectDir := tempDirWithDiagnostics(t)
		require.NoError(t, os.CopyFS(projectDir, os.DirFS(fixture)))
		cli := initializeManagedIdentityProject(t, ctx, configDir, projectDir, "petclinic-mi-authored")
		verifyManagedIdentityMaterialization(t, cli.WorkingDirectory, fixture)
	})
	t.Run("local init template", func(t *testing.T) {
		projectDir := tempDirWithDiagnostics(t)
		cli := newSpringCLI(t)
		cli.WorkingDirectory = projectDir
		cli.Env = managedIdentityTestEnvironment(cli.Env, configDir)
		result, err := cli.RunCommand(ctx, "init", "-t", fixture, ".", "-e", "petclinic-mi-template", "--no-prompt")
		require.NoError(t, err, result.Stdout+result.Stderr)
		verifyManagedIdentityMaterialization(t, projectDir, fixture)
	})
}

func initializeManagedIdentityProject(
	t *testing.T, ctx context.Context, configDir, projectDir, environmentName string,
) *azdcli.CLI {
	t.Helper()
	cli := newSpringCLI(t)
	cli.WorkingDirectory = projectDir
	cli.Env = managedIdentityTestEnvironment(cli.Env, configDir)
	result, err := cli.RunCommand(ctx, "init", "-e", environmentName, "--no-prompt")
	require.NoError(t, err, result.Stdout+result.Stderr)
	return cli
}

func managedIdentityTestEnvironment(environment []string, configDir string) []string {
	environment = environmentWithout(environment, "AZD_ALLOW_NON_EMPTY_FOLDER", "AZD_CONFIG_DIR",
		"POSTGRES_BOOTSTRAP_CLIENT_IP", "SPRING_DATABASE_PASSWORD", "SPRING_DATABASE_APP_PASSWORD")
	return append(environment, "AZD_CONFIG_DIR="+configDir, "AZURE_DEV_COLLECT_TELEMETRY=no")
}

func managedIdentityFixturePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(azdSourcePath(t), "extensions", "azure.springboot",
		"test", "functional", "testdata", "spring-petclinic-managedidentity")
}

func verifyManagedIdentityMaterialization(t *testing.T, projectDir, fixture string) {
	t.Helper()
	for _, relative := range []string{
		"azure.yaml", "infra/main.bicep", "infra/petclinic.bicep",
		"src/main/resources/db/postgres/schema.sql", "src/main/resources/db/postgres/data.sql",
	} {
		expected, err := os.ReadFile(filepath.Join(fixture, filepath.FromSlash(relative)))
		require.NoError(t, err)
		actual, err := os.ReadFile(filepath.Join(projectDir, filepath.FromSlash(relative)))
		require.NoError(t, err)
		require.Equal(t, expected, actual, relative)
	}
	require.DirExists(t, filepath.Join(projectDir, ".azure"))
	require.NoDirExists(t, filepath.Join(projectDir, "target"))
}

func readParameterValues(t *testing.T, path string) map[string]any {
	t.Helper()
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	var document struct {
		Parameters map[string]struct {
			Value any `json:"value"`
		} `json:"parameters"`
	}
	require.NoError(t, json.Unmarshal(content, &document))
	result := make(map[string]any, len(document.Parameters))
	for name, parameter := range document.Parameters {
		result[name] = parameter.Value
	}
	return result
}

func readFixtureFile(t *testing.T, root string, parts ...string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(append([]string{root}, parts...)...))
	require.NoError(t, err)
	return strings.ReplaceAll(string(content), "\r\n", "\n")
}

func verifyManagedIdentitySourceManifest(t *testing.T, root string) {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, "source-revision.json"))
	require.NoError(t, err)
	var manifest fixtureSourceManifest
	require.NoError(t, json.Unmarshal(content, &manifest))
	require.Equal(t, "https://github.com/spring-projects/spring-petclinic", manifest.Repository)
	require.Equal(t, "500158f732419217507c7656904b8e6aa1bcc0d6", manifest.Commit)
	require.Equal(t, "sha256-lf", manifest.HashAlgorithm)
	require.Equal(t, []string{"pom.xml", "src/main/resources/application-postgres.properties",
		"src/test/java/org/springframework/samples/petclinic/PostgresIntegrationTests.java"},
		sortedMapKeys(manifest.Overrides))
	for relative, upstreamHash := range manifest.Files {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		require.NoError(t, err, relative)
		actual := hashLF(content)
		if override, ok := manifest.Overrides[relative]; ok {
			require.NotEqual(t, upstreamHash, actual, relative)
			require.Equal(t, override.Fixture, actual, relative)
		} else {
			require.Equal(t, upstreamHash, actual, relative)
		}
	}
}

func hashLF(content []byte) string {
	content = bytes.ReplaceAll(content, []byte("\r\n"), []byte("\n"))
	sum := sha256.Sum256(content)
	return strings.ToLower(hex.EncodeToString(sum[:]))
}

func sortedMapKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func fixtureObject(t *testing.T, value any) map[string]any {
	t.Helper()
	object, ok := value.(map[string]any)
	require.True(t, ok, "expected object, got %T", value)
	return object
}
