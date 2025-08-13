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
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
)

const (
	ConfigKeyMCPConsent = "mcp.consent"
)

// Global state for tracking current executing tool
// This is a work around right now since the MCP protocol does not contain enough information in the sampling requests
// Specifically, the tool name and server are not included in the request context
var (
	executingTool = &ExecutingTool{}
)

// SetCurrentExecutingTool sets the currently executing tool (thread-safe)
func SetCurrentExecutingTool(name, server string) {
	executingTool.Lock()
	defer executingTool.Unlock()
	executingTool.Name = name
	executingTool.Server = server
}

// ClearCurrentExecutingTool clears the currently executing tool (thread-safe)
func ClearCurrentExecutingTool() {
	executingTool.Lock()
	defer executingTool.Unlock()
	executingTool.Name = ""
	executingTool.Server = ""
}

// GetCurrentExecutingTool gets the currently executing tool (thread-safe)
// Returns nil if no tool is currently executing
func GetCurrentExecutingTool() *ExecutingTool {
	executingTool.RLock()
	defer executingTool.RUnlock()

	// Return nil if no tool is currently executing
	if executingTool.Name == "" && executingTool.Server == "" {
		return nil
	}

	// Return a copy to avoid exposing the mutex
	return &ExecutingTool{
		Name:   executingTool.Name,
		Server: executingTool.Server,
	}
}

// consentManager implements the ConsentManager interface
type consentManager struct {
	lazyEnvManager    *lazy.Lazy[environment.Manager]
	console           input.Console
	userConfigManager config.UserConfigManager
	sessionRules      []ConsentRule // Rules for this session
	sessionMutex      sync.RWMutex
}

// NewConsentManager creates a new consent manager
func NewConsentManager(
	lazyEnvManager *lazy.Lazy[environment.Manager],
	console input.Console,
	userConfigManager config.UserConfigManager,
) ConsentManager {
	return &consentManager{
		lazyEnvManager:    lazyEnvManager,
		console:           console,
		userConfigManager: userConfigManager,
		sessionRules:      make([]ConsentRule, 0),
	}
}

// CheckConsent checks if a tool execution should be allowed
func (cm *consentManager) CheckConsent(ctx context.Context, request ConsentRequest) (*ConsentDecision, error) {
	// Check explicit rules across all scopes with unified logic
	if decision := cm.checkUnifiedRules(ctx, request); decision != nil {
		return decision, nil
	}

	// No consent found - require prompt
	return &ConsentDecision{
		Allowed:        false,
		RequiresPrompt: true,
		Reason:         "no consent granted",
	}, nil
}

// GrantConsent grants consent for a tool
func (cm *consentManager) GrantConsent(ctx context.Context, rule ConsentRule, scope Scope) error {
	rule.GrantedAt = time.Now()

	// Validate the rule
	if err := rule.Validate(); err != nil {
		return fmt.Errorf("invalid consent rule: %w", err)
	}

	switch scope {
	case ScopeSession:
		return cm.addSessionRule(rule)
	case ScopeProject:
		return cm.addProjectRule(ctx, rule)
	case ScopeGlobal:
		return cm.addGlobalRule(ctx, rule)
	default:
		return fmt.Errorf("unknown consent scope: %s", scope)
	}
}

// ListConsentRules lists consent rules for a given scope
func (cm *consentManager) ListConsentRules(ctx context.Context, scope Scope) ([]ConsentRule, error) {
	switch scope {
	case ScopeSession:
		return cm.getSessionRules(), nil
	case ScopeProject:
		return cm.getProjectRules(ctx)
	case ScopeGlobal:
		return cm.getGlobalRules(ctx)
	default:
		return nil, fmt.Errorf("unknown consent scope: %s", scope)
	}
}

// ClearConsents clears all consent rules for a given scope
func (cm *consentManager) ClearConsents(ctx context.Context, scope Scope) error {
	switch scope {
	case ScopeSession:
		return cm.clearSessionRules()
	case ScopeProject:
		return cm.clearProjectRules(ctx)
	case ScopeGlobal:
		return cm.clearGlobalRules(ctx)
	default:
		return fmt.Errorf("unknown consent scope: %s", scope)
	}
}

// ClearConsentByTarget clears consent for a specific target
func (cm *consentManager) ClearConsentByTarget(ctx context.Context, target Target, scope Scope) error {
	switch scope {
	case ScopeSession:
		return cm.removeSessionRule(target)
	case ScopeProject:
		return cm.removeProjectRule(ctx, target)
	case ScopeGlobal:
		return cm.removeGlobalRule(ctx, target)
	default:
		return fmt.Errorf("unknown consent scope: %s", scope)
	}
}

// ListConsentsByOperationType lists consent rules filtered by operation context for a given scope
func (cm *consentManager) ListConsentsByOperationType(
	ctx context.Context,
	scope Scope,
	operation OperationType,
) ([]ConsentRule, error) {
	allRules, err := cm.ListConsentRules(ctx, scope)
	if err != nil {
		return nil, err
	}

	filteredRules := make([]ConsentRule, 0)
	for _, rule := range allRules {
		if rule.Operation == operation {
			filteredRules = append(filteredRules, rule)
		}
	}

	return filteredRules, nil
}

// ClearConsentsByOperationType clears all consent rules of a specific operation context for a given scope
func (cm *consentManager) ClearConsentsByOperationType(
	ctx context.Context,
	scope Scope,
	operation OperationType,
) error {
	rules, err := cm.ListConsentsByOperationType(ctx, scope, operation)
	if err != nil {
		return fmt.Errorf("failed to list consent rules: %w", err)
	}

	for _, rule := range rules {
		if err := cm.ClearConsentByTarget(ctx, rule.Target, scope); err != nil {
			return fmt.Errorf("failed to clear consent for target %s: %w", rule.Target, err)
		}
	}

	return nil
}

// IsProjectScopeAvailable checks if project scope is available (i.e., we have an environment context)
func (cm *consentManager) IsProjectScopeAvailable(ctx context.Context) bool {
	envManager, err := cm.lazyEnvManager.GetValue()
	if err != nil {
		return false
	}

	// Try to get the current environment
	_, err = envManager.Get(ctx, "")
	return err == nil
}

// WrapTool wraps a single langchaingo tool with consent protection
func (cm *consentManager) WrapTool(tool common.AnnotatedTool) common.AnnotatedTool {
	return newConsentWrapperTool(tool, cm.console, cm)
}

// WrapTools wraps multiple langchaingo tools with consent protection
func (cm *consentManager) WrapTools(tools []common.AnnotatedTool) []common.AnnotatedTool {
	wrappedTools := make([]common.AnnotatedTool, len(tools))

	for i, tool := range tools {
		wrappedTools[i] = cm.WrapTool(tool)
	}

	return wrappedTools
}

// evaluateRule evaluates a consent rule and returns a decision
func (cm *consentManager) evaluateRule(rule ConsentRule) *ConsentDecision {
	switch rule.Permission {
	case PermissionDeny:
		return &ConsentDecision{Allowed: false, Reason: "explicitly denied"}
	case PermissionPrompt:
		return &ConsentDecision{Allowed: false, RequiresPrompt: true, Reason: "requires prompt"}
	case PermissionAllow:
		return &ConsentDecision{Allowed: true, Reason: "allowed"}
	default:
		return &ConsentDecision{Allowed: false, RequiresPrompt: true, Reason: "unknown decision"}
	}
}

// addSessionRule adds a rule to the session rules
func (cm *consentManager) addSessionRule(rule ConsentRule) error {
	cm.sessionMutex.Lock()
	defer cm.sessionMutex.Unlock()

	cm.sessionRules = append(cm.sessionRules, rule)
	return nil
}

// addProjectRule adds a rule to the project configuration
func (cm *consentManager) addProjectRule(ctx context.Context, rule ConsentRule) error {
	if !cm.IsProjectScopeAvailable(ctx) {
		return fmt.Errorf("project scope is not available (no environment context)")
	}

	envManager, err := cm.lazyEnvManager.GetValue()
	if err != nil {
		return fmt.Errorf("no environment available for project-level consent: %w", err)
	}

	// Get the current environment - this will be the active environment
	env, err := envManager.Get(ctx, "")
	if err != nil {
		return fmt.Errorf("failed to get current environment: %w", err)
	}

	var consentConfig ConsentConfig
	if exists, err := env.Config.GetSection(ConfigKeyMCPConsent, &consentConfig); err != nil {
		return fmt.Errorf("failed to get consent config from environment: %w", err)
	} else if !exists {
		consentConfig = ConsentConfig{}
	}

	// Add or update the rule
	consentConfig.Rules = cm.addOrUpdateRule(consentConfig.Rules, rule)

	if err := env.Config.Set(ConfigKeyMCPConsent, consentConfig); err != nil {
		return fmt.Errorf("failed to set consent config in environment: %w", err)
	}

	return envManager.Save(ctx, env)
}

// addGlobalRule adds a rule to the global configuration
func (cm *consentManager) addGlobalRule(ctx context.Context, rule ConsentRule) error {
	userConfig, err := cm.userConfigManager.Load()
	if err != nil {
		return fmt.Errorf("failed to load user config: %w", err)
	}

	var consentConfig ConsentConfig
	if exists, err := userConfig.GetSection(ConfigKeyMCPConsent, &consentConfig); err != nil {
		return fmt.Errorf("failed to get consent config: %w", err)
	} else if !exists {
		consentConfig = ConsentConfig{}
	}

	// Add or update the rule
	consentConfig.Rules = cm.addOrUpdateRule(consentConfig.Rules, rule)

	if err := userConfig.Set(ConfigKeyMCPConsent, consentConfig); err != nil {
		return fmt.Errorf("failed to set consent config: %w", err)
	}

	return cm.userConfigManager.Save(userConfig)
}

// addOrUpdateRule adds a new rule or updates an existing one
func (cm *consentManager) addOrUpdateRule(rules []ConsentRule, newRule ConsentRule) []ConsentRule {
	// Check if rule already exists and update it
	for i, rule := range rules {
		if rule.Target == newRule.Target && rule.Operation == newRule.Operation &&
			rule.Action == newRule.Action {
			rules[i] = newRule
			return rules
		}
	}

	// Rule doesn't exist, add it
	return append(rules, newRule)
}

// getSessionRules returns session rules for this session
func (cm *consentManager) getSessionRules() []ConsentRule {
	cm.sessionMutex.RLock()
	defer cm.sessionMutex.RUnlock()

	// Return a copy to avoid race conditions
	result := make([]ConsentRule, len(cm.sessionRules))
	copy(result, cm.sessionRules)
	return result
}

// getProjectRules returns project-level consent rules
func (cm *consentManager) getProjectRules(ctx context.Context) ([]ConsentRule, error) {
	if !cm.IsProjectScopeAvailable(ctx) {
		return nil, fmt.Errorf("project scope is not available (no environment context)")
	}

	envManager, err := cm.lazyEnvManager.GetValue()
	if err != nil {
		// No environment available - return empty rules without error
		return []ConsentRule{}, nil
	}

	// Get the current environment - this will be the active environment
	env, err := envManager.Get(ctx, "")
	if err != nil {
		// Environment not found - return empty rules without error
		return []ConsentRule{}, nil
	}

	var consentConfig ConsentConfig
	if exists, err := env.Config.GetSection(ConfigKeyMCPConsent, &consentConfig); err != nil {
		return nil, fmt.Errorf("failed to get consent config from environment: %w", err)
	} else if !exists {
		return []ConsentRule{}, nil
	}

	return consentConfig.Rules, nil
}

// getGlobalRules returns global consent rules
func (cm *consentManager) getGlobalRules(ctx context.Context) ([]ConsentRule, error) {
	config, err := cm.getGlobalConsentConfig(ctx)
	if err != nil {
		return nil, err
	}

	return config.Rules, nil
}

// getGlobalConsentConfig loads the global consent configuration
func (cm *consentManager) getGlobalConsentConfig(ctx context.Context) (*ConsentConfig, error) {
	userConfig, err := cm.userConfigManager.Load()
	if err != nil {
		return nil, fmt.Errorf("failed to load user config: %w", err)
	}

	var consentConfig ConsentConfig
	if exists, err := userConfig.GetSection(ConfigKeyMCPConsent, &consentConfig); err != nil {
		return nil, fmt.Errorf("failed to get consent config: %w", err)
	} else if !exists {
		consentConfig = ConsentConfig{}
	}

	return &consentConfig, nil
}

// clearSessionRules clears all rules for this session
func (cm *consentManager) clearSessionRules() error {
	cm.sessionMutex.Lock()
	defer cm.sessionMutex.Unlock()

	cm.sessionRules = make([]ConsentRule, 0)
	return nil
}

// clearGlobalRules clears all global consent rules
func (cm *consentManager) clearGlobalRules(ctx context.Context) error {
	userConfig, err := cm.userConfigManager.Load()
	if err != nil {
		return fmt.Errorf("failed to load user config: %w", err)
	}

	consentConfig := ConsentConfig{
		Rules: []ConsentRule{},
	}

	if err := userConfig.Set(ConfigKeyMCPConsent, consentConfig); err != nil {
		return fmt.Errorf("failed to clear consent config: %w", err)
	}

	return cm.userConfigManager.Save(userConfig)
}

// clearProjectRules clears all project-level consent rules
func (cm *consentManager) clearProjectRules(ctx context.Context) error {
	if !cm.IsProjectScopeAvailable(ctx) {
		return fmt.Errorf("project scope is not available (no environment context)")
	}

	envManager, err := cm.lazyEnvManager.GetValue()
	if err != nil {
		return fmt.Errorf("no environment available for project-level consent: %w", err)
	}

	// Get the current environment
	env, err := envManager.Get(ctx, "")
	if err != nil {
		return fmt.Errorf("failed to get current environment: %w", err)
	}

	consentConfig := ConsentConfig{
		Rules: []ConsentRule{},
	}

	if err := env.Config.Set(ConfigKeyMCPConsent, consentConfig); err != nil {
		return fmt.Errorf("failed to clear consent config in environment: %w", err)
	}

	return envManager.Save(ctx, env)
}

// removeSessionRule removes a specific rule from session rules
func (cm *consentManager) removeSessionRule(target Target) error {
	cm.sessionMutex.Lock()
	defer cm.sessionMutex.Unlock()

	// Filter out the rule to remove
	filtered := make([]ConsentRule, 0, len(cm.sessionRules))
	for _, rule := range cm.sessionRules {
		if rule.Target != target {
			filtered = append(filtered, rule)
		}
	}

	cm.sessionRules = filtered
	return nil
}

// removeProjectRule removes a specific rule from project configuration
func (cm *consentManager) removeProjectRule(ctx context.Context, target Target) error {
	if !cm.IsProjectScopeAvailable(ctx) {
		return fmt.Errorf("project scope is not available (no environment context)")
	}

	envManager, err := cm.lazyEnvManager.GetValue()
	if err != nil {
		return fmt.Errorf("no environment available for project-level consent: %w", err)
	}

	// Get the current environment
	env, err := envManager.Get(ctx, "")
	if err != nil {
		return fmt.Errorf("failed to get current environment: %w", err)
	}

	var consentConfig ConsentConfig
	if exists, err := env.Config.GetSection(ConfigKeyMCPConsent, &consentConfig); err != nil {
		return fmt.Errorf("failed to get consent config from environment: %w", err)
	} else if !exists {
		return nil // Nothing to remove
	}

	// Filter out the rule to remove
	filtered := make([]ConsentRule, 0, len(consentConfig.Rules))
	for _, rule := range consentConfig.Rules {
		if rule.Target != target {
			filtered = append(filtered, rule)
		}
	}

	consentConfig.Rules = filtered

	if err := env.Config.Set(ConfigKeyMCPConsent, consentConfig); err != nil {
		return fmt.Errorf("failed to update consent config in environment: %w", err)
	}

	return envManager.Save(ctx, env)
}

// removeGlobalRule removes a specific rule from global configuration
func (cm *consentManager) removeGlobalRule(ctx context.Context, target Target) error {
	userConfig, err := cm.userConfigManager.Load()
	if err != nil {
		return fmt.Errorf("failed to load user config: %w", err)
	}

	var consentConfig ConsentConfig
	if exists, err := userConfig.GetSection(ConfigKeyMCPConsent, &consentConfig); err != nil {
		return fmt.Errorf("failed to get consent config: %w", err)
	} else if !exists {
		return nil // Nothing to remove
	}

	// Filter out the rule to remove
	filtered := make([]ConsentRule, 0, len(consentConfig.Rules))
	for _, rule := range consentConfig.Rules {
		if rule.Target != target {
			filtered = append(filtered, rule)
		}
	}

	consentConfig.Rules = filtered

	if err := userConfig.Set(ConfigKeyMCPConsent, consentConfig); err != nil {
		return fmt.Errorf("failed to update consent config: %w", err)
	}

	return cm.userConfigManager.Save(userConfig)
}

// checkUnifiedRules checks rules using the new unified matching logic
func (cm *consentManager) checkUnifiedRules(ctx context.Context, request ConsentRequest) *ConsentDecision {
	isReadOnlyTool := request.Annotations.ReadOnlyHint != nil && *request.Annotations.ReadOnlyHint

	// Build the target for this request - ToolID is already in "server/tool" format
	requestTarget := Target(request.ToolID)

	// Check session rules first
	cm.sessionMutex.RLock()
	sessionRules := cm.sessionRules
	cm.sessionMutex.RUnlock()

	if decision := cm.findMatchingRule(
		sessionRules, requestTarget, request.Operation, isReadOnlyTool,
	); decision != nil {
		return decision
	}

	// Check project rules if environment is available
	if cm.IsProjectScopeAvailable(ctx) {
		if projectRules, err := cm.getProjectRules(ctx); err == nil {
			if decision := cm.findMatchingRule(
				projectRules, requestTarget, request.Operation, isReadOnlyTool,
			); decision != nil {
				return decision
			}
		}
	}

	// Check global rules
	if globalRules, err := cm.getGlobalRules(ctx); err == nil {
		if decision := cm.findMatchingRule(
			globalRules, requestTarget, request.Operation, isReadOnlyTool,
		); decision != nil {
			return decision
		}
	}

	return nil
}

// findMatchingRule finds a matching rule using target pattern matching
func (cm *consentManager) findMatchingRule(
	rules []ConsentRule,
	requestTarget Target,
	operation OperationType,
	isReadOnlyTool bool,
) *ConsentDecision {
	// Process rules in precedence order: deny rules first, then allow rules
	// Precedence: Explicit deny > Global scope > Server scope > Tool scope

	// First pass: Check for deny rules
	for _, rule := range rules {
		if rule.Permission == PermissionDeny && rule.Operation == operation &&
			cm.targetMatches(rule.Target, requestTarget) && cm.actionMatches(rule.Action, isReadOnlyTool) {
			return &ConsentDecision{Allowed: false, Reason: "explicitly denied"}
		}
	}

	// Second pass: Check for allow/prompt rules in precedence order
	// Global patterns first (* pattern)
	for _, rule := range rules {
		if rule.Permission != PermissionDeny && rule.Operation == operation &&
			(rule.Target == "*" || rule.Target == "*/*") &&
			cm.actionMatches(rule.Action, isReadOnlyTool) {
			return cm.evaluateRule(rule)
		}
	}

	// Server patterns next (server/* pattern)
	serverPattern := NewServerTarget(string(requestTarget[:strings.Index(string(requestTarget), "/")]))
	for _, rule := range rules {
		if rule.Permission != PermissionDeny && rule.Operation == operation &&
			rule.Target == serverPattern &&
			cm.actionMatches(rule.Action, isReadOnlyTool) {
			return cm.evaluateRule(rule)
		}
	}

	// Specific tool patterns last (exact match)
	for _, rule := range rules {
		if rule.Permission != PermissionDeny && rule.Operation == operation &&
			rule.Target == requestTarget &&
			cm.actionMatches(rule.Action, isReadOnlyTool) {
			return cm.evaluateRule(rule)
		}
	}

	return nil
}

// targetMatches checks if a rule target matches the request target
func (cm *consentManager) targetMatches(ruleTarget, requestTarget Target) bool {
	ruleStr := string(ruleTarget)
	requestStr := string(requestTarget)

	// Global wildcards
	if ruleStr == "*" || ruleStr == "*/*" {
		return true
	}

	// Server wildcard
	if strings.HasSuffix(ruleStr, "/*") {
		serverName := ruleStr[:len(ruleStr)-2]
		return strings.HasPrefix(requestStr, serverName+"/")
	}

	// Exact match
	return ruleStr == requestStr
}

// actionMatches checks if a rule action matches the request (considering readonly restrictions)
func (cm *consentManager) actionMatches(ruleAction ActionType, isReadOnlyTool bool) bool {
	switch ruleAction {
	case ActionReadOnly:
		// Rule only applies to read-only tools
		return isReadOnlyTool
	case ActionAny:
		// Rule applies to all tools
		return true
	default:
		// Unknown action, default to not matching
		return false
	}
}
