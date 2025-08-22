// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package llm

import (
	"context"
	"fmt"

	"github.com/tmc/langchaingo/llms"
)

var _ llms.Model = (*modelWithCallOptions)(nil)

// modelWithCallOptions wraps a langchaingo model to allow specifying default call options at creation time
type modelWithCallOptions struct {
	model   llms.Model
	options []llms.CallOption
}

// newModelWithCallOptions creates a new model wrapper with default call options
func newModelWithCallOptions(model llms.Model, options ...llms.CallOption) *modelWithCallOptions {
	return &modelWithCallOptions{
		model:   model,
		options: options,
	}
}

// GenerateContent generates content using the wrapped model, combining default options
// with any additional options provided at call time
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

// Call is deprecated and returns an error directing users to use GenerateContent instead
func (m *modelWithCallOptions) Call(ctx context.Context, prompt string, options ...llms.CallOption) (string, error) {
	return "", fmt.Errorf("Deprecated, call GenerateContent")
}
