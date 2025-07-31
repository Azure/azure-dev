// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/fatih/color"
	"github.com/tmc/langchaingo/llms/openai"

	"azd.ai.start/internal/agent"
)

// RunEnhancedAzureAgent runs the enhanced Azure AI agent with full capabilities
func RunEnhancedAzureAgent(ctx context.Context, llm *openai.LLM, args []string) error {
	// Create the enhanced agent
	azureAgent, err := agent.NewAzureAIAgent(llm)
	if err != nil {
		return err
	}

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
			color.Cyan("💬 You: %s\n", userInput)
		} else {
			fmt.Print(color.CyanString("\n💬 You: "))
			color.Set(color.FgCyan) // Set blue color for user input
			if !scanner.Scan() {
				color.Unset() // Reset color
				break         // EOF or error
			}
			userInput = strings.TrimSpace(scanner.Text())
			color.Unset() // Reset color after input
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
		err := azureAgent.ProcessQuery(ctx, userInput)
		if err != nil {
			continue
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading input: %w", err)
	}

	return nil
}
