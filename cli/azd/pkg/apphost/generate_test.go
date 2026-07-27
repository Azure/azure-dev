// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package apphost

import (
	_ "embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/snapshot"
)

//go:embed testdata/aspire-docker.json
var aspireDockerManifest []byte

//go:embed testdata/aspire-args.json
var aspireArgsManifest []byte

//go:embed testdata/aspire-bicep.json
var aspireBicepManifest []byte

//go:embed testdata/aspire-container.json
var aspireContainerManifest []byte

//go:embed testdata/aspire-container-args.json
var aspireContainerArgsManifest []byte

//go:embed testdata/aspire-projectv1.json
var aspireProjectV1Manifet []byte

//go:embed testdata/aspire-apphost-owns-compute.json
var aspireApphostOwnsCompute []byte

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

func TestAspireBicepGenerationAppHostOwnsCompute(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping due to EOL issues on Windows with the baselines")
	}

	ctx := t.Context()
	mockCtx := mocks.NewMockContext(ctx)
	filesFromManifest := make(map[string]string)
	ignoredBicepContent := "bicep file contents"
	filesFromManifest["aspire-owned-env.bicep"] = ignoredBicepContent
	filesFromManifest["bicepModule.bicep"] = ignoredBicepContent
	filesFromManifest["aspire.hosting.azure.bicep.postgres.bicep"] = ignoredBicepContent
	filesFromManifest["aspire.hosting.azure.bicep.servicebus.bicep"] = ignoredBicepContent
	filesFromManifest["aspire.hosting.azure.bicep.appinsights.bicep"] = ignoredBicepContent
	filesFromManifest["aspire.hosting.azure.bicep.sql.bicep"] = ignoredBicepContent
	mockPublishManifest(mockCtx, aspireApphostOwnsCompute, filesFromManifest)
	mockCli := dotnet.NewCli(mockCtx.CommandRunner)

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
			tmpl, mType, err := ContainerAppManifestTemplateForProject(m, name, AppHostOptions{})
			require.NoError(t, err)
			require.Equal(t, ContainerAppManifestTypeYAML, mType)
			snapshot.SnapshotT(t, tmpl)
		})
	}
}

func TestAspireBicepGeneration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping due to EOL issues on Windows with the baselines")
	}

	ctx := t.Context()
	mockCtx := mocks.NewMockContext(ctx)
	filesFromManifest := make(map[string]string)
	ignoredBicepContent := "bicep file contents"
	filesFromManifest["test.bicep"] = ignoredBicepContent
	filesFromManifest["aspire.hosting.azure.bicep.postgres.bicep"] = ignoredBicepContent
	filesFromManifest["aspire.hosting.azure.bicep.servicebus.bicep"] = ignoredBicepContent
	filesFromManifest["aspire.hosting.azure.bicep.appinsights.bicep"] = ignoredBicepContent
	filesFromManifest["aspire.hosting.azure.bicep.sql.bicep"] = ignoredBicepContent
	filesFromManifest["kv.bicep"] = ignoredBicepContent
	mockPublishManifest(mockCtx, aspireBicepManifest, filesFromManifest)
	mockCli := dotnet.NewCli(mockCtx.CommandRunner)

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
			tmpl, mType, err := ContainerAppManifestTemplateForProject(m, name, AppHostOptions{})
			require.NoError(t, err)
			require.Equal(t, ContainerAppManifestTypeYAML, mType)
			snapshot.SnapshotT(t, tmpl)
		})
	}
}

func TestAspireDockerGeneration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping due to EOL issues on Windows with the baselines")
	}

	ctx := t.Context()
	mockCtx := mocks.NewMockContext(ctx)
	mockPublishManifest(mockCtx, aspireDockerManifest, nil)
	mockCli := dotnet.NewCli(mockCtx.CommandRunner)

	m, err := ManifestFromAppHost(ctx, filepath.Join("testdata", "AspireDocker.AppHost.csproj"), mockCli, "")
	require.NoError(t, err)

	for _, name := range []string{"nodeapp", "api"} {
		t.Run(name, func(t *testing.T) {
			tmpl, mType, err := ContainerAppManifestTemplateForProject(m, name, AppHostOptions{})
			require.NoError(t, err)
			require.Equal(t, ContainerAppManifestTypeYAML, mType)
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

	ctx := t.Context()
	mockCtx := mocks.NewMockContext(ctx)
	mockPublishManifest(mockCtx, aspireDockerManifest, nil)
	mockCli := dotnet.NewCli(mockCtx.CommandRunner)

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

	ctx := t.Context()
	mockCtx := mocks.NewMockContext(ctx)
	mockPublishManifest(mockCtx, aspireArgsManifest, nil)
	mockCli := dotnet.NewCli(mockCtx.CommandRunner)

	m, err := ManifestFromAppHost(ctx, filepath.Join("testdata", "AspireArgs.AppHost.csproj"), mockCli, "")
	require.NoError(t, err)

	manifest, mType, err := ContainerAppManifestTemplateForProject(m, "apiservice", AppHostOptions{})
	require.Equal(t, ContainerAppManifestTypeYAML, mType)
	require.NoError(t, err)

	snapshot.SnapshotT(t, manifest)
}

func TestAspireContainerGeneration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping due to EOL issues on Windows with the baselines")
	}

	ctx := t.Context()
	mockCtx := mocks.NewMockContext(ctx)
	mockPublishManifest(mockCtx, aspireContainerManifest, nil)
	mockCli := dotnet.NewCli(mockCtx.CommandRunner)

	m, err := ManifestFromAppHost(ctx, filepath.Join("testdata", "AspireDocker.AppHost.csproj"), mockCli, "")
	require.NoError(t, err)

	for _, name := range []string{"mysqlabstract", "my-sql-abstract", "noVolume", "kafka"} {
		t.Run(name, func(t *testing.T) {
			tmpl, mType, err := ContainerAppManifestTemplateForProject(m, name, AppHostOptions{})
			require.Equal(t, ContainerAppManifestTypeYAML, mType)
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

func TestAspireContainerArgs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping due to EOL issues on Windows with the baselines")
	}

	ctx := t.Context()
	mockCtx := mocks.NewMockContext(ctx)
	mockPublishManifest(mockCtx, aspireContainerArgsManifest, nil)
	mockCli := dotnet.NewCli(mockCtx.CommandRunner)

	m, err := ManifestFromAppHost(ctx, filepath.Join("testdata", "AspireDocker.AppHost.csproj"), mockCli, "")
	require.NoError(t, err)

	for _, name := range []string{"container0", "container1"} {
		t.Run(name, func(t *testing.T) {
			tmpl, mType, err := ContainerAppManifestTemplateForProject(m, name, AppHostOptions{})
			require.Equal(t, ContainerAppManifestTypeYAML, mType)
			require.NoError(t, err)
			snapshot.SnapshotT(t, tmpl)
		})
	}
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

	outputs, err := evaluateForOutputs(value, false)
	require.NoError(t, err)
	require.Equal(t, expectedOutputs, outputs)
}

func TestInjectValueForBicepParameter(t *testing.T) {
	resourceName := "example"
	param := knownParameterKeyVault
	expectedParameter := `"exampleParameter"`

	value, inject, err := injectValueForBicepParameter(resourceName, param, "exampleParameter", false)
	require.NoError(t, err)
	require.Equal(t, expectedParameter, value)
	require.False(t, inject)

	uniqueName := strings.ToUpper("kv" + uniqueFnvNumber(resourceName))
	expectedParameter = fmt.Sprintf("resources.outputs.SERVICE_BINDING_%s_NAME", uniqueName)
	value, inject, err = injectValueForBicepParameter(resourceName, param, "", false)
	require.NoError(t, err)
	require.Equal(t, expectedParameter, value)
	require.True(t, inject)

	withDash := resourceName + "-01"
	uniqueName = strings.ToUpper("kv" + uniqueFnvNumber(withDash))
	expectedParameter = fmt.Sprintf("resources.outputs.SERVICE_BINDING_%s_NAME", uniqueName)
	value, inject, err = injectValueForBicepParameter(withDash, param, "", false)
	require.NoError(t, err)
	require.Equal(t, expectedParameter, value)
	require.True(t, inject)

	param = knownParameterPrincipalId
	expectedParameter = `"exampleParameter"`

	value, inject, err = injectValueForBicepParameter(resourceName, param, "exampleParameter", false)
	require.NoError(t, err)
	require.Equal(t, expectedParameter, value)
	require.False(t, inject)

	expectedParameter = knownInjectedValuePrincipalId
	value, inject, err = injectValueForBicepParameter(resourceName, param, "", false)
	require.NoError(t, err)
	require.Equal(t, expectedParameter, value)
	require.True(t, inject)

	param = knownParameterPrincipalType
	expectedParameter = `"exampleParameter"`

	value, inject, err = injectValueForBicepParameter(resourceName, param, "exampleParameter", false)
	require.NoError(t, err)
	require.Equal(t, expectedParameter, value)
	require.False(t, inject)

	param = knownParameterPrincipalType
	expectedParameter = knownInjectedValuePrincipalType

	value, inject, err = injectValueForBicepParameter(resourceName, param, "", false)
	require.NoError(t, err)
	require.Equal(t, expectedParameter, value)
	require.True(t, inject)

	param = knownParameterPrincipalName
	expectedParameter = `"exampleParameter"`

	value, inject, err = injectValueForBicepParameter(resourceName, param, "exampleParameter", false)
	require.NoError(t, err)
	require.Equal(t, expectedParameter, value)
	require.False(t, inject)

	param = knownParameterPrincipalName
	expectedParameter = knownInjectedValuePrincipalName

	value, inject, err = injectValueForBicepParameter(resourceName, param, "", false)
	require.NoError(t, err)
	require.Equal(t, expectedParameter, value)
	require.True(t, inject)

	param = knownParameterContainerEnvName
	expectedParameter = knownInjectedValueContainerEnvName

	value, inject, err = injectValueForBicepParameter(resourceName, param, "", false)
	require.NoError(t, err)
	require.Equal(t, expectedParameter, value)
	require.True(t, inject)

	param = knownParameterContainerEnvId
	expectedParameter = knownInjectedValueContainerEnvId

	value, inject, err = injectValueForBicepParameter(resourceName, param, "", false)
	require.NoError(t, err)
	require.Equal(t, expectedParameter, value)
	require.True(t, inject)

	param = knownParameterLogAnalytics
	expectedParameter = `"exampleParameter"`

	value, inject, err = injectValueForBicepParameter(resourceName, param, "exampleParameter", false)
	require.NoError(t, err)
	require.Equal(t, expectedParameter, value)
	require.False(t, inject)

	param = knownParameterLogAnalytics
	expectedParameter = knownInjectedValueLogAnalytics

	value, inject, err = injectValueForBicepParameter(resourceName, param, "", false)
	require.NoError(t, err)
	require.Equal(t, expectedParameter, value)
	require.True(t, inject)

	param = "otherParam"
	expectedParameter = `"exampleParameter"`
	value, inject, err = injectValueForBicepParameter(resourceName, param, "exampleParameter", false)
	require.NoError(t, err)
	require.Equal(t, expectedParameter, value)
	require.False(t, inject)

	expectedParameter = `["exampleParameter"]`
	value, inject, err = injectValueForBicepParameter(resourceName, param, []string{"exampleParameter"}, false)
	require.NoError(t, err)
	require.Equal(t, expectedParameter, value)
	require.False(t, inject)

	expectedParameter = `true`
	value, inject, err = injectValueForBicepParameter(resourceName, param, true, false)
	require.NoError(t, err)
	require.Equal(t, expectedParameter, value)
	require.False(t, inject)

	expectedParameter = `""`
	value, inject, err = injectValueForBicepParameter(resourceName, param, "", false)
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

func TestAspireProjectV1Generation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping due to EOL issues on Windows with the baselines")
	}

	ctx := t.Context()
	mockCtx := mocks.NewMockContext(ctx)
	filesFromManifest := make(map[string]string)
	ignoredBicepContent := "bicep file contents"
	filesFromManifest["test.bicep"] = ignoredBicepContent
	filesFromManifest["storage.module.bicep"] = ignoredBicepContent
	filesFromManifest["cache.module.bicep"] = ignoredBicepContent
	filesFromManifest["api.module.bicep"] = ignoredBicepContent
	filesFromManifest["account.module.bicep"] = ignoredBicepContent
	mockPublishManifest(mockCtx, aspireProjectV1Manifet, filesFromManifest)
	mockCli := dotnet.NewCli(mockCtx.CommandRunner)

	m, err := ManifestFromAppHost(ctx, filepath.Join("testdata", "AspireDocker.AppHost.csproj"), mockCli, "")
	require.NoError(t, err)

	for _, name := range []string{"api", "cache"} {
		t.Run(name, func(t *testing.T) {
			tmpl, mType, err := ContainerAppManifestTemplateForProject(m, name, AppHostOptions{})
			require.Equal(t, ContainerAppManifestTypeBicep, mType)
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

func TestAspireDashboardUrl(t *testing.T) {
	t.Run("container_env_domain", func(t *testing.T) {
		env := environment.NewWithValues("test", map[string]string{
			"AZURE_CONTAINER_APPS_ENVIRONMENT_DEFAULT_DOMAIN": "example.azurecontainerapps.io",
		})
		d := AspireDashboardUrl(t.Context(), env, nil)
		require.NotNil(t, d)
		require.Equal(t, "https://aspire-dashboard.ext.example.azurecontainerapps.io", d.Link)
		// ToString and MarshalJSON
		s := d.ToString("  ")
		require.Contains(t, s, "Aspire Dashboard:")
		b, err := d.MarshalJSON()
		require.NoError(t, err)
		require.Contains(t, string(b), "aspire-dashboard.ext.example")
	})

	t.Run("app_service_dashboard_url", func(t *testing.T) {
		env := environment.NewWithValues("test", map[string]string{
			environment.AppServiceAspireDashboardUrlEnvVarName: "https://dashboard.example.com",
		})
		d := AspireDashboardUrl(t.Context(), env, nil)
		require.NotNil(t, d)
		require.Equal(t, "https://dashboard.example.com", d.Link)
	})

	t.Run("no_env", func(t *testing.T) {
		env := environment.NewWithValues("test", map[string]string{})
		d := AspireDashboardUrl(t.Context(), env, nil)
		require.Nil(t, d)
	})
}

func TestInputMetadata(t *testing.T) {
	lower := true
	upper := false
	cfg := InputDefaultGenerate{
		MinLength:  uintPtr(16),
		Lower:      &lower,
		Upper:      &upper,
		MinNumeric: uintPtr(2),
	}
	s, err := inputMetadata(cfg)
	require.NoError(t, err)
	require.Contains(t, s, "length:16")
	// Lower was true so NoLower should be false -> "minLower" not forced; NoLower exists as false
	require.Contains(t, s, "noLower:false")
	require.Contains(t, s, "noUpper:true")
}

func TestInputMetadata_ClusterLargerThanMin(t *testing.T) {
	// When cluster sum > MinLength, finalLength = cluster sum
	cfg := InputDefaultGenerate{
		MinLength:  uintPtr(4),
		MinLower:   uintPtr(5),
		MinUpper:   uintPtr(6),
		MinNumeric: uintPtr(7),
		MinSpecial: uintPtr(8),
	}
	s, err := inputMetadata(cfg)
	require.NoError(t, err)
	require.Contains(t, s, "length:26")
}

func uintPtr(u uint) *uint { return new(u) }

func TestIsComplexExpression(t *testing.T) {
	cases := []struct {
		name        string
		input       string
		wantComplex bool
		wantVal     string
	}{
		{"simple", "'{{resource.outputs.x}}'", false, "resource.outputs.x"},
		{"complex_multi", "'{{a}}' + '{{b}}'", true, ""},
		{"plain_string", "'hello'", true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			complex, val := isComplexExpression(c.input)
			require.Equal(t, c.wantComplex, complex)
			require.Equal(t, c.wantVal, val)
		})
	}
}

func TestUrlPort(t *testing.T) {
	t.Run("main_http_no_port", func(t *testing.T) {
		p, err := urlPort(&Binding{Scheme: "http"}, true)
		require.NoError(t, err)
		require.Equal(t, "", p)
	})
	t.Run("main_https_no_port", func(t *testing.T) {
		p, err := urlPort(&Binding{Scheme: "https"}, true)
		require.NoError(t, err)
		require.Equal(t, "", p)
	})
	t.Run("port_defined", func(t *testing.T) {
		p, err := urlPort(&Binding{Scheme: "http", Port: new(8080)}, false)
		require.NoError(t, err)
		require.Equal(t, "8080", p)
	})
	t.Run("target_port_fallback", func(t *testing.T) {
		p, err := urlPort(&Binding{Scheme: "tcp", TargetPort: new(5432)}, false)
		require.NoError(t, err)
		require.Equal(t, "5432", p)
	})
	t.Run("templated", func(t *testing.T) {
		p, err := urlPort(&Binding{Scheme: "http"}, false)
		require.NoError(t, err)
		require.Equal(t, acaTemplatedTargetPort, p)
	})
}

func TestBindingPort(t *testing.T) {
	t.Run("main_http", func(t *testing.T) {
		p, err := bindingPort(&Binding{Scheme: "http"}, true)
		require.NoError(t, err)
		require.Equal(t, "80", p)
	})
	t.Run("main_https", func(t *testing.T) {
		p, err := bindingPort(&Binding{Scheme: "https"}, true)
		require.NoError(t, err)
		require.Equal(t, "443", p)
	})
	t.Run("port_priority", func(t *testing.T) {
		p, err := bindingPort(&Binding{Scheme: "tcp", Port: new(9000), TargetPort: new(1)}, false)
		require.NoError(t, err)
		require.Equal(t, "9000", p)
	})
	t.Run("target_port", func(t *testing.T) {
		p, err := bindingPort(&Binding{Scheme: "tcp", TargetPort: new(1234)}, false)
		require.NoError(t, err)
		require.Equal(t, "1234", p)
	})
	t.Run("templated", func(t *testing.T) {
		p, err := bindingPort(&Binding{Scheme: "http"}, false)
		require.NoError(t, err)
		require.Equal(t, acaTemplatedTargetPort, p)
	})
}

func TestUrlPortFromTargetPort(t *testing.T) {
	p, err := urlPortFromTargetPort(&Binding{Scheme: "http"}, true)
	require.NoError(t, err)
	require.Equal(t, "80", p)
	p, err = urlPortFromTargetPort(&Binding{Scheme: "https"}, true)
	require.NoError(t, err)
	require.Equal(t, "443", p)
	p, err = urlPortFromTargetPort(&Binding{TargetPort: new(42)}, false)
	require.NoError(t, err)
	require.Equal(t, "42", p)
	p, err = urlPortFromTargetPort(&Binding{}, false)
	require.NoError(t, err)
	require.Equal(t, acaTemplatedTargetPort, p)
}

func TestAsYamlString(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"true", `"true"`},
		{"hello", "hello"},
	}
	for _, c := range cases {
		got, err := asYamlString(c.in)
		require.NoError(t, err)
		require.Equal(t, c.want, got)
	}
}

func TestUniqueFnvNumber(t *testing.T) {
	a := uniqueFnvNumber("example")
	b := uniqueFnvNumber("example")
	c := uniqueFnvNumber("different")
	require.Equal(t, a, b)
	require.NotEqual(t, a, c)
	require.Len(t, a, 8)
}

func TestInputParameter_NoInputs(t *testing.T) {
	r := &Resource{Value: "just a plain value"}
	in, err := InputParameter("p", r)
	require.NoError(t, err)
	require.Nil(t, in)
}

func TestEvaluateForOutputs_MultipleAndMigration(t *testing.T) {
	value := "{r.outputs.AZURE_CONTAINER_REGISTRY_ENDPOINT} and " +
		"{r.outputs.AZURE_CONTAINER_APPS_ENVIRONMENT_DEFAULT_DOMAIN} and " +
		"{r.outputs.AZURE_APP_SERVICE_DASHBOARD_URI}"
	outs, err := evaluateForOutputs(value, true)
	require.NoError(t, err)
	// well-known environment keys are injected when appHostOwnsCompute=true
	_, ok := outs[environment.ContainerRegistryEndpointEnvVarName]
	require.True(t, ok)
	_, ok = outs[environment.ContainerEnvironmentEndpointEnvVarName]
	require.True(t, ok)
	_, ok = outs[environment.AppServiceAspireDashboardUrlEnvVarName]
	require.True(t, ok)
	// normal uppercase keys also present
	require.Contains(t, outs, "R_AZURE_CONTAINER_REGISTRY_ENDPOINT")
}

func TestEvaluateForOutputs_NoMatches(t *testing.T) {
	outs, err := evaluateForOutputs("just a plain value", false)
	require.NoError(t, err)
	require.Empty(t, outs)
}

func TestEvaluateForOutputs_SecretOutputs(t *testing.T) {
	outs, err := evaluateForOutputs("{resource.secretOutputs.password}", false)
	require.NoError(t, err)
	require.Contains(t, outs, "RESOURCE_PASSWORD")
	require.Equal(t, "resource.secretOutputs.password", outs["RESOURCE_PASSWORD"].Value)
}

func TestInfraGenerator_ValueResource(t *testing.T) {
	g := newInfraGenerator()
	m := &Manifest{Resources: map[string]*Resource{
		"v": {Type: "value.v0", Value: "hello"},
	}}
	require.NoError(t, g.LoadManifest(m))
	require.Equal(t, "hello", g.valueStrings["v"])
}

func TestInfraGenerator_AnnotatedString(t *testing.T) {
	g := newInfraGenerator()
	m := &Manifest{Resources: map[string]*Resource{
		"a": {Type: "annotated.string", Filter: "upper", Value: "x"},
	}}
	require.NoError(t, g.LoadManifest(m))
	require.Equal(t, "upper", g.annotatedStrings["a"].Filter)
	require.Equal(t, "x", g.annotatedStrings["a"].Value)
}

func TestInfraGenerator_ParameterResource(t *testing.T) {
	g := newInfraGenerator()
	m := &Manifest{Resources: map[string]*Resource{
		"pw": {
			Type:  "parameter.v0",
			Value: "{pw.inputs.secret}",
			Inputs: map[string]Input{
				"secret": {Secret: true, Type: "string"},
			},
		},
	}}
	require.NoError(t, g.LoadManifest(m))
	// Compile should succeed with no projects/containers
	require.NoError(t, g.Compile())
	require.Contains(t, g.bicepContext.InputParameters, "pw")
	require.True(t, g.bicepContext.InputParameters["pw"].Secret)
}

func TestInfraGenerator_ConnectionString(t *testing.T) {
	cs := "Server=tcp:{db.outputs.serverName};Database=db"
	g := newInfraGenerator()
	m := &Manifest{Resources: map[string]*Resource{
		"conn": {Type: "value.v0", Value: "placeholder", ConnectionString: &cs},
	}}
	require.NoError(t, g.LoadManifest(m))
	require.Equal(t, cs, g.connectionStrings["conn"])
	// output from connection string should be captured
	require.Contains(t, g.bicepContext.OutputParameters, "DB_SERVERNAME")
}

func TestInfraGenerator_ContainerV0(t *testing.T) {
	g := newInfraGenerator()
	m := &Manifest{Resources: map[string]*Resource{
		"redis": {Type: "container.v0", Image: new("redis:7")},
	}}
	require.NoError(t, g.LoadManifest(m))
	require.NoError(t, g.Compile())
	require.Contains(t, g.buildContainers, "redis")
	require.True(t, g.bicepContext.HasContainerEnvironment)
}

func TestInfraGenerator_DaprComponentRequiresType(t *testing.T) {
	g := newInfraGenerator()
	m := &Manifest{Resources: map[string]*Resource{
		"c": {Type: "dapr.component.v0"},
	}}
	err := g.LoadManifest(m)
	require.Error(t, err)
}

func TestInfraGenerator_DaprFullFlow(t *testing.T) {
	app := "frontend"
	appID := "frontendapp"
	appPort := 3500
	appProto := "http"

	g := newInfraGenerator()
	m := &Manifest{Resources: map[string]*Resource{
		"frontend": {Type: "project.v0", Path: new("/p/f.csproj")},
		"dsidecar": {
			Type: "dapr.v0",
			Dapr: &DaprResourceMetadata{
				Application: &app,
				AppId:       &appID,
				AppPort:     &appPort,
				AppProtocol: &appProto,
			},
		},
	}}
	require.NoError(t, g.LoadManifest(m))
	require.NoError(t, g.Compile())
	require.Contains(t, g.dapr, "dsidecar")
	// Project template context gets Dapr config
	require.Contains(t, g.containerAppTemplateContexts, "frontend")
	require.NotNil(t, g.containerAppTemplateContexts["frontend"].Dapr)
}

func TestInfraGenerator_BicepV0_WithPath(t *testing.T) {
	g := newInfraGenerator()
	m := &Manifest{Resources: map[string]*Resource{
		"mod": {
			Type:   "azure.bicep.v0",
			Path:   new("mod/mod.bicep"),
			Params: map[string]any{"keyVaultName": "", "other": "v"},
		},
	}}
	require.NoError(t, g.LoadManifest(m))
	require.NoError(t, g.Compile())
	require.Contains(t, g.bicepContext.BicepModules, "mod")
	// keyVaultName == "" should trigger auto-injection of a KeyVault
	require.NotEmpty(t, g.bicepContext.KeyVaults)
}

func TestInfraGenerator_IgnoreUnsupportedEnvVar(t *testing.T) {
	t.Setenv("AZD_DEBUG_DOTNET_APPHOST_IGNORE_UNSUPPORTED_RESOURCES", "true")
	g := newInfraGenerator()
	m := &Manifest{Resources: map[string]*Resource{
		"x": {Type: "totally.unknown.v0"},
	}}
	require.NoError(t, g.LoadManifest(m))
}
