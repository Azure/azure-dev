// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
)

// Ensure ScriptProvisioningProvider implements ProvisioningProvider interface.
var _ azdext.ProvisioningProvider = (*ScriptProvisioningProvider)(nil)

// ScriptProvisioningProvider implements azdext.ProvisioningProvider by delegating to
// user-configured shell scripts (bash/pwsh) for provisioning and teardown.
type ScriptProvisioningProvider struct {
	azdClient   *azdext.AzdClient
	projectPath string
	config      *ProviderConfig
	outputs     map[string]OutputParameter
}

// NewScriptProvisioningProvider creates a new ScriptProvisioningProvider instance.
func NewScriptProvisioningProvider(azdClient *azdext.AzdClient) *ScriptProvisioningProvider {
	return &ScriptProvisioningProvider{
		azdClient: azdClient,
		outputs:   make(map[string]OutputParameter),
	}
}

func (p *ScriptProvisioningProvider) Initialize(
	ctx context.Context,
	projectPath string,
	options *azdext.ProvisioningOptions,
) error {
	p.projectPath = projectPath

	if options.GetConfig() != nil {
		cfg, err := ParseProviderConfig(options.GetConfig().AsMap())
		if err != nil {
			return err
		}

		if err := cfg.Validate(projectPath); err != nil {
			return err
		}

		p.config = cfg
	}

	return nil
}

func (p *ScriptProvisioningProvider) State(
	ctx context.Context,
	options *azdext.ProvisioningStateOptions,
) (*azdext.ProvisioningStateResult, error) {
	protoOutputs := make(map[string]*azdext.ProvisioningOutputParameter, len(p.outputs))
	for k, v := range p.outputs {
		protoOutputs[k] = &azdext.ProvisioningOutputParameter{
			Type:  v.Type,
			Value: v.Value,
		}
	}

	return &azdext.ProvisioningStateResult{
		State: &azdext.ProvisioningState{
			Outputs: protoOutputs,
		},
	}, nil
}

func (p *ScriptProvisioningProvider) Deploy(
	ctx context.Context,
	progress grpcbroker.ProgressFunc,
) (*azdext.ProvisioningDeployResult, error) {
	if p.config == nil || len(p.config.Provision) == 0 {
		progress("No provision scripts configured, skipping")
		return &azdext.ProvisioningDeployResult{}, nil
	}

	outputs, err := p.runScripts(ctx, p.config.Provision, progress)
	if err != nil {
		return nil, fmt.Errorf("provisioning failed: %w", err)
	}

	p.outputs = MergeOutputs(p.outputs, outputs)

	protoOutputs := make(map[string]*azdext.ProvisioningOutputParameter, len(p.outputs))
	for k, v := range p.outputs {
		protoOutputs[k] = &azdext.ProvisioningOutputParameter{
			Type:  v.Type,
			Value: v.Value,
		}
	}

	return &azdext.ProvisioningDeployResult{
		Deployment: &azdext.ProvisioningDeployment{
			Outputs: protoOutputs,
		},
	}, nil
}

func (p *ScriptProvisioningProvider) Preview(
	ctx context.Context,
	progress grpcbroker.ProgressFunc,
) (*azdext.ProvisioningPreviewResult, error) {
	scriptCount := 0
	if p.config != nil {
		scriptCount = len(p.config.Provision)
	}

	return &azdext.ProvisioningPreviewResult{
		Preview: &azdext.ProvisioningDeploymentPreview{
			Summary: fmt.Sprintf("Will execute %d provision script(s)", scriptCount),
		},
	}, nil
}

func (p *ScriptProvisioningProvider) Destroy(
	ctx context.Context,
	options *azdext.ProvisioningDestroyOptions,
	progress grpcbroker.ProgressFunc,
) (*azdext.ProvisioningDestroyResult, error) {
	if p.config == nil || len(p.config.Destroy) == 0 {
		progress("No destroy scripts configured, skipping")
		return &azdext.ProvisioningDestroyResult{}, nil
	}

	_, err := p.runScripts(ctx, p.config.Destroy, progress)
	if err != nil {
		return nil, fmt.Errorf("destroy failed: %w", err)
	}

	// Collect keys that were produced by provisioning to invalidate them
	invalidatedKeys := make([]string, 0, len(p.outputs))
	for k := range p.outputs {
		invalidatedKeys = append(invalidatedKeys, k)
	}

	return &azdext.ProvisioningDestroyResult{
		InvalidatedEnvKeys: invalidatedKeys,
	}, nil
}

func (p *ScriptProvisioningProvider) EnsureEnv(ctx context.Context) error {
	return nil
}

func (p *ScriptProvisioningProvider) Parameters(
	ctx context.Context,
) ([]*azdext.ProvisioningParameter, error) {
	return nil, nil
}

func (p *ScriptProvisioningProvider) PlannedOutputs(
	ctx context.Context,
) ([]*azdext.ProvisioningPlannedOutput, error) {
	return nil, nil
}

// runScripts executes a list of scripts sequentially, collecting outputs from each.
func (p *ScriptProvisioningProvider) runScripts(
	ctx context.Context,
	scripts []*ScriptConfig,
	progress grpcbroker.ProgressFunc,
) (map[string]OutputParameter, error) {
	azdEnv, err := p.getAzdEnv(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading azd environment: %w", err)
	}

	resolver := NewEnvResolver(azdEnv)
	executor := NewScriptExecutor(p.projectPath)
	collector := NewOutputCollector(p.projectPath)

	allOutputs := make(map[string]OutputParameter)

	for i, sc := range scripts {
		progress(fmt.Sprintf("Running %s (%d/%d)", sc.Name, i+1, len(scripts)))

		env, err := resolver.Resolve(sc)
		if err != nil {
			return nil, fmt.Errorf("resolving environment for %s: %w", sc.Name, err)
		}

		_, execErr := executor.Execute(ctx, sc, env)
		if execErr != nil && !sc.ContinueOnError {
			return nil, execErr
		}

		outputs, err := collector.Collect(sc)
		if err != nil {
			return nil, fmt.Errorf("collecting outputs for %s: %w", sc.Name, err)
		}

		if outputs != nil {
			resolver.MergeOutputs(outputs)
			allOutputs = MergeOutputs(allOutputs, outputs)
		}
	}

	return allOutputs, nil
}

// getAzdEnv retrieves current azd environment values via the gRPC client.
func (p *ScriptProvisioningProvider) getAzdEnv(ctx context.Context) (map[string]string, error) {
	resp, err := p.azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{})
	if err != nil {
		// If environment is not available, return empty map
		return make(map[string]string), nil
	}

	result := make(map[string]string, len(resp.GetKeyValues()))
	for _, kv := range resp.GetKeyValues() {
		result[kv.GetKey()] = kv.GetValue()
	}

	return result, nil
}
