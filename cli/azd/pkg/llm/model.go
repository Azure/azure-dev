// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package llm

import (
	"context"
	"fmt"

	"github.com/tmc/langchaingo/llms"
)

var _ llms.Model = (*modelWithCallOptions)(nil)

// / Wraps an langchaingo model to allow specifying specific call options at create time
type modelWithCallOptions struct {
	model   llms.Model
	options []llms.CallOption
}

func newModelWithCallOptions(model llms.Model, options ...llms.CallOption) *modelWithCallOptions {
	return &modelWithCallOptions{
		model:   model,
		options: options,
	}
}

func (m *modelWithCallOptions) GenerateContent(
	ctx context.Context,
	messages []llms.MessageContent,
	options ...llms.CallOption,
) (*llms.ContentResponse, error) {
	allOptions := []llms.CallOption{}
	allOptions = append(allOptions, m.options...)
	allOptions = append(allOptions, options...)

	return m.model.GenerateContent(ctx, messages, allOptions...)
}

func (m *modelWithCallOptions) Call(ctx context.Context, prompt string, options ...llms.CallOption) (string, error) {
	return "", fmt.Errorf("Deprecated, call GenerateContent")
}
