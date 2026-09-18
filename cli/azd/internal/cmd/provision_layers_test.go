// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type layeredProvisionRecord struct {
	dependsOn      []string
	observedVnetID string
}

type layeredProvisionRecorder struct {
	mu      sync.Mutex
	env     *environment.Environment
	records map[string]layeredProvisionRecord
}

func (r *layeredProvisionRecorder) record(options provisioning.Options) string {
	r.mu.Lock()
	defer r.mu.Unlock()

	observedVnetID := r.env.Getenv("VNET_ID")
	r.records[options.Name] = layeredProvisionRecord{
		dependsOn:      slices.Clone(options.DependsOn),
		observedVnetID: observedVnetID,
	}
	return observedVnetID
}

func (r *layeredProvisionRecorder) snapshot() map[string]layeredProvisionRecord {
	r.mu.Lock()
	defer r.mu.Unlock()

	return maps.Clone(r.records)
}

type layeredProvisionProvider struct {
	recorder *layeredProvisionRecorder
	options  provisioning.Options
}

func (p *layeredProvisionProvider) Name() string { return string(provisioning.Bicep) }

func (p *layeredProvisionProvider) Initialize(
	_ context.Context,
	_ string,
	options provisioning.Options,
) error {
	p.options = options
	return nil
}

func (p *layeredProvisionProvider) State(
	context.Context,
	*provisioning.StateOptions,
) (*provisioning.StateResult, error) {
	return nil, nil
}

func (p *layeredProvisionProvider) Deploy(context.Context) (*provisioning.DeployResult, error) {
	observedVnetID := p.recorder.record(p.options)
	if p.options.Name != "network" && observedVnetID != "vnet-123" {
		return nil, fmt.Errorf("layer %q started before VNET_ID was available", p.options.Name)
	}

	outputs := map[string]provisioning.OutputParameter{}
	if p.options.Name == "network" {
		outputs["VNET_ID"] = provisioning.OutputParameter{
			Type:  provisioning.ParameterTypeString,
			Value: "vnet-123",
		}
	}
	return &provisioning.DeployResult{
		Deployment: &provisioning.Deployment{Outputs: outputs},
	}, nil
}

func (*layeredProvisionProvider) Preview(
	context.Context,
) (*provisioning.DeployPreviewResult, error) {
	return nil, nil
}

func (*layeredProvisionProvider) Destroy(
	context.Context,
	provisioning.DestroyOptions,
) (*provisioning.DestroyResult, error) {
	return nil, nil
}

func (*layeredProvisionProvider) EnsureEnv(context.Context) error { return nil }

func (*layeredProvisionProvider) Parameters(context.Context) ([]provisioning.Parameter, error) {
	return nil, nil
}

func (*layeredProvisionProvider) PlannedOutputs(context.Context) ([]provisioning.PlannedOutput, error) {
	return nil, nil
}

type offlineResourceManager struct {
	project.ResourceManager
}

func (*offlineResourceManager) GetResourceGroupName(
	context.Context,
	string,
	osutil.ExpandableString,
) (string, error) {
	return "rg-layered-test", nil
}

type layeredProvisionTest struct {
	action         *ProvisionAction
	env            *environment.Environment
	envManager     environment.Manager
	providerCalls  *layeredProvisionRecorder
	serviceManager *recordingServiceManager
}

func newLayeredProvisionTest(t *testing.T, projectConfig *project.ProjectConfig) *layeredProvisionTest {
	t.Helper()
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())
	t.Setenv("AZURE_DEV_COLLECT_TELEMETRY", "no")
	t.Setenv("NO_COLOR", "1")
	t.Setenv("AZD_FORCE_TTY", "false")

	mockContext := mocks.NewMockContext(t.Context())
	azdContext := azdcontext.NewAzdContextWithDirectory(projectConfig.Path)
	localDataStore := environment.NewLocalFileDataStore(
		azdContext,
		config.NewFileConfigManager(config.NewManager()),
	)
	envManager, err := environment.NewManager(
		mockContext.Container,
		azdContext,
		mockContext.Console,
		localDataStore,
		nil,
	)
	require.NoError(t, err)

	env := environment.New("layered-test")
	env.SetSubscriptionId("00000000-0000-0000-0000-000000000000")
	env.SetLocation("eastus2")
	require.NoError(t, envManager.Save(t.Context(), env))

	providerCalls := &layeredProvisionRecorder{
		env:     env,
		records: map[string]layeredProvisionRecord{},
	}
	// Keep the container deliberately bare: resolving the named provider reaches this fake,
	// while any accidental Azure client or Bicep CLI resolution fails the test.
	mockContext.Container.MustRegisterNamedTransient(
		string(provisioning.Bicep),
		func() provisioning.Provider {
			return &layeredProvisionProvider{recorder: providerCalls}
		},
	)

	defaultProvider := func() (provisioning.ProviderKind, error) {
		return provisioning.Bicep, nil
	}
	features := alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig())
	testCloud := cloud.AzurePublic()
	provisionManager := provisioning.NewManager(
		mockContext.Container,
		defaultProvider,
		envManager,
		env,
		mockContext.Console,
		features,
		nil,
		testCloud,
	)
	serviceManager := &recordingServiceManager{}
	importManager := project.NewImportManager(nil)
	projectManager := project.NewProjectManager(azdContext, serviceManager, importManager)

	return &layeredProvisionTest{
		action: &ProvisionAction{
			flags: &ProvisionFlags{
				global:  &internal.GlobalCommandOptions{},
				EnvFlag: &internal.EnvFlag{},
			},
			provisionManager:    provisionManager,
			projectManager:      projectManager,
			resourceManager:     &offlineResourceManager{},
			env:                 env,
			envManager:          envManager,
			formatter:           &output.NoneFormatter{},
			projectConfig:       projectConfig,
			writer:              io.Discard,
			console:             mockContext.Console,
			commandRunner:       mockContext.CommandRunner,
			serviceLocator:      mockContext.Container,
			importManager:       importManager,
			alphaFeatureManager: features,
			portalUrlBase:       testCloud.PortalUrlBase,
			defaultProvider:     defaultProvider,
			cloud:               testCloud,
		},
		env:            env,
		envManager:     envManager,
		providerCalls:  providerCalls,
		serviceManager: serviceManager,
	}
}

func TestProvisionAction_TopLevelLayersFromAzureYaml(t *testing.T) {
	projectDir := t.TempDir()
	writeLayeredProvisionProject(t, projectDir)

	projectConfig, err := project.Load(t.Context(), filepath.Join(projectDir, "azure.yaml"))
	require.NoError(t, err)
	require.Equal(t, project.ProjectFormatLayersV2, projectConfig.Format())
	fixture := newLayeredProvisionTest(t, projectConfig)

	spanRecorder := tracetest.NewSpanRecorder()
	tracerProvider := tracesdk.NewTracerProvider(tracesdk.WithSpanProcessor(spanRecorder))
	ctx, span := tracerProvider.Tracer("test").Start(t.Context(), "provision")
	result, err := fixture.action.Run(ctx)
	span.End()
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(t, result.Message.Header, "Your application was provisioned")
	require.Empty(t, fixture.serviceManager.initializedServices)

	records := fixture.providerCalls.snapshot()
	require.Len(t, records, 3)
	assert.Contains(t, records, "network")
	assert.Equal(t, "vnet-123", records["compute"].observedVnetID)
	assert.Equal(t, "vnet-123", records["monitoring"].observedVnetID)
	assert.Equal(t, []string{"network"}, records["monitoring"].dependsOn)

	require.NoError(t, fixture.envManager.Reload(t.Context(), fixture.env))
	assert.Equal(t, "vnet-123", fixture.env.Getenv("VNET_ID"))

	require.Len(t, spanRecorder.Ended(), 1)
	assert.Equal(t, map[string]any{
		string(fields.ProvisionLayerIsV2Key.Key):                   true,
		string(fields.ProvisionLayerCountKey.Key):                  int64(3),
		string(fields.ProvisionLayerExplicitDependsOnCountKey.Key): int64(1),
		string(fields.ProvisionLayerMaxParallelKey.Key):            int64(2),
		string(fields.ProvisionLayerSafeFallbackCountKey.Key):      int64(0),
	}, provisionLayerAttributes(spanRecorder.Ended()[0]))
}

func TestProvisionLayersGraph_AnalysisFailureOmitsTopologyTelemetry(t *testing.T) {
	projectDir := t.TempDir()
	projectConfig := &project.ProjectConfig{
		Path: projectDir,
		Layers: project.LayerConfigs{
			{
				Name:  "foundation",
				Infra: []provisioning.Options{{Name: "network", Provider: provisioning.Bicep, Path: "missing/network"}},
			},
			{
				Name:      "application",
				DependsOn: []string{"foundation"},
				Infra:     []provisioning.Options{{Name: "api", Provider: provisioning.Bicep, Path: "missing/api"}},
			},
		},
	}
	layers := projectConfig.InfrastructureConfigs()
	fixture := newLayeredProvisionTest(t, projectConfig)

	spanRecorder := tracetest.NewSpanRecorder()
	tracerProvider := tracesdk.NewTracerProvider(tracesdk.WithSpanProcessor(spanRecorder))
	ctx, span := tracerProvider.Tracer("test").Start(t.Context(), "provision")
	result, err := fixture.action.provisionLayersGraph(ctx, layers, time.Now(), false)
	span.End()

	require.ErrorContains(t, err, "analyzing layer dependencies")
	require.Nil(t, result)
	require.Len(t, spanRecorder.Ended(), 1)
	assert.Equal(t, map[string]any{
		string(fields.ProvisionLayerIsV2Key.Key):                   true,
		string(fields.ProvisionLayerCountKey.Key):                  int64(2),
		string(fields.ProvisionLayerExplicitDependsOnCountKey.Key): int64(1),
	}, provisionLayerAttributes(spanRecorder.Ended()[0]))
}

func writeLayeredProvisionProject(t *testing.T, projectDir string) {
	t.Helper()

	files := map[string]string{
		"azure.yaml": `name: layered-test
layers:
  - name: foundation
    infra:
      - name: network
        provider: bicep
        path: infra/network
  - name: application
    infra:
      - name: compute
        provider: bicep
        path: infra/compute
  - name: operations
    dependsOn:
      - foundation
    infra:
      - name: monitoring
        provider: bicep
        path: infra/monitoring
`,
		"infra/network/main.bicep": `output VNET_ID string = 'vnet-123'
`,
		"infra/compute/main.bicep": `param vnetId string
`,
		"infra/compute/main.bicepparam": `using 'main.bicep'
param vnetId = readEnvironmentVariable('VNET_ID')
`,
		"infra/monitoring/main.bicep": `param location string
`,
	}

	for name, contents := range files {
		path := filepath.Join(projectDir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
	}
}
