// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package test contains an test implementation of provider.Provider. This
// provider is registered for use when this package is imported, and can be imported for
// side effects only to register the provider, e.g.:
package external

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

type ExternalProvider struct {
	envManager  environment.Manager
	env         *environment.Environment
	projectPath string
	options     provisioning.Options
	console     input.Console
	prompters   prompt.Prompter

	extension     *extensions.Extension
	stream        azdext.ProvisioningService_StreamServer
	providerName  string
	responseChans sync.Map
}

// Public methods
func NewExternalProvider(
	name string,
	extension *extensions.Extension,
	stream azdext.ProvisioningService_StreamServer,
	envManager environment.Manager,
	env *environment.Environment,
	console input.Console,
	prompters prompt.Prompter,
) provisioning.Provider {
	provider := &ExternalProvider{
		envManager:   envManager,
		env:          env,
		console:      console,
		prompters:    prompters,
		extension:    extension,
		stream:       stream,
		providerName: name,
	}

	provider.startResponseDispatcher()

	return provider
}

// Name gets the name of the infra provider
func (p *ExternalProvider) Name() string {
	return p.providerName
}

func (p *ExternalProvider) RequiredExternalTools() []tools.ExternalTool {
	return []tools.ExternalTool{}
}

func (p *ExternalProvider) Initialize(ctx context.Context, projectPath string, options provisioning.Options) error {
	requestId := uuid.NewString()

	// Map provisioning.Options to proto ProvisioningOptions
	protoOptions := &azdext.ProvisioningOptions{
		Provider:              string(options.Provider),
		Path:                  options.Path,
		Module:                options.Module,
		DeploymentStacks:      map[string]string{},
		IgnoreDeploymentState: options.IgnoreDeploymentState,
	}

	config, err := structpb.NewStruct(options.Config)
	if err != nil {
		return fmt.Errorf("failed to convert config to struct: %w", err)
	}

	protoOptions.Config = config

	if options.DeploymentStacks != nil {
		for k, v := range options.DeploymentStacks {
			// Only string values are supported in proto map, so convert if possible
			if str, ok := v.(string); ok {
				protoOptions.DeploymentStacks[k] = str
			} else {
				// fallback: use fmt.Sprintf for non-string values
				protoOptions.DeploymentStacks[k] = fmt.Sprintf("%v", v)
			}
		}
	}

	req := &azdext.ProvisioningMessage{
		RequestId: requestId,
		MessageType: &azdext.ProvisioningMessage_InitializeRequest{
			InitializeRequest: &azdext.InitializeRequest{
				ProjectPath: projectPath,
				Options:     protoOptions,
			},
		},
	}
	resp, err := p.sendAndWait(ctx, req, func(r *azdext.ProvisioningMessage) bool {
		return r.GetInitializeResponse() != nil
	})
	if err != nil {
		return err
	}
	result := resp.GetInitializeResponse()
	if result != nil {
		return nil
	}
	return errors.New("unexpected response type")
}

// EnsureEnv ensures that the environment is in a provision-ready state with required values set, prompting the user if
// values are unset.
//
// An environment is considered to be in a provision-ready state if it contains both an AZURE_SUBSCRIPTION_ID and
// AZURE_LOCATION value.
func (t *ExternalProvider) EnsureEnv(ctx context.Context) error {
	requestId := uuid.NewString()
	req := &azdext.ProvisioningMessage{
		RequestId: requestId,
		MessageType: &azdext.ProvisioningMessage_EnsureEnvRequest{
			EnsureEnvRequest: &azdext.EnsureEnvRequest{},
		},
	}
	resp, err := t.sendAndWait(ctx, req, func(r *azdext.ProvisioningMessage) bool {
		return r.GetEnsureEnvResponse() != nil
	})
	if err != nil {
		return err
	}
	result := resp.GetEnsureEnvResponse()
	if result != nil {
		return nil
	}
	return errors.New("unexpected response type")
}

func (p *ExternalProvider) State(ctx context.Context, options *provisioning.StateOptions) (*provisioning.StateResult, error) {
	requestId := uuid.NewString()
	req := &azdext.ProvisioningMessage{
		RequestId: requestId,
		MessageType: &azdext.ProvisioningMessage_StateRequest{
			StateRequest: &azdext.StateRequest{}, // No fields to map from options
		},
	}
	resp, err := p.sendAndWait(ctx, req, func(r *azdext.ProvisioningMessage) bool {
		return r.GetStateResponse() != nil
	})
	if err != nil {
		return nil, err
	}
	result := resp.GetStateResponse()
	if result != nil && result.StateResult != nil {
		protoState := result.StateResult.State
		state := &provisioning.State{
			Outputs:   map[string]provisioning.OutputParameter{},
			Resources: []provisioning.Resource{},
		}
		if protoState != nil {
			for k, v := range protoState.Outputs {
				state.Outputs[k] = provisioning.OutputParameter{
					Type:  provisioning.ParameterType(v.Type),
					Value: v.Value,
				}
			}
			for _, r := range protoState.Resources {
				state.Resources = append(state.Resources, provisioning.Resource{
					Id: r.Id,
				})
			}
		}
		return &provisioning.StateResult{
			State: state,
		}, nil
	}
	return nil, errors.New("unexpected response type")
}

// Provisioning the infrastructure within the specified template
func (p *ExternalProvider) Deploy(ctx context.Context) (*provisioning.DeployResult, error) {
	cleanup := p.wireConsole()
	defer cleanup()

	requestId := uuid.NewString()
	req := &azdext.ProvisioningMessage{
		RequestId: requestId,
		MessageType: &azdext.ProvisioningMessage_DeployRequest{
			DeployRequest: &azdext.DeployRequest{},
		},
	}

	resp, err := p.sendAndWait(ctx, req, func(r *azdext.ProvisioningMessage) bool {
		return r.GetDeployResult() != nil
	})
	if err != nil {
		return nil, err
	}
	result := resp.GetDeployResult()
	if result != nil && result.Result != nil {
		protoDeployment := result.Result.Deployment
		deployment := &provisioning.Deployment{
			Parameters: map[string]provisioning.InputParameter{},
			Outputs:    map[string]provisioning.OutputParameter{},
		}
		if protoDeployment != nil {
			for k, v := range protoDeployment.Parameters {
				deployment.Parameters[k] = provisioning.InputParameter{
					Type:         v.Type,
					DefaultValue: v.DefaultValue,
					Value:        v.Value,
				}
			}
			for k, v := range protoDeployment.Outputs {
				deployment.Outputs[k] = provisioning.OutputParameter{
					Type:  provisioning.ParameterType(v.Type),
					Value: v.Value,
				}
			}
		}
		return &provisioning.DeployResult{
			Deployment:    deployment,
			SkippedReason: provisioning.SkippedReasonType(result.Result.SkippedReason),
		}, nil
	}
	return nil, errors.New("unexpected response type")
}

func (p *ExternalProvider) Preview(ctx context.Context) (*provisioning.DeployPreviewResult, error) {
	requestId := uuid.NewString()
	req := &azdext.ProvisioningMessage{
		RequestId: requestId,
		MessageType: &azdext.ProvisioningMessage_PreviewRequest{
			PreviewRequest: &azdext.PreviewRequest{},
		},
	}
	resp, err := p.sendAndWait(ctx, req, func(r *azdext.ProvisioningMessage) bool {
		return r.GetPreviewResult() != nil
	})
	if err != nil {
		return nil, err
	}
	result := resp.GetPreviewResult()
	if result != nil && result.Result != nil {
		protoPreview := result.Result.Preview
		if protoPreview == nil {
			return &provisioning.DeployPreviewResult{Preview: nil}, nil
		}
		// Map protoPreview to Go DeploymentPreview
		preview := &provisioning.DeploymentPreview{
			Status:     protoPreview.GetSummary(),
			Properties: &provisioning.DeploymentPreviewProperties{
				// No changes field in proto, so leave empty or extend proto if needed
			},
		}
		return &provisioning.DeployPreviewResult{
			Preview: preview,
		}, nil
	}
	return nil, errors.New("unexpected response type")
}

func (p *ExternalProvider) Destroy(
	ctx context.Context,
	options provisioning.DestroyOptions,
) (*provisioning.DestroyResult, error) {
	cleanup := p.wireConsole()
	defer cleanup()

	requestId := uuid.NewString()
	req := &azdext.ProvisioningMessage{
		RequestId: requestId,
		MessageType: &azdext.ProvisioningMessage_DestroyRequest{
			DestroyRequest: &azdext.DestroyRequest{},
		},
	}
	resp, err := p.sendAndWait(ctx, req, func(r *azdext.ProvisioningMessage) bool {
		return r.GetDestroyResult() != nil
	})
	if err != nil {
		return nil, err
	}
	result := resp.GetDestroyResult()
	if result != nil && result.Result != nil {
		return &provisioning.DestroyResult{
			InvalidatedEnvKeys: result.Result.InvalidatedEnvKeys,
		}, nil
	}
	return nil, errors.New("unexpected response type")
}

func (p *ExternalProvider) Parameters(ctx context.Context) ([]provisioning.Parameter, error) {
	requestId := uuid.NewString()
	req := &azdext.ProvisioningMessage{
		RequestId: requestId,
		MessageType: &azdext.ProvisioningMessage_ParametersRequest{
			ParametersRequest: &azdext.ParametersRequest{},
		},
	}
	resp, err := p.sendAndWait(ctx, req, func(r *azdext.ProvisioningMessage) bool {
		return r.GetParametersResponse() != nil
	})
	if err != nil {
		return nil, err
	}
	result := resp.GetParametersResponse()
	if result != nil && result.Parameters != nil {
		params := make([]provisioning.Parameter, 0, len(result.Parameters))
		for _, p := range result.Parameters {
			params = append(params, provisioning.Parameter{
				Name:               p.Name,
				Secret:             p.Secret,
				Value:              p.Value,
				EnvVarMapping:      p.EnvVarMapping,
				LocalPrompt:        p.LocalPrompt,
				UsingEnvVarMapping: p.UsingEnvVarMapping,
			})
		}
		return params, nil
	}
	return nil, errors.New("unexpected response type")
}

// Private struct methods
// helper to send a request and wait for the matching response
func (p *ExternalProvider) sendAndWait(ctx context.Context, req *azdext.ProvisioningMessage, match func(*azdext.ProvisioningMessage) bool) (*azdext.ProvisioningMessage, error) {
	ch := make(chan *azdext.ProvisioningMessage, 1)
	p.responseChans.Store(req.RequestId, ch)
	defer p.responseChans.Delete(req.RequestId)

	if err := p.stream.Send(req); err != nil {
		return nil, err
	}

	for {
		select {
		case resp := <-ch:
			if match(resp) {
				if resp.Error != nil && resp.Error.Message != "" {
					return nil, errors.New(resp.Error.Message)
				}
				return resp, nil
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// goroutine to receive and dispatch responses
func (p *ExternalProvider) startResponseDispatcher() {
	go func() {
		for {
			resp, err := p.stream.Recv()
			if err != nil {
				// propagate error to all waiting calls
				p.responseChans.Range(func(key, value any) bool {
					ch := value.(chan *azdext.ProvisioningMessage)
					close(ch)
					return true
				})
				return
			}
			if ch, ok := p.responseChans.Load(resp.RequestId); ok {
				ch.(chan *azdext.ProvisioningMessage) <- resp
			}
		}
	}()
}

func (p *ExternalProvider) wireConsole() func() {
	stdOut := p.extension.StdOut()
	stdErr := p.extension.StdErr()
	stdOut.AddWriter(p.console.Handles().Stdout)
	stdErr.AddWriter(p.console.Handles().Stderr)

	return func() {
		stdOut.RemoveWriter(p.console.Handles().Stdout)
		stdErr.RemoveWriter(p.console.Handles().Stderr)
	}
}
