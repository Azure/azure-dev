// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package mcp

import (
	"context"
	"fmt"
	"slices"

	"github.com/azure/azure-dev/cli/azd/internal/agent/consent"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// McpElicitationHandler handles elicitation requests from MCP clients by prompting the user for input
type McpElicitationHandler struct {
	consentManager consent.ConsentManager
	console        input.Console
}

// NewMcpElicitationHandler creates a new MCP elicitation handler with the specified consent manager and console
func NewMcpElicitationHandler(consentManager consent.ConsentManager, console input.Console) client.ElicitationHandler {
	return &McpElicitationHandler{
		consentManager: consentManager,
		console:        console,
	}
}

// Elicit implements client.ElicitationHandler.
func (h *McpElicitationHandler) Elicit(ctx context.Context, request mcp.ElicitationRequest) (*mcp.ElicitationResult, error) {
	// Get current executing tool for context (package-level tracking)
	currentTool := consent.GetCurrentExecutingTool()
	if currentTool == nil {
		return nil, fmt.Errorf("no current tool executing")
	}

	// Check consent for sampling if consent manager is available
	if err := h.checkConsent(ctx, currentTool); err != nil {
		return &mcp.ElicitationResult{
			ElicitationResponse: mcp.ElicitationResponse{
				Action: mcp.ElicitationResponseActionDecline,
			},
		}, nil
	}

	const root = "mem://schema.json"
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(root, request.Params.RequestedSchema); err != nil {
		return nil, err
	}

	schema, err := compiler.Compile(root)
	if err != nil {
		return nil, err
	}

	results := map[string]any{}

	h.console.Message(ctx, "")
	h.console.Message(ctx, request.Params.Message)

	for key, property := range schema.Properties {
		value, err := h.promptForValue(ctx, key, property, schema)
		if err != nil {
			return nil, err
		}
		results[key] = value
	}

	return &mcp.ElicitationResult{
		ElicitationResponse: mcp.ElicitationResponse{
			Action:  mcp.ElicitationResponseActionCancel,
			Content: results,
		},
	}, nil
}

func (h *McpElicitationHandler) promptForValue(
	ctx context.Context,
	key string,
	property *jsonschema.Schema,
	root *jsonschema.Schema,
) (any, error) {
	required := slices.Contains(root.Required, key)

	propertyPrompt := ux.NewPrompt(&ux.PromptOptions{
		Message:     fmt.Sprintf("Enter value for %s", key),
		Required:    required,
		HelpMessage: property.Description,
	})

	return propertyPrompt.Ask(ctx)
}

// checkConsent checks consent for sampling requests using the current executing tool
func (h *McpElicitationHandler) checkConsent(
	ctx context.Context,
	currentTool *consent.ExecutingTool,
) error {
	// Create a consent checker for this specific server
	consentChecker := consent.NewConsentChecker(h.consentManager, currentTool.Server)

	// Check elicitation consent using the consent checker
	decision, err := consentChecker.CheckElicitationConsent(ctx, currentTool.Name)
	if err != nil {
		return fmt.Errorf("consent check failed: %w", err)
	}

	if !decision.Allowed {
		if decision.RequiresPrompt {
			// Use console.DoInteraction to show consent prompt
			if err := h.console.DoInteraction(func() error {
				return consentChecker.PromptAndGrantElicitationConsent(
					ctx,
					currentTool.Name,
					"Allows requesting additional information from the user",
				)
			}); err != nil {
				return err
			}
		} else {
			return fmt.Errorf("sampling denied: %s", decision.Reason)
		}
	}

	return nil
}
