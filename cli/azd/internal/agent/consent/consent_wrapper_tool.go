// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package consent

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/common"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tmc/langchaingo/tools"
)

// Ensure ConsentWrapperTool implements common.Tool
var _ tools.Tool = (*ConsentWrapperTool)(nil)

// ConsentWrapperTool wraps a langchaingo tool with consent protection
type ConsentWrapperTool struct {
	console        input.Console
	tool           tools.Tool
	consentChecker *ConsentChecker
	annotations    *mcp.ToolAnnotation
}

// Name returns the name of the tool
func (c *ConsentWrapperTool) Name() string {
	return c.tool.Name()
}

// Description returns the description of the tool
func (c *ConsentWrapperTool) Description() string {
	return c.tool.Description()
}

// Call executes the tool with consent protection
func (c *ConsentWrapperTool) Call(ctx context.Context, input string) (string, error) {
	// Check consent using enhanced checker with annotations
	decision, err := c.consentChecker.CheckToolConsentWithAnnotations(ctx, c.Name(), c.Description(), c.annotations)
	if err != nil {
		return "", fmt.Errorf("consent check failed: %w", err)
	}

	if !decision.Allowed {
		if decision.RequiresPrompt {
			if err := c.console.DoInteraction(func() error {
				// Show interactive consent prompt using shared checker
				promptErr := c.consentChecker.PromptAndGrantConsent(ctx, c.Name(), c.Description())
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
	tool tools.Tool,
	console input.Console,
	consentManager ConsentManager,
) tools.Tool {
	var server string
	var annotations *mcp.ToolAnnotation

	if annotatedTool, ok := tool.(common.AnnotatedTool); ok {
		toolAnnotations := annotatedTool.Annotations()
		annotations = &toolAnnotations
		server = annotatedTool.Server()
	}

	if commonTool, ok := tool.(common.Tool); ok {
		server = commonTool.Server()
	}

	if server == "" {
		server = "unknown"
	}

	return &ConsentWrapperTool{
		tool:           tool,
		console:        console,
		consentChecker: NewConsentChecker(consentManager, server),
		annotations:    annotations,
	}
}
