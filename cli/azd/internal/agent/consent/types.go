// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package consent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/common"
	"github.com/mark3labs/mcp-go/mcp"
)

// Scope defines the rule applicability level
type Scope string

const (
	ScopeSession Scope = "session"
	ScopeProject Scope = "project"
	ScopeGlobal  Scope = "global"
)

// ActionType defines the kind of action the rule controls
type ActionType string

const (
	ActionReadOnly ActionType = "readonly"
	ActionAny      ActionType = "any"
)

// OperationType defines the feature or context for the rule
type OperationType string

const (
	OperationTypeTool     OperationType = "tool"     // running tools
	OperationTypeSampling OperationType = "sampling" // sampling requests
)

// Permission is the consent outcome for a rule
type Permission string

const (
	PermissionAllow  Permission = "allow"
	PermissionDeny   Permission = "deny"
	PermissionPrompt Permission = "prompt"
)

// Target is a consolidated string combining server and tool in the form "server/tool"
// Wildcards supported, e.g., "server/*" means all tools in that server, "*" or "*/*" means all servers/tools
type Target string

// NewToolTarget creates a target for a specific tool
func NewToolTarget(server, tool string) Target {
	return Target(fmt.Sprintf("%s/%s", server, tool))
}

// NewServerTarget creates a target for all tools in a server
func NewServerTarget(server string) Target {
	return Target(fmt.Sprintf("%s/*", server))
}

// NewGlobalTarget creates a target for all servers and tools
func NewGlobalTarget() Target {
	return Target("*/*")
}

// Validate checks if the target format is valid
func (t Target) Validate() error {
	str := string(t)
	if str == "" {
		return fmt.Errorf("target cannot be empty")
	}
	if str == "*" || str == "*/*" {
		return nil // Global wildcards are valid
	}
	parts := strings.Split(str, "/")
	if len(parts) != 2 {
		return fmt.Errorf("target must be in format 'server/tool', 'server/*', or '*'")
	}
	if parts[0] == "" {
		return fmt.Errorf("server part of target cannot be empty")
	}
	if parts[1] == "" {
		return fmt.Errorf("tool part of target cannot be empty")
	}
	return nil
}

// AllowedOperationContexts contains the valid operation contexts for command validation
var AllowedOperationContexts = []string{
	string(OperationTypeTool),
	string(OperationTypeSampling),
}

// ParseOperationContext converts a string to OperationContext with validation
func ParseOperationContext(contextStr string) (OperationType, error) {
	for _, allowedContext := range AllowedOperationContexts {
		if contextStr == allowedContext {
			return OperationType(contextStr), nil
		}
	}
	return "", fmt.Errorf("invalid operation context: %s (allowed: %v)", contextStr, AllowedOperationContexts)
}

// ConsentRule represents a single consent rule entry
type ConsentRule struct {
	Scope      Scope         `json:"scope"`
	Target     Target        `json:"target"` // e.g. "myServer/myTool", "myServer/*", "*"
	Action     ActionType    `json:"action"`
	Operation  OperationType `json:"operation"`
	Permission Permission    `json:"permission"`
	GrantedAt  time.Time     `json:"grantedAt"`
}

// Validate checks if the consent rule is valid
func (r ConsentRule) Validate() error {
	if err := r.Target.Validate(); err != nil {
		return fmt.Errorf("invalid target: %w", err)
	}

	// Validate enums have valid values
	validScopes := []Scope{ScopeSession, ScopeProject, ScopeGlobal}
	validScope := false
	for _, scope := range validScopes {
		if r.Scope == scope {
			validScope = true
			break
		}
	}
	if !validScope {
		return fmt.Errorf("invalid scope: %s", r.Scope)
	}

	validActions := []ActionType{ActionReadOnly, ActionAny}
	validAction := false
	for _, action := range validActions {
		if r.Action == action {
			validAction = true
			break
		}
	}
	if !validAction {
		return fmt.Errorf("invalid action: %s", r.Action)
	}

	validContexts := []OperationType{OperationTypeTool, OperationTypeSampling}
	validContext := false
	for _, context := range validContexts {
		if r.Operation == context {
			validContext = true
			break
		}
	}
	if !validContext {
		return fmt.Errorf("invalid operation context: %s", r.Operation)
	}

	validDecisions := []Permission{PermissionAllow, PermissionDeny, PermissionPrompt}
	validDecision := false
	for _, decision := range validDecisions {
		if r.Permission == decision {
			validDecision = true
			break
		}
	}
	if !validDecision {
		return fmt.Errorf("invalid decision: %s", r.Permission)
	}

	return nil
}

// ConsentConfig represents the MCP consent configuration
type ConsentConfig struct {
	Rules []ConsentRule `json:"rules,omitempty"`
}

// ConsentRequest represents a request to check consent for a tool
type ConsentRequest struct {
	ToolID           string
	ServerName       string
	OperationContext OperationType // Type of consent being requested (tool, sampling, etc.)
	Parameters       map[string]interface{}
	SessionID        string
	ProjectPath      string
	Annotations      mcp.ToolAnnotation
}

// ConsentDecision represents the result of a consent check
type ConsentDecision struct {
	Allowed        bool
	Reason         string
	RequiresPrompt bool
}

// ConsentManager manages consent rules and decisions
type ConsentManager interface {
	CheckConsent(ctx context.Context, request ConsentRequest) (*ConsentDecision, error)
	GrantConsent(ctx context.Context, rule ConsentRule, scope Scope) error
	ListConsents(ctx context.Context, scope Scope) ([]ConsentRule, error)
	ListConsentsByOperationContext(
		ctx context.Context,
		scope Scope,
		operationContext OperationType,
	) ([]ConsentRule, error)
	ClearConsents(ctx context.Context, scope Scope) error
	ClearConsentsByOperationContext(ctx context.Context, scope Scope, operationContext OperationType) error
	ClearConsentByTarget(ctx context.Context, target Target, scope Scope) error

	// Tool wrapping methods
	WrapTool(tool common.AnnotatedTool) common.AnnotatedTool
	WrapTools(tools []common.AnnotatedTool) []common.AnnotatedTool
}

type ExecutingTool struct {
	sync.RWMutex
	Name   string
	Server string
}
