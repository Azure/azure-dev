// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"fmt"
	"log"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/structpb"
)

// ExternalProvisioningProvider implements provisioning.Provider by delegating to an extension.
type ExternalProvisioningProvider struct {
	providerName string
	extension    *extensions.Extension
	broker       *grpcbroker.MessageBroker[azdext.ProvisioningMessage]
}

// NewExternalProvisioningProviderFactory returns a DI-compatible factory function.
func NewExternalProvisioningProviderFactory(
	providerName string,
	extension *extensions.Extension,
	broker *grpcbroker.MessageBroker[azdext.ProvisioningMessage],
) func() provisioning.Provider {
	return func() provisioning.Provider {
		return &ExternalProvisioningProvider{
			providerName: providerName,
			extension:    extension,
			broker:       broker,
		}
	}
}

// Name returns the provisioning provider name.
func (p *ExternalProvisioningProvider) Name() string {
	return p.providerName
}

// Initialize initializes the provider with project path and options.
// Note: projectPath validation (path traversal, absolute path checks) is not
// performed here — this matches the existing pattern in service targets and
// framework services. Path validation should be addressed holistically across
// all extension provider types.
func (p *ExternalProvisioningProvider) Initialize(
	ctx context.Context,
	projectPath string,
	options provisioning.Options,
) error {
	protoOptions, err := convertToProtoOptions(options)
	if err != nil {
		return fmt.Errorf(
			"failed to convert provisioning options: %w", err,
		)
	}

	req := &azdext.ProvisioningMessage{
		RequestId: uuid.NewString(),
		MessageType: &azdext.ProvisioningMessage_InitializeRequest{
			InitializeRequest: &azdext.ProvisioningInitializeRequest{
				ProjectPath: projectPath,
				Options:     protoOptions,
			},
		},
	}

	_, err = p.broker.SendAndWait(ctx, req)
	if err != nil {
		return fmt.Errorf(
			"provisioning initialize failed: %w", err,
		)
	}

	return nil
}

// State returns the current state of provisioned infrastructure.
func (p *ExternalProvisioningProvider) State(
	ctx context.Context,
	options *provisioning.StateOptions,
) (*provisioning.StateResult, error) {
	var hint string
	if options != nil {
		hint = options.Hint()
	}

	req := &azdext.ProvisioningMessage{
		RequestId: uuid.NewString(),
		MessageType: &azdext.ProvisioningMessage_StateRequest{
			StateRequest: &azdext.ProvisioningStateRequest{
				ProviderName: p.providerName,
				Options: &azdext.ProvisioningStateOptions{
					Hint: hint,
				},
			},
		},
	}

	resp, err := p.broker.SendAndWait(ctx, req)
	if err != nil {
		return nil, fmt.Errorf(
			"provisioning state failed: %w", err,
		)
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
			DeployRequest: &azdext.ProvisioningDeployRequest{
				ProviderName: p.providerName,
			},
		},
	}

	// TODO: Route extension progress to the CLI interactive progress display.
	// Currently progress is only logged; built-in providers surface through console formatter.
	resp, err := p.broker.SendAndWaitWithProgress(ctx, req, func(msg string) {
		log.Printf("provisioning progress: %s", msg)
	})
	if err != nil {
		return nil, fmt.Errorf(
			"provisioning deploy failed: %w", err,
		)
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
			PreviewRequest: &azdext.ProvisioningPreviewRequest{
				ProviderName: p.providerName,
			},
		},
	}

	// TODO: Route to console progress display
	resp, err := p.broker.SendAndWaitWithProgress(ctx, req, func(msg string) {
		log.Printf("provisioning preview progress: %s", msg)
	})
	if err != nil {
		return nil, fmt.Errorf(
			"provisioning preview failed: %w", err,
		)
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
				ProviderName: p.providerName,
				Options: &azdext.ProvisioningDestroyOptions{
					Force: options.Force(),
					Purge: options.Purge(),
				},
			},
		},
	}

	// TODO: Route to console progress display
	resp, err := p.broker.SendAndWaitWithProgress(ctx, req, func(msg string) {
		log.Printf("provisioning destroy progress: %s", msg)
	})
	if err != nil {
		return nil, fmt.Errorf(
			"provisioning destroy failed: %w", err,
		)
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
			EnsureEnvRequest: &azdext.ProvisioningEnsureEnvRequest{
				ProviderName: p.providerName,
			},
		},
	}

	_, err := p.broker.SendAndWait(ctx, req)
	if err != nil {
		return fmt.Errorf(
			"provisioning ensure env failed: %w", err,
		)
	}

	return nil
}

// Parameters returns the provisioning parameters.
func (p *ExternalProvisioningProvider) Parameters(
	ctx context.Context,
) ([]provisioning.Parameter, error) {
	req := &azdext.ProvisioningMessage{
		RequestId: uuid.NewString(),
		MessageType: &azdext.ProvisioningMessage_ParametersRequest{
			ParametersRequest: &azdext.ProvisioningParametersRequest{
				ProviderName: p.providerName,
			},
		},
	}

	resp, err := p.broker.SendAndWait(ctx, req)
	if err != nil {
		return nil, fmt.Errorf(
			"provisioning parameters failed: %w", err,
		)
	}

	paramsResp := resp.GetParametersResponse()
	if paramsResp == nil {
		return []provisioning.Parameter{}, nil
	}

	return convertFromProtoParameters(paramsResp.Parameters), nil
}

// PlannedOutputs returns planned outputs from the extension provider.
func (p *ExternalProvisioningProvider) PlannedOutputs(
	ctx context.Context,
) ([]provisioning.PlannedOutput, error) {
	req := &azdext.ProvisioningMessage{
		RequestId: uuid.NewString(),
		MessageType: &azdext.ProvisioningMessage_PlannedOutputsRequest{
			PlannedOutputsRequest: &azdext.ProvisioningPlannedOutputsRequest{
				ProviderName: p.providerName,
			},
		},
	}

	resp, err := p.broker.SendAndWait(ctx, req)
	if err != nil {
		return nil, fmt.Errorf(
			"provisioning planned outputs failed: %w", err,
		)
	}

	outputsResp := resp.GetPlannedOutputsResponse()
	if outputsResp == nil {
		return []provisioning.PlannedOutput{}, nil
	}

	result := make(
		[]provisioning.PlannedOutput,
		len(outputsResp.PlannedOutputs),
	)
	for i, o := range outputsResp.PlannedOutputs {
		result[i] = provisioning.PlannedOutput{Name: o.Name}
	}
	return result, nil
}

// --- Conversion helpers ---

func convertToProtoOptions(
	options provisioning.Options,
) (*azdext.ProvisioningOptions, error) {
	deploymentStacks := make(
		map[string]string, len(options.DeploymentStacks),
	)
	for k, v := range options.DeploymentStacks {
		deploymentStacks[k] = fmt.Sprintf("%v", v)
	}

	protoOptions := &azdext.ProvisioningOptions{
		Provider:              string(options.Provider),
		Path:                  options.Path,
		Module:                options.Module,
		DeploymentStacks:      deploymentStacks,
		IgnoreDeploymentState: options.IgnoreDeploymentState,
		Name:                  options.Name,
	}

	// Convert Config to protobuf Struct if present
	if options.Config != nil {
		s, err := structpb.NewStruct(options.Config)
		if err != nil {
			return nil, fmt.Errorf(
				"failed to convert config to protobuf struct: %w",
				err,
			)
		}
		protoOptions.Config = s
	}

	if options.VirtualEnv != nil {
		protoOptions.VirtualEnv = options.VirtualEnv
	}

	return protoOptions, nil
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
		if v == nil {
			continue
		}
		state.Outputs[k] = provisioning.OutputParameter{
			Type:  provisioning.ParameterType(v.Type),
			Value: v.Value,
		}
	}

	for _, r := range result.State.Resources {
		if r == nil {
			continue
		}
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
			if v == nil {
				continue
			}
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
			if v == nil {
				continue
			}
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

	// Resource-level change details (created/modified/deleted) are not yet supported
	// for extension providers. The proto can be extended with a repeated changes field
	// when this capability is needed.
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
	result := make([]provisioning.Parameter, len(params))
	for i, p := range params {
		result[i] = provisioning.Parameter{
			Name:               p.Name,
			Secret:             p.Secret,
			Value:              p.Value,
			EnvVarMapping:      p.EnvVarMapping,
			LocalPrompt:        p.LocalPrompt,
			UsingEnvVarMapping: p.UsingEnvVarMapping,
		}
	}
	return result
}
