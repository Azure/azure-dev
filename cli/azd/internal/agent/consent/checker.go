// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package consent

import (
	"context"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/pkg/output"
	"github.com/azure/azure-dev/pkg/ux"
	"github.com/mark3labs/mcp-go/mcp"
)

var ErrToolExecutionDenied = fmt.Errorf("tool execution denied by user")
var ErrSamplingDenied = fmt.Errorf("sampling denied by user")
var ErrElicitationDenied = fmt.Errorf("elicitation denied by user")

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
	toolId := fmt.Sprintf("%s/%s", cc.serverName, toolName)

	// Create consent request
	consentRequest := ConsentRequest{
		ToolID:      toolId,
		ServerName:  cc.serverName,
		Operation:   OperationTypeTool, // This is a tool execution request
		Annotations: annotations,
	}

	return cc.consentMgr.CheckConsent(ctx, consentRequest)
}

// CheckSamplingConsent checks sampling consent for a specific tool
func (cc *ConsentChecker) CheckSamplingConsent(
	ctx context.Context,
	toolName string,
) (*ConsentDecision, error) {
	toolId := fmt.Sprintf("%s/%s", cc.serverName, toolName)

	// Create consent request for sampling
	consentRequest := ConsentRequest{
		ToolID:     toolId,
		ServerName: cc.serverName,
		Operation:  OperationTypeSampling, // This is a sampling request
	}

	return cc.consentMgr.CheckConsent(ctx, consentRequest)
}

// CheckElicitationConsent checks elicitation consent for a specific tool
func (cc *ConsentChecker) CheckElicitationConsent(
	ctx context.Context,
	toolName string,
) (*ConsentDecision, error) {
	toolId := fmt.Sprintf("%s/%s", cc.serverName, toolName)

	// Create consent request for sampling
	consentRequest := ConsentRequest{
		ToolID:     toolId,
		ServerName: cc.serverName,
		Operation:  OperationTypeElicitation, // This is a elicitation request
	}

	return cc.consentMgr.CheckConsent(ctx, consentRequest)
}

// PromptAndGrantConsent shows consent prompt and grants permission based on user choice
func (cc *ConsentChecker) PromptAndGrantConsent(
	ctx context.Context,
	toolName, toolDesc string,
	annotations mcp.ToolAnnotation,
) error {
	toolId := fmt.Sprintf("%s/%s", cc.serverName, toolName)

	choice, err := cc.promptForToolConsent(ctx, toolName, toolDesc, annotations)
	if err != nil {
		return err
	}

	if choice == "deny" {
		return ErrToolExecutionDenied
	}

	// Grant consent based on user choice
	return cc.grantConsentFromChoice(ctx, toolId, choice, OperationTypeTool)
}

// PromptAndGrantReadOnlyToolConsent shows consent prompt and grants permission based on user choice for read only tools
func (cc *ConsentChecker) PromptAndGrantReadOnlyToolConsent(
	ctx context.Context,
) error {
	choice, err := cc.promptForReadOnlyToolConsent(ctx)
	if err != nil {
		return err
	}

	// deny is for No, ask me for each tool, so we skip
	if choice == "deny" {
		return nil
	}

	// Grant consent based on user choice
	return cc.grantConsentFromChoice(ctx, "*/*", choice, OperationTypeTool)
}

// PromptAndGrantSamplingConsent shows sampling consent prompt and grants permission based on user choice
func (cc *ConsentChecker) PromptAndGrantSamplingConsent(
	ctx context.Context,
	toolName, toolDesc string,
) error {
	toolId := fmt.Sprintf("%s/%s", cc.serverName, toolName)

	choice, err := cc.promptForSamplingConsent(ctx, toolName, toolDesc)
	if err != nil {
		return fmt.Errorf("sampling consent prompt failed: %w", err)
	}

	if choice == "deny" {
		return ErrSamplingDenied
	}

	// Grant sampling consent based on user choice
	return cc.grantConsentFromChoice(ctx, toolId, choice, OperationTypeSampling)
}

// PromptAndGrantElicitationConsent shows elicitation consent prompt and grants permission based on user choice
func (cc *ConsentChecker) PromptAndGrantElicitationConsent(
	ctx context.Context,
	toolName, toolDesc string,
) error {
	toolId := fmt.Sprintf("%s/%s", cc.serverName, toolName)

	choice, err := cc.promptForElicitationConsent(ctx, toolName, toolDesc)
	if err != nil {
		return fmt.Errorf("elicitation consent prompt failed: %w", err)
	}

	if choice == "deny" {
		return ErrElicitationDenied
	}

	// Grant elicitation consent based on user choice
	return cc.grantConsentFromChoice(ctx, toolId, choice, OperationTypeElicitation)
}

// Private Struct Methods

// formatToolDescriptionWithAnnotations creates a formatted description with tool annotations as bullet points
func (cc *ConsentChecker) formatToolDescriptionWithAnnotations(
	toolDesc string,
	annotations mcp.ToolAnnotation,
) string {
	if toolDesc == "" {
		toolDesc = "No description available"
	}

	// Start with the base description
	description := toolDesc

	// Collect annotation information
	var annotationBullets []string

	if annotations.Title != "" {
		annotationBullets = append(annotationBullets, fmt.Sprintf("• Title: %s", annotations.Title))
	}

	if annotations.ReadOnlyHint != nil {
		if *annotations.ReadOnlyHint {
			annotationBullets = append(annotationBullets, "• Read-only operation")
		} else {
			annotationBullets = append(annotationBullets, "• May modify data")
		}
	}

	if annotations.DestructiveHint != nil {
		if *annotations.DestructiveHint {
			annotationBullets = append(annotationBullets, "• Potentially destructive operation")
		} else {
			annotationBullets = append(annotationBullets, "• Non-destructive operation")
		}
	}

	if annotations.IdempotentHint != nil {
		if *annotations.IdempotentHint {
			annotationBullets = append(annotationBullets, "• Idempotent (safe to retry)")
		} else {
			annotationBullets = append(annotationBullets, "• Not idempotent (may have side effects on retry)")
		}
	}

	if annotations.OpenWorldHint != nil {
		if *annotations.OpenWorldHint {
			annotationBullets = append(annotationBullets, "• May access external resources")
		} else {
			annotationBullets = append(annotationBullets, "• Operates on local resources only")
		}
	}

	// Append annotations as bullet list if any exist
	if len(annotationBullets) > 0 {
		description += "\n\nTool characteristics:"
		for _, bullet := range annotationBullets {
			description += "\n" + bullet
		}
	}

	return description
}

// promptForToolConsent shows an interactive consent prompt and returns the user's choice
func (cc *ConsentChecker) promptForToolConsent(
	ctx context.Context,
	toolName, toolDesc string,
	annotations mcp.ToolAnnotation,
) (string, error) {
	message := fmt.Sprintf(
		"Allow %s tool from %s server?",
		output.WithHighLightFormat(toolName),
		output.WithHighLightFormat(cc.serverName),
	)

	helpMessage := cc.formatToolDescriptionWithAnnotations(toolDesc, annotations)

	choices := []*ux.SelectChoice{
		{
			Value: "always",
			Label: "Yes, always allow this tool",
		},
		{
			Value: "session",
			Label: "Yes, until I restart azd",
		},
	}

	// Add server trust option if not already trusted
	if !cc.isServerAlreadyTrusted(ctx, OperationTypeTool) {
		choices = append(choices, &ux.SelectChoice{
			Value: "server",
			Label: fmt.Sprintf("Yes, always allow all tools from %s server", cc.serverName),
		})
	}

	choices = append(choices, &ux.SelectChoice{
		Value: "deny",
		Label: "No, block this tool and exit interaction",
	})

	selector := ux.NewSelect(&ux.SelectOptions{
		Message:         message,
		HelpMessage:     helpMessage,
		Choices:         choices,
		EnableFiltering: ux.Ptr(false),
		DisplayCount:    4,
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

func (cc *ConsentChecker) promptForReadOnlyToolConsent(
	ctx context.Context,
) (string, error) {
	message := "Allow all read-only tools by default?"
	helpMessage := "Read-only tools can read your local files and environment but cannot make any changes."

	choices := []*ux.SelectChoice{
		{
			Value: "deny",
			Label: "No, ask me for each tool",
		},
		{
			Value: "readonly_session",
			Label: "Yes, until I restart azd",
		},
	}

	choices = append(choices, &ux.SelectChoice{
		Value: "readonly_global",
		Label: "Yes, always allow read-only tools",
	})

	selector := ux.NewSelect(&ux.SelectOptions{
		Message:         message,
		HelpMessage:     helpMessage,
		Choices:         choices,
		EnableFiltering: ux.Ptr(false),
		DisplayCount:    3,
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

// isServerAlreadyTrusted checks if the server is already trusted for the specified operation context
func (cc *ConsentChecker) isServerAlreadyTrusted(ctx context.Context, operation OperationType) bool {
	// Create a mock request to check if server has trust for the specified operation context
	request := ConsentRequest{
		ToolID:     fmt.Sprintf("%s/test-tool", cc.serverName),
		ServerName: cc.serverName,
		Operation:  operation,
	}

	// For tool requests, add annotations to avoid readonly-only matches
	if operation == OperationTypeTool {
		request.Annotations = mcp.ToolAnnotation{} // No readonly hint
	}

	// Check if server has trust for this operation context
	decision, err := cc.consentMgr.CheckConsent(ctx, request)
	if err != nil {
		return false
	}

	// Server is trusted if it's allowed
	return decision.Allowed
}

// grantConsentFromChoice processes the user's consent choice and saves the appropriate rule
func (cc *ConsentChecker) grantConsentFromChoice(
	ctx context.Context,
	toolId string,
	choice string,
	operation OperationType,
) error {
	var rule ConsentRule

	// Parse server and tool from toolId
	parts := strings.Split(toolId, "/")
	if len(parts) != 2 {
		return fmt.Errorf("invalid toolId format: %s", toolId)
	}
	serverName := parts[0]
	toolName := parts[1]

	switch choice {
	case "once":
		rule = ConsentRule{
			Scope:      ScopeOneTime,
			Target:     NewToolTarget(serverName, toolName),
			Action:     ActionAny,
			Operation:  operation,
			Permission: PermissionAllow,
		}
	case "session":
		rule = ConsentRule{
			Scope:      ScopeSession,
			Target:     NewToolTarget(serverName, toolName),
			Action:     ActionAny,
			Operation:  operation,
			Permission: PermissionAllow,
		}
	case "project":
		rule = ConsentRule{
			Scope:      ScopeProject,
			Target:     NewToolTarget(serverName, toolName),
			Action:     ActionAny,
			Operation:  operation,
			Permission: PermissionAllow,
		}
	case "always":
		rule = ConsentRule{
			Scope:      ScopeGlobal,
			Target:     NewToolTarget(serverName, toolName),
			Action:     ActionAny,
			Operation:  operation,
			Permission: PermissionAllow,
		}
	case "server":
		// Grant trust to entire server
		rule = ConsentRule{
			Scope:      ScopeGlobal,
			Target:     NewServerTarget(serverName),
			Action:     ActionAny,
			Operation:  operation,
			Permission: PermissionAllow,
		}
	case "global":
		rule = ConsentRule{
			Scope:      ScopeGlobal,
			Target:     NewGlobalTarget(),
			Action:     ActionAny,
			Operation:  operation,
			Permission: PermissionAllow,
		}
	case "readonly_session":
		// Grant trust to all readonly tools until azd restart (only for tool context)
		if operation != OperationTypeTool {
			return fmt.Errorf("readonly session option only available for tool consent")
		}
		rule = ConsentRule{
			Scope:      ScopeSession,
			Target:     NewGlobalTarget(),
			Action:     ActionReadOnly,
			Operation:  operation,
			Permission: PermissionAllow,
		}
	case "readonly_global":
		// Grant trust to all readonly tools globally (only for tool context)
		if operation != OperationTypeTool {
			return fmt.Errorf("readonly global option only available for tool consent")
		}
		rule = ConsentRule{
			Scope:      ScopeGlobal,
			Target:     NewGlobalTarget(),
			Action:     ActionReadOnly,
			Operation:  operation,
			Permission: PermissionAllow,
		}
	default:
		return fmt.Errorf("unknown consent choice: %s", choice)
	}

	return cc.consentMgr.GrantConsent(ctx, rule)
}

// promptForSamplingConsent shows an interactive sampling consent prompt and returns the user's choice
func (cc *ConsentChecker) promptForSamplingConsent(
	ctx context.Context,
	toolName, toolDesc string,
) (string, error) {
	message := fmt.Sprintf(
		"Allow tool %s from server %s to communicate with the AI Model?",
		output.WithHighLightFormat(toolName),
		output.WithHighLightFormat(cc.serverName),
	)

	helpMessage := fmt.Sprintf("This will allow the tool to send data to an LLM for analysis or generation. %s", toolDesc)

	choices := []*ux.SelectChoice{
		{
			Value: "once",
			Label: "Yes, just this time",
		},
		{
			Value: "session",
			Label: "Yes, until I restart azd",
		},
		{
			Value: "project",
			Label: "Yes, remember for this project",
		},
		{
			Value: "always",
			Label: "Yes, always allow this tool",
		},
		{
			Value: "deny",
			Label: "No - Don't send data",
		},
	}

	// Add server trust option if not already trusted for sampling
	if !cc.isServerAlreadyTrusted(ctx, OperationTypeSampling) {
		choices = append(choices, &ux.SelectChoice{
			Value: "server",
			Label: "Allow sampling for all tools from this server",
		})
	}

	// Add global sampling trust option
	choices = append(choices, &ux.SelectChoice{
		Value: "global",
		Label: "Allow sampling for all tools from any server",
	})

	selector := ux.NewSelect(&ux.SelectOptions{
		Message:         message,
		HelpMessage:     helpMessage,
		Choices:         choices,
		EnableFiltering: ux.Ptr(false),
		DisplayCount:    5,
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

// promptForElicitationConsent shows an interactive elicitation consent prompt and returns the user's choice
func (cc *ConsentChecker) promptForElicitationConsent(
	ctx context.Context,
	toolName, toolDesc string,
) (string, error) {
	message := fmt.Sprintf(
		"Allow %s tool from %s server to collect additional information?",
		output.WithHighLightFormat(toolName),
		output.WithHighLightFormat(cc.serverName),
	)

	helpMessage := fmt.Sprintf("This will allow the tool prompt you for additional information as needed. %s", toolDesc)

	choices := []*ux.SelectChoice{
		{
			Value: "once",
			Label: "Yes, just this time",
		},
		{
			Value: "session",
			Label: "Yes, until I restart azd",
		},
		{
			Value: "project",
			Label: "Yes, remember for this project",
		},
		{
			Value: "always",
			Label: "Yes, always allow for this tool",
		},
		{
			Value: "deny",
			Label: "No - Don't collect data",
		},
	}

	// Add server trust option if not already trusted for elicitation
	if !cc.isServerAlreadyTrusted(ctx, OperationTypeElicitation) {
		choices = append(choices, &ux.SelectChoice{
			Value: "server",
			Label: "Allow elicitation for all tools from this server",
		})
	}

	// Add global elicitation trust option
	choices = append(choices, &ux.SelectChoice{
		Value: "global",
		Label: "Allow elicitation for all tools from any server",
	})

	selector := ux.NewSelect(&ux.SelectOptions{
		Message:         message,
		HelpMessage:     helpMessage,
		Choices:         choices,
		EnableFiltering: ux.Ptr(false),
		DisplayCount:    5,
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
