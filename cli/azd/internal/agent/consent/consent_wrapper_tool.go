// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package consent

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/internal/agent/tools/common"
	"github.com/azure/azure-dev/pkg/input"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tmc/langchaingo/tools"
)

// Ensure ConsentWrapperTool implements common.Tool
var _ tools.Tool = (*ConsentWrapperTool)(nil)

// ConsentWrapperTool wraps a langchaingo tool with consent protection
type ConsentWrapperTool struct {
	console        input.Console
	tool           common.AnnotatedTool
	consentChecker *ConsentChecker
	annotations    mcp.ToolAnnotation
}

// Name returns the name of the tool
func (c *ConsentWrapperTool) Name() string {
	return c.tool.Name()
}

// Server returns the server of the tool
func (c *ConsentWrapperTool) Server() string {
	return c.tool.Server()
}

// Annotations returns the annotations of the tool
func (c *ConsentWrapperTool) Annotations() mcp.ToolAnnotation {
	return c.annotations
}

// Description returns the description of the tool
func (c *ConsentWrapperTool) Description() string {
	return c.tool.Description()
}

// Call executes the tool with consent protection
func (c *ConsentWrapperTool) Call(ctx context.Context, input string) (string, error) {
	// Set current executing tool for tracking (used by sampling handler)
	SetCurrentExecutingTool(c.Name(), c.Server())
	defer ClearCurrentExecutingTool()

	// Check consent using enhanced checker with annotations
	decision, err := c.consentChecker.CheckToolConsent(ctx, c.Name(), c.Description(), c.annotations)
	if err != nil {
		return "", fmt.Errorf("consent check failed: %w", err)
	}

	if !decision.Allowed {
		if decision.RequiresPrompt {
			if err := c.console.DoInteraction(func() error {
				// Show interactive consent prompt using shared checker with annotations
				promptErr := c.consentChecker.PromptAndGrantConsent(ctx, c.Name(), c.Description(), c.annotations)
				c.console.Message(ctx, "")

				return promptErr
			}); err != nil {
				return "", err
			}
		} else {
			return "", fmt.Errorf("tool execution denied: %s", decision.Reason)
		}
	}

	// Consent granted, execute the original tool
	return c.tool.Call(ctx, input)
}

// newConsentWrapperTool wraps a langchaingo tool with consent protection
func newConsentWrapperTool(
	tool common.AnnotatedTool,
	console input.Console,
	consentManager ConsentManager,
) common.AnnotatedTool {
	return &ConsentWrapperTool{
		tool:           tool,
		console:        console,
		consentChecker: NewConsentChecker(consentManager, tool.Server()),
		annotations:    tool.Annotations(),
	}
}
