// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package consent

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/mark3labs/mcp-go/mcp"
)

// ConsentChecker provides shared consent checking logic for different tool types
type ConsentChecker struct {
	consentMgr ConsentManager
	serverName string
}

// NewConsentChecker creates a new shared consent checker
func NewConsentChecker(
	consentMgr ConsentManager,
	serverName string,
) *ConsentChecker {
	return &ConsentChecker{
		consentMgr: consentMgr,
		serverName: serverName,
	}
}

// CheckToolConsentWithAnnotations checks tool consent with optional MCP annotations
func (cc *ConsentChecker) CheckToolConsent(
	ctx context.Context,
	toolName, toolDesc string,
	annotations mcp.ToolAnnotation,
) (*ConsentDecision, error) {
	toolID := fmt.Sprintf("%s/%s", cc.serverName, toolName)

	// Create consent request
	consentRequest := ConsentRequest{
		ToolID:      toolID,
		ServerName:  cc.serverName,
		SessionID:   "", // Not needed since each manager represents one session
		Annotations: annotations,
	}

	return cc.consentMgr.CheckConsent(ctx, consentRequest)
}

// PromptAndGrantConsent shows consent prompt and grants permission based on user choice
func (cc *ConsentChecker) PromptAndGrantConsent(
	ctx context.Context,
	toolName, toolDesc string,
	annotations mcp.ToolAnnotation,
) error {
	toolID := fmt.Sprintf("%s/%s", cc.serverName, toolName)

	choice, err := cc.promptForConsent(ctx, toolName, toolDesc, annotations)
	if err != nil {
		return fmt.Errorf("consent prompt failed: %w", err)
	}

	if choice == "deny" {
		return fmt.Errorf("tool execution denied by user")
	}

	// Grant consent based on user choice
	return cc.grantConsentFromChoice(ctx, toolID, choice)
}

// promptForConsent shows an interactive consent prompt and returns the user's choice
func (cc *ConsentChecker) promptForConsent(
	ctx context.Context,
	toolName, toolDesc string,
	annotations mcp.ToolAnnotation,
) (string, error) {
	message := fmt.Sprintf(
		"Tool %s from server %s requires consent.\n\nHow would you like to proceed?",
		output.WithHighLightFormat(toolName),
		output.WithHighLightFormat(cc.serverName),
	)

	helpMessage := toolDesc

	choices := []*ux.SelectChoice{
		{
			Value: "deny",
			Label: "Deny - Block this tool execution",
		},
		{
			Value: "once",
			Label: "Allow once - Execute this time only",
		},
		{
			Value: "session",
			Label: "Allow for session - Allow until restart",
		},
		{
			Value: "project",
			Label: "Allow for project - Remember for this project",
		},
		{
			Value: "always",
			Label: "Allow always - Remember globally",
		},
	}

	// Add server trust option if not already trusted
	if !cc.isServerAlreadyTrusted(ctx) {
		choices = append(choices, &ux.SelectChoice{
			Value: "server",
			Label: "Allow all tools from this server",
		})
	}

	// Add readonly trust options if this is a readonly tool
	isReadOnlyTool := annotations.ReadOnlyHint != nil && *annotations.ReadOnlyHint
	if isReadOnlyTool {
		choices = append(choices, &ux.SelectChoice{
			Value: "readonly_server",
			Label: "Allow all read-only tools from this server",
		})

		choices = append(choices, &ux.SelectChoice{
			Value: "readonly_global",
			Label: "Allow all read-only tools from any server",
		})
	}

	selector := ux.NewSelect(&ux.SelectOptions{
		Message:         message,
		HelpMessage:     helpMessage,
		Choices:         choices,
		EnableFiltering: ux.Ptr(false),
		DisplayCount:    10,
	})

	choiceIndex, err := selector.Ask(ctx)
	if err != nil {
		return "", err
	}

	if choiceIndex == nil || *choiceIndex < 0 || *choiceIndex >= len(choices) {
		return "", fmt.Errorf("invalid choice selected")
	}

	return choices[*choiceIndex].Value, nil
}

// isServerAlreadyTrusted checks if the server is already trusted
func (cc *ConsentChecker) isServerAlreadyTrusted(ctx context.Context) bool {
	// Create a mock tool request to check if server has full trust
	request := ConsentRequest{
		ToolID:      fmt.Sprintf("%s/test-tool", cc.serverName),
		ServerName:  cc.serverName,
		SessionID:   "",                   // Not needed since each manager represents one session
		Annotations: mcp.ToolAnnotation{}, // No readonly hint
	}

	// Check if server has full trust (not readonly-only)
	decision, err := cc.consentMgr.CheckConsent(ctx, request)
	if err != nil {
		return false
	}

	// Server is trusted if it's allowed and the reason indicates server-level trust
	return decision.Allowed && (decision.Reason == "server trusted" || decision.Reason == "server_always")
}

// grantConsentFromChoice processes the user's consent choice and saves the appropriate rule
func (cc *ConsentChecker) grantConsentFromChoice(ctx context.Context, toolID string, choice string) error {
	var rule ConsentRule
	var scope ConsentScope

	switch choice {
	case "once":
		rule = ConsentRule{
			ToolID:     toolID,
			Permission: ConsentOnce,
		}
		scope = ScopeSession
	case "session":
		rule = ConsentRule{
			ToolID:     toolID,
			Permission: ConsentSession,
		}
		scope = ScopeSession
	case "project":
		rule = ConsentRule{
			ToolID:     toolID,
			Permission: ConsentProject,
		}
		scope = ScopeProject
	case "always":
		rule = ConsentRule{
			ToolID:     toolID,
			Permission: ConsentAlways,
		}
		scope = ScopeGlobal
	case "server":
		// Grant trust to entire server
		rule = ConsentRule{
			ToolID:     fmt.Sprintf("%s/*", cc.serverName),
			Permission: ConsentServerAlways,
			RuleScope:  RuleScopeAll,
		}
		scope = ScopeGlobal
	case "readonly_server":
		// Grant trust to readonly tools from this server
		rule = ConsentRule{
			ToolID:     fmt.Sprintf("%s/*", cc.serverName),
			Permission: ConsentAlways,
			RuleScope:  RuleScopeReadOnly,
		}
		scope = ScopeGlobal
	case "readonly_global":
		// Grant trust to all readonly tools globally
		rule = ConsentRule{
			ToolID:     "*",
			Permission: ConsentAlways,
			RuleScope:  RuleScopeReadOnly,
		}
		scope = ScopeGlobal
	default:
		return fmt.Errorf("unknown consent choice: %s", choice)
	}

	return cc.consentMgr.GrantConsent(ctx, rule, scope)
}
