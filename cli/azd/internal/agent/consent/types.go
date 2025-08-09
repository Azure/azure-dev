// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package consent

import (
	"context"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tmc/langchaingo/tools"
)

// ConsentLevel represents the level of consent granted for a tool
type ConsentLevel string

// ConsentScope represents where consent rules are stored
type ConsentScope string

const (
	ConsentDeny         ConsentLevel = "deny"
	ConsentPrompt       ConsentLevel = "prompt"
	ConsentOnce         ConsentLevel = "once"
	ConsentSession      ConsentLevel = "session"
	ConsentProject      ConsentLevel = "project"
	ConsentAlways       ConsentLevel = "always"
	ConsentServerAlways ConsentLevel = "server_always" // All tools from server
)

const (
	ScopeGlobal  ConsentScope = "global"
	ScopeProject ConsentScope = "project"
	ScopeSession ConsentScope = "session"
)

// ConsentRule represents a single consent rule for a tool
type ConsentRule struct {
	ToolID     string       `json:"tool_id"`
	Permission ConsentLevel `json:"permission"`
	GrantedAt  time.Time    `json:"granted_at"`
}

// ConsentConfig represents the MCP consent configuration
type ConsentConfig struct {
	Rules              []ConsentRule `json:"rules,omitempty"`
	AllowReadOnlyTools bool          `json:"allow_readonly_tools,omitempty"`
	TrustedServers     []string      `json:"trusted_servers,omitempty"`
}

// ConsentRequest represents a request to check consent for a tool
type ConsentRequest struct {
	ToolID      string
	ServerName  string
	Parameters  map[string]interface{}
	SessionID   string
	ProjectPath string
	Annotations *mcp.ToolAnnotation
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
	GrantConsent(ctx context.Context, rule ConsentRule, scope ConsentScope) error
	ListConsents(ctx context.Context, scope ConsentScope) ([]ConsentRule, error)
	ClearConsents(ctx context.Context, scope ConsentScope) error
	ClearConsentByToolID(ctx context.Context, toolID string, scope ConsentScope) error

	// Tool wrapping methods
	WrapTool(tool tools.Tool) tools.Tool
	WrapTools(tools []tools.Tool) []tools.Tool
}
