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

// AllowedOperationTypes contains the valid operation contexts for command validation
var AllowedOperationTypes = []string{
	string(OperationTypeTool),
	string(OperationTypeSampling),
}

// ParseOperationType converts a string to OperationType with validation
func ParseOperationType(contextStr string) (OperationType, error) {
	for _, allowedContext := range AllowedOperationTypes {
		if contextStr == allowedContext {
			return OperationType(contextStr), nil
		}
	}
	return "", fmt.Errorf("invalid operation context: %s (allowed: %v)", contextStr, AllowedOperationTypes)
}

// AllowedScopes contains the valid scopes for command validation
var AllowedScopes = []string{
	string(ScopeGlobal),
	string(ScopeProject),
	string(ScopeSession),
}

// ParseScope converts a string to Scope with validation
func ParseScope(scopeStr string) (Scope, error) {
	for _, allowedScope := range AllowedScopes {
		if scopeStr == allowedScope {
			return Scope(scopeStr), nil
		}
	}
	return "", fmt.Errorf("invalid scope: %s (allowed: %v)", scopeStr, AllowedScopes)
}

// AllowedActionTypes contains the valid action types for command validation
var AllowedActionTypes = []string{
	"readonly",
	"all",
}

// ParseActionType converts a string to ActionType with validation
func ParseActionType(actionStr string) (ActionType, error) {
	switch actionStr {
	case "readonly":
		return ActionReadOnly, nil
	case "all":
		return ActionAny, nil
	default:
		return "", fmt.Errorf("invalid action type: %s (allowed: %v)", actionStr, AllowedActionTypes)
	}
}

// AllowedPermissions contains the valid permissions for command validation
var AllowedPermissions = []string{
	string(PermissionAllow),
	string(PermissionDeny),
	string(PermissionPrompt),
}

// ParsePermission converts a string to Permission with validation
func ParsePermission(permissionStr string) (Permission, error) {
	for _, allowedPermission := range AllowedPermissions {
		if permissionStr == allowedPermission {
			return Permission(permissionStr), nil
		}
	}
	return "", fmt.Errorf("invalid permission: %s (allowed: %v)", permissionStr, AllowedPermissions)
}

// FilterOption represents a functional option for filtering consent rules
type FilterOption func(*FilterOptions)

// FilterOptions contains the filtering options for listing consent rules
type FilterOptions struct {
	Scope      *Scope
	Operation  *OperationType
	Target     *Target
	Action     *ActionType
	Permission *Permission
}

// WithScope filters rules by scope
func WithScope(scope Scope) FilterOption {
	return func(opts *FilterOptions) {
		opts.Scope = &scope
	}
}

// WithOperation filters rules by operation type
func WithOperation(operation OperationType) FilterOption {
	return func(opts *FilterOptions) {
		opts.Operation = &operation
	}
}

// WithTarget filters rules by target pattern
func WithTarget(target Target) FilterOption {
	return func(opts *FilterOptions) {
		opts.Target = &target
	}
}

// WithAction filters rules by action type
func WithAction(action ActionType) FilterOption {
	return func(opts *FilterOptions) {
		opts.Action = &action
	}
}

// WithPermission filters rules by permission type
func WithPermission(permission Permission) FilterOption {
	return func(opts *FilterOptions) {
		opts.Permission = &permission
	}
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
	ToolID      string
	ServerName  string
	Operation   OperationType // Type of consent being requested (tool, sampling, etc.)
	Parameters  map[string]interface{}
	Annotations mcp.ToolAnnotation
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
	GrantConsent(ctx context.Context, rule ConsentRule) error
	ListConsentRules(ctx context.Context, options ...FilterOption) ([]ConsentRule, error)
	ClearConsentRules(ctx context.Context, options ...FilterOption) error

	// Environment context methods
	IsProjectScopeAvailable(ctx context.Context) bool

	// Tool wrapping methods
	WrapTool(tool common.AnnotatedTool) common.AnnotatedTool
	WrapTools(tools []common.AnnotatedTool) []common.AnnotatedTool
}

type ExecutingTool struct {
	sync.RWMutex
	Name   string
	Server string
}
