// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/tmc/langchaingo/llms/openai"

	"azd.ai.start/internal/agent"
)

// RunEnhancedAzureAgent runs the enhanced Azure AI agent with full capabilities
func RunEnhancedAzureAgent(ctx context.Context, llm *openai.LLM, args []string) error {
	// Create the enhanced agent
	azureAgent := agent.NewAzureAIAgent(llm)

	fmt.Println("🤖 Enhanced Azure AI Agent - Interactive Mode")
	fmt.Println("═══════════════════════════════════════════════════════════")

	// Handle initial query if provided
	var initialQuery string
	if len(args) > 0 {
		initialQuery = strings.Join(args, " ")
	}

	scanner := bufio.NewScanner(os.Stdin)

	for {
		var userInput string

		if initialQuery != "" {
			userInput = initialQuery
			initialQuery = "" // Clear after first use
			fmt.Printf("💬 You: %s\n", userInput)
		} else {
			fmt.Print("\n💬 You: ")
			if !scanner.Scan() {
				break // EOF or error
			}
			userInput = strings.TrimSpace(scanner.Text())
		}

		// Check for exit commands
		if userInput == "" {
			continue
		}

		if strings.ToLower(userInput) == "exit" || strings.ToLower(userInput) == "quit" {
			fmt.Println("👋 Goodbye! Thanks for using the Enhanced Azure AI Agent!")
			break
		}

		// Process the query with the enhanced agent
		response, err := azureAgent.ProcessQuery(ctx, userInput)
		if err != nil {
			fmt.Printf("❌ Error: %v\n", err)
			continue
		}

		// Display the final response
		fmt.Printf("\n💬 Agent:\n%s\n", response)
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading input: %w", err)
	}

	return nil
}
