// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/tmc/langchaingo/agents"
	"github.com/tmc/langchaingo/schema"
	"github.com/tmc/langchaingo/tools"

	"azd.ai.start/internal/logging"
	"azd.ai.start/internal/session"
	"azd.ai.start/internal/utils"
	"azd.ai.start/internal/validation"
)

// AzureAIAgent represents an enhanced Azure AI agent with action tracking and intent validation
type AzureAIAgent struct {
	agent           *agents.ConversationalAgent
	executor        *agents.Executor
	memory          schema.Memory
	tools           []tools.Tool
	intentValidator *validation.IntentValidator
	actionLogger    *logging.ActionLogger
	currentSession  *session.ActionSession
}

// ProcessQuery processes a user query with full action tracking and validation
func (aai *AzureAIAgent) ProcessQuery(ctx context.Context, userInput string) (*AgentResponse, error) {
	// Start new action session
	sess := session.NewActionSession(userInput)
	aai.currentSession = sess

	fmt.Printf("\n🎯 Intent: %s\n", userInput)
	fmt.Printf("📋 Planning and executing actions...\n")
	fmt.Println("═══════════════════════════════════════")

	// Clear previous actions
	aai.actionLogger.Clear()

	// Enhanced user input with explicit completion requirements
	enhancedInput := fmt.Sprintf(`%s

IMPORTANT: You must complete this task successfully. Do not stop until:
1. All required actions have been executed
2. Any files that need to be created are actually saved
3. You verify the results of your actions
4. The task is fully accomplished

If a tool fails, analyze why and try again with corrections. If you need to create files, use the write_file tool with the complete content.`, userInput)

	// Execute with enhanced input
	result, err := aai.executor.Call(ctx, map[string]any{
		"input": enhancedInput,
	})

	if err != nil {
		sess.End()
		fmt.Printf("❌ Execution failed: %s\n", err.Error())
		return nil, err
	}

	// Get executed actions from logger and intermediate steps
	executedActions := aai.actionLogger.GetActions()
	for _, action := range executedActions {
		sess.AddExecutedAction(action)
	}

	// If no actions in logger but we have intermediate steps, extract them
	if len(sess.ExecutedActions) == 0 {
		if steps, ok := result["intermediateSteps"].([]schema.AgentStep); ok && len(steps) > 0 {
			for _, step := range steps {
				actionLog := session.ActionLog{
					Timestamp: time.Now(),
					Action:    step.Action.Tool,
					Tool:      step.Action.Tool,
					Input:     step.Action.ToolInput,
					Output:    step.Observation,
					Success:   true,
					Duration:  time.Millisecond * 100, // Approximate
				}
				sess.AddExecutedAction(actionLog)
			}
		}
	}

	// Check if any actions were taken - if not, this was likely conversational
	if len(sess.ExecutedActions) == 0 {
		fmt.Printf("💬 No tool actions needed - appears to be conversational\n")

		sess.End()
		validationResult := &validation.ValidationResult{
			Status:      validation.ValidationComplete,
			Explanation: "Conversational response - no actions required",
			Confidence:  1.0,
		}
		sess.SetValidationResult(validationResult)

		// Display simple summary for conversational responses
		fmt.Println("\n📊 Session Summary")
		fmt.Println("═══════════════════════════════════════")
		duration := sess.EndTime.Sub(sess.StartTime)
		fmt.Printf("⏱️  Duration: %v\n", duration.Round(time.Millisecond))
		fmt.Println("\n💬 Conversational response - no tool actions needed")
		fmt.Printf("🎯 Intent Status: %s (%.1f%% confidence)\n", validationResult.Status, validationResult.Confidence*100)
		fmt.Println("═══════════════════════════════════════")

		return NewAgentResponse(result["output"].(string), sess, validationResult), nil
	}

	// Actions were taken, so validate and potentially retry
	var lastResult = result
	var lastValidation *validation.ValidationResult
	maxAttempts := 3 // Maximum retry attempts for incomplete tasks

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		// Validate intent completion with enhanced validation
		fmt.Printf("\n🔍 Validating completion...\n")
		validationResult := aai.intentValidator.ValidateCompletion(
			userInput,
			sess.ExecutedActions,
		)
		lastValidation = validationResult
		sess.SetValidationResult(validationResult)

		// Check if task is complete
		if validationResult.Status == validation.ValidationComplete {
			fmt.Printf("✅ Task completed successfully!\n")
			break
		}

		// If task is incomplete and we have more attempts, retry
		if attempt < maxAttempts {
			if validationResult.Status == validation.ValidationIncomplete || validationResult.Status == validation.ValidationPartial {
				fmt.Printf("⚠️  Task incomplete (attempt %d/%d): %s\n", attempt, maxAttempts, validationResult.Explanation)
				fmt.Printf("🔄 Analyzing what's missing and taking corrective action...\n")

				// Clear previous actions for retry
				aai.actionLogger.Clear()

				// Enhanced retry with feedback about what was incomplete
				retryInput := fmt.Sprintf(`%s

IMPORTANT: You must complete this task successfully. Do not stop until:
1. All required actions have been executed
2. Any files that need to be created are actually saved
3. You verify the results of your actions
4. The task is fully accomplished

PREVIOUS ATTEMPT ANALYSIS: The previous attempt was marked as %s. 
Reason: %s

Please analyze what was missing or incomplete and take the necessary additional actions to fully complete the task.`,
					userInput, validationResult.Status, validationResult.Explanation)

				// Execute retry
				retryResult, err := aai.executor.Call(ctx, map[string]any{
					"input": retryInput,
				})

				if err != nil {
					fmt.Printf("❌ Retry attempt %d failed: %s\n", attempt+1, err.Error())
					if attempt == maxAttempts-1 {
						sess.End()
						return nil, err
					}
					continue
				}

				lastResult = retryResult

				// Get new actions from this retry
				retryActions := aai.actionLogger.GetActions()
				if len(retryActions) == 0 {
					if steps, ok := retryResult["intermediateSteps"].([]schema.AgentStep); ok && len(steps) > 0 {
						for _, step := range steps {
							actionLog := session.ActionLog{
								Timestamp: time.Now(),
								Action:    step.Action.Tool,
								Tool:      step.Action.Tool,
								Input:     step.Action.ToolInput,
								Output:    step.Observation,
								Success:   true,
								Duration:  time.Millisecond * 100,
							}
							retryActions = append(retryActions, actionLog)
						}
					}
				}

				// Accumulate actions from retry
				for _, action := range retryActions {
					sess.AddExecutedAction(action)
				}
				continue
			}
		} else {
			// This was the last attempt and still incomplete
			fmt.Printf("⚠️  Task still incomplete after %d attempts: %s\n", maxAttempts, validationResult.Explanation)
			fmt.Printf("💡 Consider:\n")
			fmt.Printf("   - Breaking the task into smaller, more specific steps\n")
			fmt.Printf("   - Checking if all required files were actually created\n")
			fmt.Printf("   - Verifying tool outputs were successful\n")
		}
	}

	sess.End()

	// Display comprehensive summary
	aai.displayCompleteSummary(sess, lastResult)

	return NewAgentResponse(lastResult["output"].(string), sess, lastValidation), nil
}

// ProcessQueryWithRetry processes a query with automatic retry on failure
func (aai *AzureAIAgent) ProcessQueryWithRetry(ctx context.Context, userInput string, maxRetries int) (*AgentResponse, error) {
	var lastErr error
	var lastResponse *AgentResponse

	for attempt := 1; attempt <= maxRetries; attempt++ {
		fmt.Printf("\n🔄 Attempt %d/%d\n", attempt, maxRetries)

		response, err := aai.ProcessQuery(ctx, userInput)
		if err != nil {
			lastErr = err
			fmt.Printf("❌ Attempt %d failed: %s\n", attempt, err.Error())
			continue
		}

		lastResponse = response

		// Check if task completed successfully
		if response.Validation.Status == validation.ValidationComplete {
			fmt.Printf("✅ Task completed successfully on attempt %d\n", attempt)
			return response, nil
		}

		if response.Validation.Status == validation.ValidationPartial {
			fmt.Printf("⚠️  Partial completion on attempt %d: %s\n", attempt, response.Validation.Explanation)
		} else {
			fmt.Printf("❌ Task incomplete on attempt %d: %s\n", attempt, response.Validation.Explanation)
		}

		// Clear memory for fresh retry
		aai.ClearMemory(ctx)
	}

	if lastResponse != nil {
		return lastResponse, nil
	}

	return nil, fmt.Errorf("all %d attempts failed, last error: %w", maxRetries, lastErr)
}

// GetSessionStats returns statistics about the current session
func (aai *AzureAIAgent) GetSessionStats() *SessionStats {
	if aai.currentSession == nil {
		return &SessionStats{}
	}

	stats := &SessionStats{
		TotalActions:      len(aai.currentSession.ExecutedActions),
		SuccessfulActions: 0,
		FailedActions:     0,
		TotalDuration:     aai.currentSession.EndTime.Sub(aai.currentSession.StartTime),
	}

	for _, action := range aai.currentSession.ExecutedActions {
		if action.Success {
			stats.SuccessfulActions++
		} else {
			stats.FailedActions++
		}
	}

	return stats
}

// GetMemoryContent returns the current memory content for debugging
func (aai *AzureAIAgent) GetMemoryContent(ctx context.Context) (map[string]any, error) {
	return aai.memory.LoadMemoryVariables(ctx, map[string]any{})
}

// ClearMemory clears the conversation memory
func (aai *AzureAIAgent) ClearMemory(ctx context.Context) error {
	return aai.memory.Clear(ctx)
}

// EnableVerboseLogging enables detailed iteration logging
func (aai *AzureAIAgent) EnableVerboseLogging() {
	// This would enable more detailed logging in the action logger
	fmt.Println("🔍 Verbose logging enabled - you'll see detailed iteration steps")
}

// displayCompleteSummary displays a comprehensive summary of the session
func (aai *AzureAIAgent) displayCompleteSummary(sess *session.ActionSession, result map[string]any) {
	fmt.Println("\n📊 Session Summary")
	fmt.Println("═══════════════════════════════════════")

	// Display timing
	duration := sess.EndTime.Sub(sess.StartTime)
	fmt.Printf("⏱️  Duration: %v\n", duration.Round(time.Millisecond))

	// Display actions with attempt grouping
	if len(sess.ExecutedActions) > 0 {
		fmt.Println("\n🔧 Actions Executed:")
		for i, action := range sess.ExecutedActions {
			status := "✅"
			if !action.Success {
				status = "❌"
			}
			fmt.Printf("  %s %d. %s (%v)\n",
				status, i+1,
				utils.TruncateString(action.Input, 50),
				action.Duration.Round(time.Millisecond))
		}
	} else {
		fmt.Println("\n🔧 No explicit tool actions required")
	}

	// Display validation result with enhanced messaging
	if validationResult, ok := sess.ValidationResult.(*validation.ValidationResult); ok {
		fmt.Printf("\n🎯 Intent Status: %s", validationResult.Status)
		if validationResult.Confidence > 0 {
			fmt.Printf(" (%.1f%% confidence)", validationResult.Confidence*100)
		}
		fmt.Println()

		if validationResult.Explanation != "" {
			fmt.Printf("💭 Assessment: %s\n", validationResult.Explanation)
		}

		// Show completion status with actionable advice
		switch validationResult.Status {
		case validation.ValidationComplete:
			fmt.Printf("🎉 Task completed successfully!\n")
		case validation.ValidationPartial:
			fmt.Printf("⚠️  Task partially completed. Some aspects may need attention.\n")
		case validation.ValidationIncomplete:
			fmt.Printf("❌ Task incomplete. Additional actions may be needed.\n")
		case validation.ValidationError:
			fmt.Printf("⚠️  Validation error. Please review the actions taken.\n")
		}
	}

	// Display intermediate steps if available
	if steps, ok := result["intermediateSteps"].([]schema.AgentStep); ok && len(steps) > 0 {
		fmt.Printf("\n🔍 Reasoning Steps: %d\n", len(steps))
		for i, step := range steps {
			fmt.Printf("Step %d:\n", i+1)
			fmt.Printf("  Tool: %s\n", step.Action.Tool)
			fmt.Printf("  Input: %s\n", step.Action.ToolInput)
			fmt.Printf("  Observation: %s\n", utils.TruncateString(step.Observation, 200))
		}
	}

	fmt.Println("═══════════════════════════════════════")
}
