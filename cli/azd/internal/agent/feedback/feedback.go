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

	// InitialPrompt is the first question to ask the user
	InitialPrompt string

	// FeedbackPrompt is the prompt for collecting actual feedback
	FeedbackPrompt string

	// FeedbackPlaceholder is the placeholder text for feedback input
	FeedbackPlaceholder string

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
) error {
	if c.options.EnableLoop {
		return c.collectFeedbackAndApplyWithLoop(ctx, azdAgent)
	}
	return c.collectFeedbackAndApplyOnce(ctx, azdAgent)
}

// collectFeedbackAndApplyWithLoop handles feedback collection with multiple rounds (init.go style)
func (c *FeedbackCollector) collectFeedbackAndApplyWithLoop(
	ctx context.Context,
	azdAgent agent.Agent,
) error {
	// Loop to allow multiple rounds of feedback
	for {
		confirmFeedback := uxlib.NewConfirm(&uxlib.ConfirmOptions{
			Message:      c.options.InitialPrompt,
			DefaultValue: uxlib.Ptr(false),
			HelpMessage:  "You will be able to provide and feedback or changes after each step.",
		})

		hasFeedback, err := confirmFeedback.Ask(ctx)
		if err != nil {
			return err
		}

		if !*hasFeedback {
			c.console.Message(ctx, "")
			break
		}

		if err := c.processFeedbackRound(ctx, azdAgent); err != nil {
			return err
		}
	}

	return nil
}

// collectFeedbackAndApplyOnce handles single feedback collection (error.go style)
func (c *FeedbackCollector) collectFeedbackAndApplyOnce(
	ctx context.Context,
	azdAgent agent.Agent,
) error {
	userInputPrompt := uxlib.NewPrompt(&uxlib.PromptOptions{
		Message:  c.options.FeedbackPrompt,
		Hint:     "Describe your changes or press enter to skip.",
		Required: false,
	})

	userInput, err := userInputPrompt.Ask(ctx)
	if err != nil {
		return fmt.Errorf("failed to collect feedback for user input: %w", err)
	}

	if userInput == "" {
		c.console.Message(ctx, "")
		return nil
	}

	return c.applyFeedback(ctx, azdAgent, userInput)
}

// processFeedbackRound handles a single round of feedback in loop mode
func (c *FeedbackCollector) processFeedbackRound(
	ctx context.Context,
	azdAgent agent.Agent,
) error {
	userInputPrompt := uxlib.NewPrompt(&uxlib.PromptOptions{
		Message:        c.options.FeedbackPrompt,
		PlaceHolder:    c.options.FeedbackPlaceholder,
		Required:       c.options.RequireFeedback,
		IgnoreHintKeys: true,
	})

	userInput, err := userInputPrompt.Ask(ctx)
	if err != nil {
		return fmt.Errorf("error collecting feedback during azd init, %w", err)
	}

	c.console.Message(ctx, "")

	if userInput != "" {
		return c.applyFeedback(ctx, azdAgent, userInput)
	}

	return nil
}

// applyFeedback sends feedback to agent and displays response
func (c *FeedbackCollector) applyFeedback(
	ctx context.Context,
	azdAgent agent.Agent,
	userInput string,
) error {
	c.console.Message(ctx, color.MagentaString("Feedback"))

	AIDisclaimer := output.WithGrayFormat("The following content is AI-generated. AI responses may be incorrect.")
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
