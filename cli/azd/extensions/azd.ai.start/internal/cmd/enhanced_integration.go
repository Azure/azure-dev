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
	azureAgent := agent.CreateAzureAIAgent(llm)

	fmt.Println("🤖 Enhanced Azure AI Agent - Interactive Mode")
	fmt.Println("Features: Action Tracking | Intent Validation | Smart Memory")
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

		// Special commands
		if strings.ToLower(userInput) == "clear" {
			err := azureAgent.ClearMemory(ctx)
			if err != nil {
				fmt.Printf("❌ Failed to clear memory: %s\n", err.Error())
			} else {
				fmt.Println("🧹 Memory cleared!")
			}
			continue
		}

		if strings.ToLower(userInput) == "stats" {
			stats := azureAgent.GetSessionStats()
			fmt.Printf("📊 Session Stats:\n")
			fmt.Printf("   Total Actions: %d\n", stats.TotalActions)
			fmt.Printf("   Successful: %d\n", stats.SuccessfulActions)
			fmt.Printf("   Failed: %d\n", stats.FailedActions)
			if stats.TotalDuration > 0 {
				fmt.Printf("   Duration: %v\n", stats.TotalDuration)
			}
			continue
		}

		// Process the query with the enhanced agent
		fmt.Printf("\n🤖 Enhanced AI Agent:\n")
		response, err := azureAgent.ProcessQuery(ctx, userInput)
		if err != nil {
			fmt.Printf("❌ Error: %v\n", err)
			continue
		}

		// Display the final response
		fmt.Printf("\n💬 Final Response:\n%s\n", response.Output)
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading input: %w", err)
	}

	return nil
}
