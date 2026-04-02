// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"fmt"
	"log"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/structpb"
)

// ExternalProvisioningProvider implements provisioning.Provider by delegating to an extension.
type ExternalProvisioningProvider struct {
	providerName string
	extension    *extensions.Extension
	broker       *grpcbroker.MessageBroker[azdext.ProvisioningMessage]
	envManager   environment.Manager
	env          *environment.Environment
}

// NewExternalProvisioningProviderFactory returns a DI-compatible factory function.
func NewExternalProvisioningProviderFactory(
	providerName string,
	extension *extensions.Extension,
	broker *grpcbroker.MessageBroker[azdext.ProvisioningMessage],
	lazyEnv *lazy.Lazy[*environment.Environment],
) func(envManager environment.Manager) provisioning.Provider {
	return func(envManager environment.Manager) provisioning.Provider {
		env, _ := lazyEnv.GetValue()
		return &ExternalProvisioningProvider{
			providerName: providerName,
			extension:    extension,
			broker:       broker,
			envManager:   envManager,
			env:          env,
		}
	}
}

// Name returns the provisioning provider name.
func (p *ExternalProvisioningProvider) Name() string {
	return p.providerName
}

// Initialize initializes the provider with project path and options.
func (p *ExternalProvisioningProvider) Initialize(
	ctx context.Context,
	projectPath string,
	options provisioning.Options,
) error {
	protoOptions := convertToProtoOptions(options)

	req := &azdext.ProvisioningMessage{
		RequestId: uuid.NewString(),
		MessageType: &azdext.ProvisioningMessage_InitializeRequest{
			InitializeRequest: &azdext.ProvisioningInitializeRequest{
				ProjectPath: projectPath,
				Options:     protoOptions,
			},
		},
	}

	_, err := p.broker.SendAndWait(ctx, req)
	return err
}

// State returns the current state of provisioned infrastructure.
func (p *ExternalProvisioningProvider) State(
	ctx context.Context,
	options *provisioning.StateOptions,
) (*provisioning.StateResult, error) {
	req := &azdext.ProvisioningMessage{
		RequestId: uuid.NewString(),
		MessageType: &azdext.ProvisioningMessage_StateRequest{
			StateRequest: &azdext.ProvisioningStateRequest{
				Options: &azdext.ProvisioningStateOptions{},
			},
		},
	}

	resp, err := p.broker.SendAndWait(ctx, req)
	if err != nil {
		return nil, err
	}

	stateResp := resp.GetStateResponse()
	if stateResp == nil || stateResp.StateResult == nil {
		return &provisioning.StateResult{}, nil
	}

	return convertFromProtoStateResult(stateResp.StateResult), nil
}

// Deploy performs the provisioning deployment.
func (p *ExternalProvisioningProvider) Deploy(
	ctx context.Context,
) (*provisioning.DeployResult, error) {
	req := &azdext.ProvisioningMessage{
		RequestId: uuid.NewString(),
		MessageType: &azdext.ProvisioningMessage_DeployRequest{
			DeployRequest: &azdext.ProvisioningDeployRequest{},
		},
	}

	resp, err := p.broker.SendAndWaitWithProgress(ctx, req, func(msg string) {
		log.Printf("provisioning progress: %s", msg)
	})
	if err != nil {
		return nil, err
	}

	deployResp := resp.GetDeployResponse()
	if deployResp == nil || deployResp.Result == nil {
		return &provisioning.DeployResult{}, nil
	}

	return convertFromProtoDeployResult(deployResp.Result), nil
}

// Preview returns a preview of what a deployment would change.
func (p *ExternalProvisioningProvider) Preview(
	ctx context.Context,
) (*provisioning.DeployPreviewResult, error) {
	req := &azdext.ProvisioningMessage{
		RequestId: uuid.NewString(),
		MessageType: &azdext.ProvisioningMessage_PreviewRequest{
			PreviewRequest: &azdext.ProvisioningPreviewRequest{},
		},
	}

	resp, err := p.broker.SendAndWaitWithProgress(ctx, req, func(msg string) {
		log.Printf("provisioning preview progress: %s", msg)
	})
	if err != nil {
		return nil, err
	}

	previewResp := resp.GetPreviewResponse()
	if previewResp == nil || previewResp.Result == nil {
		return &provisioning.DeployPreviewResult{}, nil
	}

	return convertFromProtoPreviewResult(previewResp.Result), nil
}

// Destroy destroys the provisioned infrastructure.
func (p *ExternalProvisioningProvider) Destroy(
	ctx context.Context,
	options provisioning.DestroyOptions,
) (*provisioning.DestroyResult, error) {
	req := &azdext.ProvisioningMessage{
		RequestId: uuid.NewString(),
		MessageType: &azdext.ProvisioningMessage_DestroyRequest{
			DestroyRequest: &azdext.ProvisioningDestroyRequest{
				Options: &azdext.ProvisioningDestroyOptions{
					Force: options.Force(),
					Purge: options.Purge(),
				},
			},
		},
	}

	resp, err := p.broker.SendAndWaitWithProgress(ctx, req, func(msg string) {
		log.Printf("provisioning destroy progress: %s", msg)
	})
	if err != nil {
		return nil, err
	}

	destroyResp := resp.GetDestroyResponse()
	if destroyResp == nil || destroyResp.Result == nil {
		return &provisioning.DestroyResult{}, nil
	}

	return &provisioning.DestroyResult{
		InvalidatedEnvKeys: destroyResp.Result.InvalidatedEnvKeys,
	}, nil
}

// EnsureEnv ensures the environment is configured properly.
func (p *ExternalProvisioningProvider) EnsureEnv(ctx context.Context) error {
	req := &azdext.ProvisioningMessage{
		RequestId: uuid.NewString(),
		MessageType: &azdext.ProvisioningMessage_EnsureEnvRequest{
			EnsureEnvRequest: &azdext.ProvisioningEnsureEnvRequest{},
		},
	}

	_, err := p.broker.SendAndWait(ctx, req)
	return err
}

// Parameters returns the provisioning parameters.
func (p *ExternalProvisioningProvider) Parameters(
	ctx context.Context,
) ([]provisioning.Parameter, error) {
	req := &azdext.ProvisioningMessage{
		RequestId: uuid.NewString(),
		MessageType: &azdext.ProvisioningMessage_ParametersRequest{
			ParametersRequest: &azdext.ProvisioningParametersRequest{},
		},
	}

	resp, err := p.broker.SendAndWait(ctx, req)
	if err != nil {
		return nil, err
	}

	paramsResp := resp.GetParametersResponse()
	if paramsResp == nil {
		return nil, nil
	}

	return convertFromProtoParameters(paramsResp.Parameters), nil
}

// PlannedOutputs returns planned outputs. Not yet supported for external providers.
func (p *ExternalProvisioningProvider) PlannedOutputs(
	ctx context.Context,
) ([]provisioning.PlannedOutput, error) {
	return nil, nil
}

// --- Conversion helpers ---

func convertToProtoOptions(
	options provisioning.Options,
) *azdext.ProvisioningOptions {
	deploymentStacks := make(map[string]string, len(options.DeploymentStacks))
	for k, v := range options.DeploymentStacks {
		deploymentStacks[k] = fmt.Sprintf("%v", v)
	}

	protoOptions := &azdext.ProvisioningOptions{
		Provider:              string(options.Provider),
		Path:                  options.Path,
		Module:                options.Module,
		DeploymentStacks:      deploymentStacks,
		IgnoreDeploymentState: options.IgnoreDeploymentState,
	}

	// Convert Config to protobuf Struct if present
	if options.Config != nil {
		if s, err := structpb.NewStruct(options.Config); err == nil {
			protoOptions.Config = s
		}
	}

	return protoOptions
}

func convertFromProtoStateResult(
	result *azdext.ProvisioningStateResult,
) *provisioning.StateResult {
	if result == nil || result.State == nil {
		return &provisioning.StateResult{}
	}

	state := &provisioning.State{
		Outputs:   make(map[string]provisioning.OutputParameter, len(result.State.Outputs)),
		Resources: make([]provisioning.Resource, 0, len(result.State.Resources)),
	}

	for k, v := range result.State.Outputs {
		state.Outputs[k] = provisioning.OutputParameter{
			Type:  provisioning.ParameterType(v.Type),
			Value: v.Value,
		}
	}

	for _, r := range result.State.Resources {
		state.Resources = append(state.Resources, provisioning.Resource{Id: r.Id})
	}

	return &provisioning.StateResult{State: state}
}

func convertFromProtoDeployResult(
	result *azdext.ProvisioningDeployResult,
) *provisioning.DeployResult {
	deployResult := &provisioning.DeployResult{}

	if result.Deployment != nil {
		deployment := &provisioning.Deployment{
			Parameters: make(
				map[string]provisioning.InputParameter,
				len(result.Deployment.Parameters),
			),
			Outputs: make(
				map[string]provisioning.OutputParameter,
				len(result.Deployment.Outputs),
			),
		}

		for k, v := range result.Deployment.Parameters {
			param := provisioning.InputParameter{
				Type:  v.Type,
				Value: v.Value,
			}
			if v.DefaultValue != "" {
				param.DefaultValue = v.DefaultValue
			}
			deployment.Parameters[k] = param
		}

		for k, v := range result.Deployment.Outputs {
			deployment.Outputs[k] = provisioning.OutputParameter{
				Type:  provisioning.ParameterType(v.Type),
				Value: v.Value,
			}
		}

		deployResult.Deployment = deployment
	}

	//nolint:exhaustive
	switch result.SkippedReason {
	case azdext.ProvisioningSkippedReason_PROVISIONING_SKIPPED_REASON_DEPLOYMENT_STATE:
		deployResult.SkippedReason = provisioning.DeploymentStateSkipped
	}

	return deployResult
}

func convertFromProtoPreviewResult(
	result *azdext.ProvisioningPreviewResult,
) *provisioning.DeployPreviewResult {
	if result == nil || result.Preview == nil {
		return &provisioning.DeployPreviewResult{}
	}

	preview := &provisioning.DeploymentPreview{
		Status: result.Preview.Summary,
		Properties: &provisioning.DeploymentPreviewProperties{
			Changes: []*provisioning.DeploymentPreviewChange{},
		},
	}

	return &provisioning.DeployPreviewResult{Preview: preview}
}

func convertFromProtoParameters(
	params []*azdext.ProvisioningParameter,
) []provisioning.Parameter {
	result := make([]provisioning.Parameter, 0, len(params))
	for _, p := range params {
		result = append(result, provisioning.Parameter{
			Name:               p.Name,
			Secret:             p.Secret,
			Value:              p.Value,
			EnvVarMapping:      p.EnvVarMapping,
			LocalPrompt:        p.LocalPrompt,
			UsingEnvVarMapping: p.UsingEnvVarMapping,
		})
	}
	return result
}
