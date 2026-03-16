// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package consent

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/mark3labs/mcp-go/mcp"
)

// PromptWorkflowConsent on consentManager implements the ConsentManager interface method.
// It shows an upfront consent prompt asking the user whether to grant blanket access to the
// given MCP tool servers. If all servers are already trusted, the prompt is skipped.
func (cm *consentManager) PromptWorkflowConsent(ctx context.Context, servers []string) error {
	// Skip if every server is already trusted for tool execution
	if allServersTrusted(ctx, cm, servers) {
		return nil
	}

	scope, err := promptForWorkflowConsent(ctx, servers)
	if err != nil {
		return err
	}

	// Empty scope means the user chose "No, prompt me for each operation" — no rules to add
	if scope == "" {
		return nil
	}

	return grantWorkflowRules(ctx, cm, servers, scope)
}

// allServersTrusted returns true when every server already has a consent rule that
// would allow tool execution (any action, not just read-only).
func allServersTrusted(ctx context.Context, mgr ConsentManager, servers []string) bool {
	for _, server := range servers {
		request := ConsentRequest{
			ToolID:      fmt.Sprintf("%s/test-tool", server),
			ServerName:  server,
			Operation:   OperationTypeTool,
			Annotations: mcp.ToolAnnotation{}, // no read-only hint — checks for full access
		}

		decision, err := mgr.CheckConsent(ctx, request)
		if err != nil || !decision.Allowed {
			return false
		}
	}

	return true
}

// promptForWorkflowConsent displays the consent choices and returns the selected scope
// (ScopeSession or ScopeGlobal), or empty string for "no, prompt me".
func promptForWorkflowConsent(ctx context.Context, servers []string) (Scope, error) {
	serverList := strings.Join(servers, ", ")

	message := fmt.Sprintf(
		"Grant access to tools for %s to read and write files in your current workspace?",
		output.WithHighLightFormat(serverList),
	)

	helpMessage := "This allows the agent workflow to use built-in tools (file read/write, " +
		"Azure MCP, azd CLI) without prompting for each tool individually.\n\n" +
		"This command does not create or provision any resources in Azure. " +
		"It only generates configuration files locally in your workspace.\n\n" +
		"You can review or revoke permissions at any time with: azd copilot consent list / revoke"

	choices := []*ux.SelectChoice{
		{
			Value: string(ScopeSession),
			Label: "Yes, approve for this session",
		},
		{
			Value: string(ScopeGlobal),
			Label: "Yes, always approve",
		},
		{
			Value: "prompt",
			Label: "No, prompt me for each operation",
		},
	}

	selector := ux.NewSelect(&ux.SelectOptions{
		Message:         message,
		HelpMessage:     helpMessage,
		Choices:         choices,
		EnableFiltering: new(false),
		DisplayCount:    len(choices),
	})

	choiceIndex, err := selector.Ask(ctx)
	if err != nil {
		return "", err
	}

	if choiceIndex == nil || *choiceIndex < 0 || *choiceIndex >= len(choices) {
		return "", fmt.Errorf("invalid choice selected")
	}

	selected := choices[*choiceIndex].Value
	if selected == "prompt" {
		return "", nil
	}

	return Scope(selected), nil
}

// grantWorkflowRules creates server-level allow rules for every server for tool operations.
func grantWorkflowRules(ctx context.Context, mgr ConsentManager, servers []string, scope Scope) error {
	now := time.Now()

	for _, server := range servers {
		rule := ConsentRule{
			Scope:      scope,
			Target:     NewServerTarget(server),
			Action:     ActionAny,
			Operation:  OperationTypeTool,
			Permission: PermissionAllow,
			GrantedAt:  now,
		}

		if err := mgr.GrantConsent(ctx, rule); err != nil {
			log.Printf("[consent] failed to persist workflow consent for %s (scope=%s): %v",
				server, scope, err)
		}
	}

	return nil
}
