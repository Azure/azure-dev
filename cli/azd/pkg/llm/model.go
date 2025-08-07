package llm

import (
	"context"
	"fmt"

	"github.com/tmc/langchaingo/llms"
)

var _ llms.Model = (*Model)(nil)

// / Wraps an langchaingo model to allow specifying specific call options at create time
type Model struct {
	model   llms.Model
	options []llms.CallOption
}

func NewModel(model llms.Model, options ...llms.CallOption) *Model {
	return &Model{
		model:   model,
		options: options,
	}
}

func (m *Model) GenerateContent(ctx context.Context, messages []llms.MessageContent, options ...llms.CallOption) (*llms.ContentResponse, error) {
	allOptions := []llms.CallOption{}
	allOptions = append(allOptions, m.options...)
	allOptions = append(allOptions, options...)

	return m.model.GenerateContent(ctx, messages, allOptions...)
}

func (m *Model) Call(ctx context.Context, prompt string, options ...llms.CallOption) (string, error) {
	return "", fmt.Errorf("Deprecated, call GenerateContent")
}
