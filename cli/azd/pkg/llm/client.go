package llm

import (
	"github.com/tmc/langchaingo/llms"
)

// Client is the AZD representation of a Language Model (LLM) client.
type Client struct {
	llms.Model
}
