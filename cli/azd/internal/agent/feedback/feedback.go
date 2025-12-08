// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package feedback

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/internal/agent"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	uxlib "github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/fatih/color"
)

// FeedbackCollectorOptions configures the feedback collection behavior
type FeedbackCollectorOptions struct {
	// EnableLoop determines if feedback collection should loop for multiple rounds
	EnableLoop bool

	// FeedbackPrompt is the prompt for collecting feedback
	FeedbackPrompt string

	// FeedbackHint is the hint text for FeedbackPrompt
	FeedbackHint string

	// RequireFeedback determines if feedback input is required when provided
	RequireFeedback bool

	// AIDisclaimer is the disclaimer text to show
	AIDisclaimer string
}

// FeedbackCollector handles feedback collection and processing
type FeedbackCollector struct {
	console input.Console
	options FeedbackCollectorOptions
}

// NewFeedbackCollector creates a new feedback collector with the specified options
func NewFeedbackCollector(console input.Console, options FeedbackCollectorOptions) *FeedbackCollector {
	return &FeedbackCollector{
		console: console,
		options: options,
	}
}

// CollectFeedbackAndApply collects user feedback and applies it using the provided agent
func (c *FeedbackCollector) CollectFeedbackAndApply(
	ctx context.Context,
	azdAgent agent.Agent,
	AIDisclaimer string,
) error {
	if c.options.EnableLoop {
		return c.collectFeedbackAndApplyWithLoop(ctx, azdAgent, AIDisclaimer)
	}
	return c.collectFeedbackAndApplyOnce(ctx, azdAgent, AIDisclaimer)
}

// collectFeedbackAndApplyWithLoop handles feedback collection with multiple rounds (init.go style)
func (c *FeedbackCollector) collectFeedbackAndApplyWithLoop(
	ctx context.Context,
	azdAgent agent.Agent,
	AIDisclaimer string,
) error {
	// Loop to allow multiple rounds of feedback
	for {
		userInputPrompt := uxlib.NewPrompt(&uxlib.PromptOptions{
			Message:               c.options.FeedbackPrompt,
			HelpMessageOnNextLine: c.options.FeedbackHint,
			Required:              c.options.RequireFeedback,
		})

		userInput, err := userInputPrompt.Ask(ctx)
		if err != nil {
			return fmt.Errorf("failed to collect feedback for user input: %w", err)
		}

		if userInput == "" {
			c.console.Message(ctx, "")
			break
		}

		c.applyFeedback(ctx, azdAgent, userInput, AIDisclaimer)
	}

	return nil
}

// collectFeedbackAndApplyOnce handles single feedback collection like error handling workflow
func (c *FeedbackCollector) collectFeedbackAndApplyOnce(
	ctx context.Context,
	azdAgent agent.Agent,
	AIDisclaimer string,
) error {
	userInputPrompt := uxlib.NewPrompt(&uxlib.PromptOptions{
		Message:               c.options.FeedbackPrompt,
		HelpMessageOnNextLine: c.options.FeedbackHint,
		Required:              c.options.RequireFeedback,
	})

	userInput, err := userInputPrompt.Ask(ctx)
	if err != nil {
		return fmt.Errorf("failed to collect feedback for user input: %w", err)
	}

	if userInput == "" {
		c.console.Message(ctx, "")
		return nil
	}

	return c.applyFeedback(ctx, azdAgent, userInput, AIDisclaimer)
}

// applyFeedback sends feedback to agent and displays response
func (c *FeedbackCollector) applyFeedback(
	ctx context.Context,
	azdAgent agent.Agent,
	userInput string,
	AIDisclaimer string,
) error {
	c.console.Message(ctx, "")
	c.console.Message(ctx, color.MagentaString("Feedback"))

	feedbackOutput, err := azdAgent.SendMessage(ctx, userInput)
	if err != nil {
		if feedbackOutput != "" {
			c.console.Message(ctx, AIDisclaimer)
			c.console.Message(ctx, output.WithMarkdown(feedbackOutput))
		}
		return err
	}

	c.console.Message(ctx, AIDisclaimer)

	c.console.Message(ctx, "")
	c.console.Message(ctx, fmt.Sprintf("%s:", output.AzdAgentLabel()))
	c.console.Message(ctx, output.WithMarkdown(feedbackOutput))
	c.console.Message(ctx, "")

	return nil
}
