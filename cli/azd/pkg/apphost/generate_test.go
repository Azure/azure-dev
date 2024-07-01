package apphost

import (
	"context"
	_ "embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/snapshot"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

//go:embed testdata/aspire-docker.json
var aspireDockerManifest []byte

//go:embed testdata/aspire-storage.json
var aspireStorageManifest []byte

//go:embed testdata/aspire-args.json
var aspireArgsManifest []byte

//go:embed testdata/aspire-bicep.json
var aspireBicepManifest []byte

//go:embed testdata/aspire-escaping.json
var aspireEscapingManifest []byte

//go:embed testdata/aspire-container.json
var aspireContainerManifest []byte

// mockPublishManifest mocks the dotnet run --publisher manifest command to return a fixed manifest.
func mockPublishManifest(mockCtx *mocks.MockContext, manifest []byte, files map[string]string) {
	mockCtx.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return args.Cmd == "dotnet" && args.Args[0] == "run" && args.Args[3] == "--publisher" && args.Args[4] == "manifest"
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		err := os.WriteFile(args.Args[6], manifest, osutil.PermissionFile)
		if err != nil {
			return exec.RunResult{
				ExitCode: -1,
				Stderr:   err.Error(),
			}, err
		}
		publishDir := filepath.Dir(args.Args[6])
		for name, contents := range files {
			err := os.WriteFile(filepath.Join(publishDir, name), []byte(contents), osutil.PermissionFile)
			if err != nil {
				return exec.RunResult{
					ExitCode: -1,
					Stderr:   err.Error(),
				}, err
			}
		}
		return exec.RunResult{}, nil
	})
}

func TestAspireEscaping(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping due to EOL issues on Windows with the baselines")
	}

	ctx := context.Background()
	mockCtx := mocks.NewMockContext(ctx)
	mockPublishManifest(mockCtx, aspireEscapingManifest, nil)

	mockCli := dotnet.NewDotNetCli(mockCtx.CommandRunner)

	m, err := ManifestFromAppHost(ctx, filepath.Join("testdata", "AspireDocker.AppHost.csproj"), mockCli, "")
	require.NoError(t, err)
	for _, name := range []string{"api"} {
		t.Run(name, func(t *testing.T) {
			tmpl, err := ContainerAppManifestTemplateForProject(m, name, AppHostOptions{})
			require.NoError(t, err)
			snapshot.SnapshotT(t, tmpl)
		})
	}
}

func TestAspireStorageGeneration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping due to EOL issues on Windows with the baselines")
	}

	ctx := context.Background()
	mockCtx := mocks.NewMockContext(ctx)
	mockPublishManifest(mockCtx, aspireStorageManifest, nil)
	mockCli := dotnet.NewDotNetCli(mockCtx.CommandRunner)

	m, err := ManifestFromAppHost(ctx, filepath.Join("testdata", "AspireDocker.AppHost.csproj"), mockCli, "")
	require.NoError(t, err)

	files, err := BicepTemplate("main", m, AppHostOptions{})
	require.NoError(t, err)

	err = fs.WalkDir(files, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		contents, err := fs.ReadFile(files, path)
		if err != nil {
			return err
		}
		t.Run(path, func(t *testing.T) {
			snapshot.SnapshotT(t, string(contents))
		})
		return nil
	})
	require.NoError(t, err)
}

func TestAspireBicepGeneration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping due to EOL issues on Windows with the baselines")
	}

	ctx := context.Background()
	mockCtx := mocks.NewMockContext(ctx)
	filesFromManifest := make(map[string]string)
	ignoredBicepContent := "bicep file contents"
	filesFromManifest["test.bicep"] = ignoredBicepContent
	filesFromManifest["aspire.hosting.azure.bicep.postgres.bicep"] = ignoredBicepContent
	filesFromManifest["aspire.hosting.azure.bicep.servicebus.bicep"] = ignoredBicepContent
	filesFromManifest["aspire.hosting.azure.bicep.appinsights.bicep"] = ignoredBicepContent
	filesFromManifest["aspire.hosting.azure.bicep.sql.bicep"] = ignoredBicepContent
	mockPublishManifest(mockCtx, aspireBicepManifest, filesFromManifest)
	mockCli := dotnet.NewDotNetCli(mockCtx.CommandRunner)

	m, err := ManifestFromAppHost(ctx, filepath.Join("testdata", "AspireDocker.AppHost.csproj"), mockCli, "")
	require.NoError(t, err)

	files, err := BicepTemplate("main", m, AppHostOptions{})
	require.NoError(t, err)

	err = fs.WalkDir(files, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		contents, err := fs.ReadFile(files, path)
		if err != nil {
			return err
		}
		t.Run(path, func(t *testing.T) {
			snapshot.SnapshotT(t, string(contents))
		})
		return nil
	})
	require.NoError(t, err)

	for _, name := range []string{"frontend"} {
		t.Run(name, func(t *testing.T) {
			tmpl, err := ContainerAppManifestTemplateForProject(m, name, AppHostOptions{})
			require.NoError(t, err)
			snapshot.SnapshotT(t, tmpl)
		})
	}
}

func TestAspireDockerGeneration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping due to EOL issues on Windows with the baselines")
	}

	ctx := context.Background()
	mockCtx := mocks.NewMockContext(ctx)
	mockPublishManifest(mockCtx, aspireDockerManifest, nil)
	mockCli := dotnet.NewDotNetCli(mockCtx.CommandRunner)

	m, err := ManifestFromAppHost(ctx, filepath.Join("testdata", "AspireDocker.AppHost.csproj"), mockCli, "")
	require.NoError(t, err)

	for _, name := range []string{"nodeapp", "api"} {
		t.Run(name, func(t *testing.T) {
			tmpl, err := ContainerAppManifestTemplateForProject(m, name, AppHostOptions{})
			require.NoError(t, err)
			snapshot.SnapshotT(t, tmpl)
		})
	}

	files, err := BicepTemplate("main", m, AppHostOptions{})
	require.NoError(t, err)

	err = fs.WalkDir(files, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		contents, err := fs.ReadFile(files, path)
		if err != nil {
			return err
		}
		t.Run(path, func(t *testing.T) {
			snapshot.SnapshotT(t, string(contents))
		})
		return nil
	})
	require.NoError(t, err)
}

func TestAspireDashboardGeneration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping due to EOL issues on Windows with the baselines")
	}

	ctx := context.Background()
	mockCtx := mocks.NewMockContext(ctx)
	mockPublishManifest(mockCtx, aspireDockerManifest, nil)
	mockCli := dotnet.NewDotNetCli(mockCtx.CommandRunner)

	m, err := ManifestFromAppHost(ctx, filepath.Join("testdata", "AspireDocker.AppHost.csproj"), mockCli, "")
	require.NoError(t, err)

	files, err := BicepTemplate("main", m, AppHostOptions{})
	require.NoError(t, err)

	err = fs.WalkDir(files, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		contents, err := fs.ReadFile(files, path)
		if err != nil {
			return err
		}
		t.Run(path, func(t *testing.T) {
			snapshot.SnapshotT(t, string(contents))
		})
		return nil
	})
	require.NoError(t, err)
}

func TestAspireArgsGeneration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping due to EOL issues on Windows with the baselines")
	}

	ctx := context.Background()
	mockCtx := mocks.NewMockContext(ctx)
	mockPublishManifest(mockCtx, aspireArgsManifest, nil)
	mockCli := dotnet.NewDotNetCli(mockCtx.CommandRunner)

	m, err := ManifestFromAppHost(ctx, filepath.Join("testdata", "AspireArgs.AppHost.csproj"), mockCli, "")
	require.NoError(t, err)

	manifest, err := ContainerAppManifestTemplateForProject(m, "apiservice", AppHostOptions{})
	require.NoError(t, err)

	snapshot.SnapshotT(t, manifest)
}

func TestAspireContainerGeneration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping due to EOL issues on Windows with the baselines")
	}

	ctx := context.Background()
	mockCtx := mocks.NewMockContext(ctx)
	mockPublishManifest(mockCtx, aspireContainerManifest, nil)
	mockCli := dotnet.NewDotNetCli(mockCtx.CommandRunner)

	m, err := ManifestFromAppHost(ctx, filepath.Join("testdata", "AspireDocker.AppHost.csproj"), mockCli, "")
	require.NoError(t, err)

	for _, name := range []string{"mysqlabstract", "my-sql-abstract", "noVolume", "kafka"} {
		t.Run(name, func(t *testing.T) {
			tmpl, err := ContainerAppManifestTemplateForProject(m, name, AppHostOptions{})
			require.NoError(t, err)
			snapshot.SnapshotT(t, tmpl)
		})
	}

	_, err = BicepTemplate("main", m, AppHostOptions{})
	require.Error(t, err, provisioning.ErrBindMountOperationDisabled)

	files, err := BicepTemplate("main", m, AppHostOptions{
		AzdOperations: true,
	})
	require.NoError(t, err)

	err = fs.WalkDir(files, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if d.Name() == "azd.operations.yaml" {
			// can't snapshot this file as it contains a path that on CI is different depending on OS
			return nil
		}
		contents, err := fs.ReadFile(files, path)
		if err != nil {
			return err
		}
		t.Run(path, func(t *testing.T) {
			snapshot.SnapshotT(t, string(contents))
		})
		return nil
	})
	require.NoError(t, err)
}

func TestBuildEnvResolveServiceToConnectionString(t *testing.T) {
	// Create a mock infraGenerator instance
	mockGenerator := &infraGenerator{
		resourceTypes: map[string]string{
			"service": "postgres.database.v0",
		},
	}

	// Define test input
	env := map[string]string{
		"VAR1": "value1",
		"VAR2": "value2",
		"VAR3": `complex {service.connectionString} expression`,
	}

	expected := map[string]string{
		"VAR1": "value1",
		"VAR2": "value2",
	}

	expectedSecrets := map[string]string{
		"VAR3": `complex {{ connectionString "service" }} expression`,
	}

	manifestCtx := &genContainerAppManifestTemplateContext{
		Env:             make(map[string]string),
		Secrets:         make(map[string]string),
		KeyVaultSecrets: make(map[string]string),
	}

	// Call the method being tested
	err := mockGenerator.buildEnvBlock(env, manifestCtx)
	require.NoError(t, err)
	require.Equal(t, expected, manifestCtx.Env)
	require.Equal(t, expectedSecrets, manifestCtx.Secrets)
}

func TestAddContainerAppService(t *testing.T) {
	// Create a mock infraGenerator instance
	mockGenerator := &infraGenerator{
		bicepContext: genBicepTemplateContext{
			StorageAccounts: make(map[string]genStorageAccount),
		},
	}

	// Call the method being tested
	mockGenerator.addStorageBlob("storage", "blob")
	mockGenerator.addStorageAccount("storage")
	mockGenerator.addStorageQueue("storage", "quue")
	mockGenerator.addStorageAccount("storage")
	mockGenerator.addStorageTable("storage", "table")
	mockGenerator.addStorageAccount("storage2")
	mockGenerator.addStorageAccount("storage3")
	mockGenerator.addStorageTable("storage4", "table")
	mockGenerator.addStorageTable("storage2", "table")
	mockGenerator.addStorageQueue("storage", "quue2")

	require.Equal(t, 1, len(mockGenerator.bicepContext.StorageAccounts["storage"].Blobs))
	require.Equal(t, 2, len(mockGenerator.bicepContext.StorageAccounts["storage"].Queues))
	require.Equal(t, 1, len(mockGenerator.bicepContext.StorageAccounts["storage"].Tables))

	require.Equal(t, 0, len(mockGenerator.bicepContext.StorageAccounts["storage2"].Blobs))
	require.Equal(t, 0, len(mockGenerator.bicepContext.StorageAccounts["storage2"].Queues))
	require.Equal(t, 1, len(mockGenerator.bicepContext.StorageAccounts["storage2"].Tables))

	require.Equal(t, 0, len(mockGenerator.bicepContext.StorageAccounts["storage3"].Blobs))
	require.Equal(t, 0, len(mockGenerator.bicepContext.StorageAccounts["storage3"].Queues))
	require.Equal(t, 0, len(mockGenerator.bicepContext.StorageAccounts["storage3"].Tables))

	require.Equal(t, 0, len(mockGenerator.bicepContext.StorageAccounts["storage4"].Blobs))
	require.Equal(t, 0, len(mockGenerator.bicepContext.StorageAccounts["storage4"].Queues))
	require.Equal(t, 1, len(mockGenerator.bicepContext.StorageAccounts["storage4"].Tables))
}

func TestEvaluateForOutputs(t *testing.T) {
	value := "{resource.outputs.output1} and {resource.secretOutputs.output2}"

	expectedOutputs := map[string]genOutputParameter{
		"RESOURCE_OUTPUT1": {
			Type:  "string",
			Value: "resource.outputs.output1",
		},
		"RESOURCE_OUTPUT2": {
			Type:  "string",
			Value: "resource.secretOutputs.output2",
		},
	}

	outputs, err := evaluateForOutputs(value)
	require.NoError(t, err)
	require.Equal(t, expectedOutputs, outputs)
}

func TestInjectValueForBicepParameter(t *testing.T) {
	resourceName := "example"
	param := knownParameterKeyVault
	expectedParameter := `"exampleParameter"`

	value, inject, err := injectValueForBicepParameter(resourceName, param, "exampleParameter")
	require.NoError(t, err)
	require.Equal(t, expectedParameter, value)
	require.False(t, inject)

	uniqueName := strings.ToUpper("kv" + uniqueFnvNumber(resourceName))
	expectedParameter = fmt.Sprintf("resources.outputs.SERVICE_BINDING_%s_NAME", uniqueName)
	value, inject, err = injectValueForBicepParameter(resourceName, param, "")
	require.NoError(t, err)
	require.Equal(t, expectedParameter, value)
	require.True(t, inject)

	withDash := resourceName + "-01"
	uniqueName = strings.ToUpper("kv" + uniqueFnvNumber(withDash))
	expectedParameter = fmt.Sprintf("resources.outputs.SERVICE_BINDING_%s_NAME", uniqueName)
	value, inject, err = injectValueForBicepParameter(withDash, param, "")
	require.NoError(t, err)
	require.Equal(t, expectedParameter, value)
	require.True(t, inject)

	param = knownParameterPrincipalId
	expectedParameter = `"exampleParameter"`

	value, inject, err = injectValueForBicepParameter(resourceName, param, "exampleParameter")
	require.NoError(t, err)
	require.Equal(t, expectedParameter, value)
	require.False(t, inject)

	expectedParameter = knownInjectedValuePrincipalId
	value, inject, err = injectValueForBicepParameter(resourceName, param, "")
	require.NoError(t, err)
	require.Equal(t, expectedParameter, value)
	require.True(t, inject)

	param = knownParameterPrincipalType
	expectedParameter = `"exampleParameter"`

	value, inject, err = injectValueForBicepParameter(resourceName, param, "exampleParameter")
	require.NoError(t, err)
	require.Equal(t, expectedParameter, value)
	require.False(t, inject)

	param = knownParameterPrincipalType
	expectedParameter = knownInjectedValuePrincipalType

	value, inject, err = injectValueForBicepParameter(resourceName, param, "")
	require.NoError(t, err)
	require.Equal(t, expectedParameter, value)
	require.True(t, inject)

	param = knownParameterPrincipalName
	expectedParameter = `"exampleParameter"`

	value, inject, err = injectValueForBicepParameter(resourceName, param, "exampleParameter")
	require.NoError(t, err)
	require.Equal(t, expectedParameter, value)
	require.False(t, inject)

	param = knownParameterPrincipalName
	expectedParameter = knownInjectedValuePrincipalName

	value, inject, err = injectValueForBicepParameter(resourceName, param, "")
	require.NoError(t, err)
	require.Equal(t, expectedParameter, value)
	require.True(t, inject)

	param = knownParameterContainerEnvName
	expectedParameter = knownInjectedValueContainerEnvName

	value, inject, err = injectValueForBicepParameter(resourceName, param, "")
	require.NoError(t, err)
	require.Equal(t, expectedParameter, value)
	require.True(t, inject)

	param = knownParameterContainerEnvId
	expectedParameter = knownInjectedValueContainerEnvId

	value, inject, err = injectValueForBicepParameter(resourceName, param, "")
	require.NoError(t, err)
	require.Equal(t, expectedParameter, value)
	require.True(t, inject)

	param = knownParameterLogAnalytics
	expectedParameter = `"exampleParameter"`

	value, inject, err = injectValueForBicepParameter(resourceName, param, "exampleParameter")
	require.NoError(t, err)
	require.Equal(t, expectedParameter, value)
	require.False(t, inject)

	param = knownParameterLogAnalytics
	expectedParameter = knownInjectedValueLogAnalytics

	value, inject, err = injectValueForBicepParameter(resourceName, param, "")
	require.NoError(t, err)
	require.Equal(t, expectedParameter, value)
	require.True(t, inject)

	param = "otherParam"
	expectedParameter = `"exampleParameter"`
	value, inject, err = injectValueForBicepParameter(resourceName, param, "exampleParameter")
	require.NoError(t, err)
	require.Equal(t, expectedParameter, value)
	require.False(t, inject)

	expectedParameter = `["exampleParameter"]`
	value, inject, err = injectValueForBicepParameter(resourceName, param, []string{"exampleParameter"})
	require.NoError(t, err)
	require.Equal(t, expectedParameter, value)
	require.False(t, inject)

	expectedParameter = `true`
	value, inject, err = injectValueForBicepParameter(resourceName, param, true)
	require.NoError(t, err)
	require.Equal(t, expectedParameter, value)
	require.False(t, inject)

	expectedParameter = `""`
	value, inject, err = injectValueForBicepParameter(resourceName, param, "")
	require.NoError(t, err)
	require.Equal(t, expectedParameter, value)
	require.False(t, inject)
}

func TestHasInputs(t *testing.T) {
	tests := []struct {
		name   string
		value  string
		result bool
	}{
		{
			name:   "Valid input with inputs",
			value:  "{resource.inputs.property}",
			result: true,
		},
		{
			name:   "Valid input with inputs dash",
			value:  "{resource-01.inputs.property}",
			result: true,
		},
		{
			name:   "Valid input with numbers",
			value:  "{resource001.inputs.property}",
			result: true,
		},
		{
			name:   "Valid input with numbers and dash",
			value:  "{resource-01.inputs.property}",
			result: true,
		},
		{
			name:   "No inputs - missing inputs token",
			value:  "{resource.property}",
			result: false,
		},
		{
			name:   "No inputs - missing close",
			value:  "{resource.inputs.property",
			result: false,
		},
		{
			name:   "No inputs - unsupported resource",
			value:  "{resource_001.inputs.property",
			result: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.result, hasInputs(tt.value))
		})
	}
}
