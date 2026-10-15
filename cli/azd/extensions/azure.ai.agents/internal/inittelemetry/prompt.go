// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package inittelemetry

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/grpc"
)

type promptClient struct {
	azdext.PromptServiceClient
	id Step
}

// Prompts decorates a prompt client without reading any request or response values.
func Prompts(client azdext.PromptServiceClient, id Step) azdext.PromptServiceClient {
	return &promptClient{PromptServiceClient: client, id: id}
}

func promptCall[T any](ctx context.Context, id Step, composite bool, call func() (T, error)) (T, error) {
	category, source, scope := Prompt, User, "prompt_rpc"
	if composite {
		// Host-owned pickers can fetch data or auto-resolve without showing a question.
		category, source, scope = External, "", "composite_prompt_rpc"
	} else if tracker, ok := ctx.Value(contextKey{}).(*Tracker); ok && tracker.noPrompt {
		category, source, scope = Execution, "", "prompt_resolution_rpc"
	}
	end := start(ctx, id, category, source, scope)
	value, err := call()
	end(err)
	return value, err
}

func (p *promptClient) Prompt(
	ctx context.Context, req *azdext.PromptRequest, opts ...grpc.CallOption,
) (*azdext.PromptResponse, error) {
	return promptCall(ctx, p.id, false, func() (*azdext.PromptResponse, error) {
		return p.PromptServiceClient.Prompt(ctx, req, opts...)
	})
}

func (p *promptClient) Select(
	ctx context.Context, req *azdext.SelectRequest, opts ...grpc.CallOption,
) (*azdext.SelectResponse, error) {
	return promptCall(ctx, p.id, false, func() (*azdext.SelectResponse, error) {
		return p.PromptServiceClient.Select(ctx, req, opts...)
	})
}

func (p *promptClient) MultiSelect(
	ctx context.Context, req *azdext.MultiSelectRequest, opts ...grpc.CallOption,
) (*azdext.MultiSelectResponse, error) {
	return promptCall(ctx, p.id, false, func() (*azdext.MultiSelectResponse, error) {
		return p.PromptServiceClient.MultiSelect(ctx, req, opts...)
	})
}

func (p *promptClient) Confirm(
	ctx context.Context, req *azdext.ConfirmRequest, opts ...grpc.CallOption,
) (*azdext.ConfirmResponse, error) {
	return promptCall(ctx, p.id, false, func() (*azdext.ConfirmResponse, error) {
		return p.PromptServiceClient.Confirm(ctx, req, opts...)
	})
}

func (p *promptClient) PromptSubscription(
	ctx context.Context, req *azdext.PromptSubscriptionRequest, opts ...grpc.CallOption,
) (*azdext.PromptSubscriptionResponse, error) {
	return promptCall(ctx, p.id, true, func() (*azdext.PromptSubscriptionResponse, error) {
		return p.PromptServiceClient.PromptSubscription(ctx, req, opts...)
	})
}

func (p *promptClient) PromptLocation(
	ctx context.Context, req *azdext.PromptLocationRequest, opts ...grpc.CallOption,
) (*azdext.PromptLocationResponse, error) {
	return promptCall(ctx, p.id, true, func() (*azdext.PromptLocationResponse, error) {
		return p.PromptServiceClient.PromptLocation(ctx, req, opts...)
	})
}

func (p *promptClient) PromptAiModel(
	ctx context.Context, req *azdext.PromptAiModelRequest, opts ...grpc.CallOption,
) (*azdext.PromptAiModelResponse, error) {
	return promptCall(ctx, p.id, true, func() (*azdext.PromptAiModelResponse, error) {
		return p.PromptServiceClient.PromptAiModel(ctx, req, opts...)
	})
}

func (p *promptClient) PromptAiDeployment(
	ctx context.Context, req *azdext.PromptAiDeploymentRequest, opts ...grpc.CallOption,
) (*azdext.PromptAiDeploymentResponse, error) {
	return promptCall(ctx, p.id, true, func() (*azdext.PromptAiDeploymentResponse, error) {
		return p.PromptServiceClient.PromptAiDeployment(ctx, req, opts...)
	})
}

func (p *promptClient) PromptAiModelLocationWithQuota(
	ctx context.Context, req *azdext.PromptAiModelLocationWithQuotaRequest, opts ...grpc.CallOption,
) (*azdext.PromptAiModelLocationWithQuotaResponse, error) {
	return promptCall(ctx, p.id, true, func() (*azdext.PromptAiModelLocationWithQuotaResponse, error) {
		return p.PromptServiceClient.PromptAiModelLocationWithQuota(ctx, req, opts...)
	})
}
