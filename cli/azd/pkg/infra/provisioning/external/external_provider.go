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

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
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

	stream        azdext.ProvisioningService_StreamServer
	providerName  string
	responseChans sync.Map
}

// Public methods
func NewExternalProvider(
	name string,
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
		_, ok := r.MessageType.(*azdext.ProvisioningMessage_InitializeResponse)
		return ok
	})
	if err != nil {
		return err
	}
	if r, ok := resp.MessageType.(*azdext.ProvisioningMessage_InitializeResponse); ok {
		if !r.InitializeResponse.Success {
			return errors.New(r.InitializeResponse.ErrorMessage)
		}
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
	return provisioning.EnsureSubscriptionAndLocation(
		ctx,
		t.envManager,
		t.env,
		t.prompters,
		provisioning.EnsureSubscriptionAndLocationOptions{},
	)
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
		_, ok := r.MessageType.(*azdext.ProvisioningMessage_StateResponse)
		return ok
	})
	if err != nil {
		return nil, err
	}
	if r, ok := resp.MessageType.(*azdext.ProvisioningMessage_StateResponse); ok {
		if r.StateResponse.StateResult != nil {
			return &provisioning.StateResult{
				State: &provisioning.State{
					Outputs:   map[string]provisioning.OutputParameter{},
					Resources: []provisioning.Resource{},
				},
			}, nil // No fields to map
		}
		return &provisioning.StateResult{
			State: &provisioning.State{
				Outputs:   map[string]provisioning.OutputParameter{},
				Resources: []provisioning.Resource{},
			},
		}, nil
	}
	return nil, errors.New("unexpected response type")
}

// Provisioning the infrastructure within the specified template
func (p *ExternalProvider) Deploy(ctx context.Context) (*provisioning.DeployResult, error) {
	requestId := uuid.NewString()
	req := &azdext.ProvisioningMessage{
		RequestId: requestId,
		MessageType: &azdext.ProvisioningMessage_DeployRequest{
			DeployRequest: &azdext.DeployRequest{},
		},
	}
	resp, err := p.sendAndWait(ctx, req, func(r *azdext.ProvisioningMessage) bool {
		_, ok := r.MessageType.(*azdext.ProvisioningMessage_DeployResult)
		return ok
	})
	if err != nil {
		return nil, err
	}
	if r, ok := resp.MessageType.(*azdext.ProvisioningMessage_DeployResult); ok {
		if r.DeployResult.Result != nil {
			return &provisioning.DeployResult{
				Deployment: &provisioning.Deployment{
					Parameters: map[string]provisioning.InputParameter{},
					Outputs:    map[string]provisioning.OutputParameter{},
				},
			}, nil
		}
		return &provisioning.DeployResult{
			Deployment: &provisioning.Deployment{
				Parameters: map[string]provisioning.InputParameter{},
				Outputs:    map[string]provisioning.OutputParameter{},
			},
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
		_, ok := r.MessageType.(*azdext.ProvisioningMessage_PreviewResult)
		return ok
	})
	if err != nil {
		return nil, err
	}
	if r, ok := resp.MessageType.(*azdext.ProvisioningMessage_PreviewResult); ok {
		if r.PreviewResult.Result != nil {
			return &provisioning.DeployPreviewResult{}, nil // No fields to map
		}
		return &provisioning.DeployPreviewResult{}, nil
	}
	return nil, errors.New("unexpected response type")
}

func (p *ExternalProvider) Destroy(
	ctx context.Context,
	options provisioning.DestroyOptions,
) (*provisioning.DestroyResult, error) {
	requestId := uuid.NewString()
	req := &azdext.ProvisioningMessage{
		RequestId: requestId,
		MessageType: &azdext.ProvisioningMessage_DestroyRequest{
			DestroyRequest: &azdext.DestroyRequest{}, // No fields to map from options
		},
	}
	resp, err := p.sendAndWait(ctx, req, func(r *azdext.ProvisioningMessage) bool {
		_, ok := r.MessageType.(*azdext.ProvisioningMessage_DestroyResult)
		return ok
	})
	if err != nil {
		return nil, err
	}
	if r, ok := resp.MessageType.(*azdext.ProvisioningMessage_DestroyResult); ok {
		if r.DestroyResult.Result != nil {
			return &provisioning.DestroyResult{}, nil // No fields to map
		}
		return &provisioning.DestroyResult{}, nil
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
		_, ok := r.MessageType.(*azdext.ProvisioningMessage_ParametersResponse)
		return ok
	})
	if err != nil {
		return nil, err
	}
	if r, ok := resp.MessageType.(*azdext.ProvisioningMessage_ParametersResponse); ok {
		if r.ParametersResponse.Parameters != nil {
			return nil, nil // No fields to map
		}
		return nil, nil
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
