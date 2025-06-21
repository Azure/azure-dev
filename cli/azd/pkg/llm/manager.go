package llm

import (
	"context"
	"fmt"

	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/openai"
)

func NewManager() Manager {
	return Manager{}
}

type Manager struct {
}

func (m Manager) Info() (string, error) {
	llm, err := openai.New(
		openai.WithModel("o1-mini"),
		openai.WithAPIType(openai.APITypeAzure),
		openai.WithAPIVersion("2024-12-01-preview"),
		openai.WithBaseURL("https://vivazqu-2260-resource.cognitiveservices.azure.com/"),
	)
	if err != nil {
		return "", fmt.Errorf("failed to create LLM: %w", err)
	}

	ctx := context.Background()
	content := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, `
Tell what model you are using and what is your version. Make it sound like a friendly human.`),
	}
	fmt.Println("Generating content...")
	output, err := llm.GenerateContent(ctx, content,
		llms.WithMaxTokens(4000),
		llms.WithTemperature(1),
	)
	if err != nil {
		return "", fmt.Errorf("failed to generate content: %w", err)
	}
	return output.Choices[0].Content, nil
}
