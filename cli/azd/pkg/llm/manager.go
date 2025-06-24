package llm

import (
	"context"
	"fmt"
	"io"

	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/openai"
)

func NewManager() Manager {
	return Manager{}
}

type Manager struct {
}

func (m Manager) Info(stdout io.Writer) (string, error) {
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
Respond with the version of the LLM you are using.
Use the format "LLM: <version>".`),
	}
	output, err := llm.GenerateContent(ctx, content,
		llms.WithMaxTokens(4000),
		llms.WithTemperature(1),
		llms.WithStreamingFunc(func(ctx context.Context, chunk []byte) error {
			fmt.Fprintf(stdout, "%s", string(chunk))
			return nil
		}),
	)
	if err != nil {
		return "", fmt.Errorf("failed to generate content: %w", err)
	}
	return output.Choices[0].Content, nil
}

// func (m Manager) Info(stdout io.Writer) (string, error) {
// 	llm, err := ollama.New(ollama.WithModel("llama3"))
// 	if err != nil {
// 		return "", err
// 	}
// 	ctx := context.Background()
// 	output, err := llms.GenerateFromSinglePrompt(
// 		ctx,
// 		llm,
// 		"Human: Describe the version of the LLM you are using. Use the format 'LLM: <version>'.",
// 		llms.WithTemperature(0.8),
// 		llms.WithStreamingFunc(func(ctx context.Context, chunk []byte) error {
// 			fmt.Fprintf(stdout, "%s", string(chunk))
// 			return nil
// 		}),
// 	)
// 	_ = output // We don't use the output here, as we are streaming directly to stdout.
// 	if err != nil {
// 		return "", err
// 	}
// 	return "", nil
// }
