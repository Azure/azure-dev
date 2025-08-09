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

// CheckToolConsent checks if a tool execution should be allowed
func (cc *ConsentChecker) CheckToolConsent(ctx context.Context, toolName, toolDesc string) (*ConsentDecision, error) {
	return cc.CheckToolConsentWithAnnotations(ctx, toolName, toolDesc, nil)
}

// CheckToolConsentWithAnnotations checks tool consent with optional MCP annotations
func (cc *ConsentChecker) CheckToolConsentWithAnnotations(ctx context.Context, toolName, toolDesc string, annotations *mcp.ToolAnnotation) (*ConsentDecision, error) {
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
func (cc *ConsentChecker) PromptAndGrantConsent(ctx context.Context, toolName, toolDesc string) error {
	toolID := fmt.Sprintf("%s/%s", cc.serverName, toolName)

	choice, err := cc.promptForConsent(ctx, toolName, toolDesc)
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
func (cc *ConsentChecker) promptForConsent(ctx context.Context, toolName, toolDesc string) (string, error) {
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
			Label: fmt.Sprintf("Trust server '%s' - Allow all tools from this server", cc.serverName),
		})
	}

	selector := ux.NewSelect(&ux.SelectOptions{
		Message:         message,
		HelpMessage:     helpMessage,
		Choices:         choices,
		EnableFiltering: ux.Ptr(false),
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
	request := ConsentRequest{
		ServerName: cc.serverName,
		SessionID:  "", // Not needed since each manager represents one session
	}

	// Create a mock consent request to check if server is trusted
	decision, err := cc.consentMgr.CheckConsent(ctx, request)
	if err != nil {
		return false
	}

	return decision.Allowed && decision.Reason == "trusted server"
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
		}
		scope = ScopeGlobal
	default:
		return fmt.Errorf("unknown consent choice: %s", choice)
	}

	return cc.consentMgr.GrantConsent(ctx, rule, scope)
}
