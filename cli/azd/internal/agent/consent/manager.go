// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package consent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/common"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
)

const (
	ConfigKeyMCPConsent = "mcp.consent"
)

// Global state for tracking current executing tool
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
	console           input.Console
	userConfigManager config.UserConfigManager
	sessionRules      []ConsentRule // Rules for this session
	sessionMutex      sync.RWMutex
}

// NewConsentManager creates a new consent manager
func NewConsentManager(
	console input.Console,
	userConfigManager config.UserConfigManager,
) ConsentManager {
	return &consentManager{
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
func (cm *consentManager) GrantConsent(ctx context.Context, rule ConsentRule, scope ConsentScope) error {
	rule.GrantedAt = time.Now()

	// Set default RuleScope if not specified (backward compatibility)
	if rule.RuleScope == "" {
		rule.RuleScope = RuleScopeAll
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

// ListConsents lists consent rules for a given scope
func (cm *consentManager) ListConsents(ctx context.Context, scope ConsentScope) ([]ConsentRule, error) {
	switch scope {
	case ScopeSession:
		return cm.getSessionRules(), nil
	case ScopeProject:
		return cm.getProjectRules(ctx, "")
	case ScopeGlobal:
		return cm.getGlobalRules(ctx)
	default:
		return nil, fmt.Errorf("unknown consent scope: %s", scope)
	}
}

// ClearConsents clears all consent rules for a given scope
func (cm *consentManager) ClearConsents(ctx context.Context, scope ConsentScope) error {
	switch scope {
	case ScopeSession:
		return cm.clearSessionRules()
	case ScopeProject:
		return fmt.Errorf("project-level consent clearing not yet implemented")
	case ScopeGlobal:
		return cm.clearGlobalRules(ctx)
	default:
		return fmt.Errorf("unknown consent scope: %s", scope)
	}
}

// ClearConsentByToolID clears consent for a specific tool
func (cm *consentManager) ClearConsentByToolID(ctx context.Context, toolID string, scope ConsentScope) error {
	switch scope {
	case ScopeSession:
		return cm.removeSessionRule(toolID)
	case ScopeProject:
		return fmt.Errorf("project-level consent removal not yet implemented")
	case ScopeGlobal:
		return cm.removeGlobalRule(ctx, toolID)
	default:
		return fmt.Errorf("unknown consent scope: %s", scope)
	}
}

// ListConsentsByType lists consent rules filtered by type for a given scope
func (cm *consentManager) ListConsentsByType(
	ctx context.Context,
	scope ConsentScope,
	ruleType ConsentRuleType,
) ([]ConsentRule, error) {
	allRules, err := cm.ListConsents(ctx, scope)
	if err != nil {
		return nil, err
	}

	filteredRules := make([]ConsentRule, 0)
	for _, rule := range allRules {
		if rule.Type == ruleType {
			filteredRules = append(filteredRules, rule)
		}
	}

	return filteredRules, nil
}

// ClearConsentsByType clears all consent rules of a specific type for a given scope
func (cm *consentManager) ClearConsentsByType(ctx context.Context, scope ConsentScope, ruleType ConsentRuleType) error {
	rules, err := cm.ListConsentsByType(ctx, scope, ruleType)
	if err != nil {
		return fmt.Errorf("failed to list consent rules: %w", err)
	}

	for _, rule := range rules {
		if err := cm.ClearConsentByToolID(ctx, rule.ToolID, scope); err != nil {
			return fmt.Errorf("failed to clear consent for tool %s: %w", rule.ToolID, err)
		}
	}

	return nil
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
	case ConsentDeny:
		return &ConsentDecision{Allowed: false, Reason: "explicitly denied"}
	case ConsentPrompt:
		return &ConsentDecision{Allowed: false, RequiresPrompt: true, Reason: "requires prompt"}
	case ConsentOnce:
		// For one-time consent, we allow it but mark it for removal
		// The caller should handle removing this rule after use
		return &ConsentDecision{Allowed: true, Reason: "one-time consent"}
	case ConsentSession, ConsentProject, ConsentAlways, ConsentServerAlways:
		return &ConsentDecision{Allowed: true, Reason: string(rule.Permission)}
	default:
		return &ConsentDecision{Allowed: false, RequiresPrompt: true, Reason: "unknown permission level"}
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
	// This would need to be implemented with the environment manager
	// For now, return an error to indicate it's not implemented
	return fmt.Errorf("project-level consent not yet implemented")
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
		if rule.ToolID == newRule.ToolID {
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
func (cm *consentManager) getProjectRules(ctx context.Context, projectPath string) ([]ConsentRule, error) {
	// TODO: Implement project-level consent rules
	return []ConsentRule{}, nil
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

// removeSessionRule removes a specific rule from session rules
func (cm *consentManager) removeSessionRule(toolID string) error {
	cm.sessionMutex.Lock()
	defer cm.sessionMutex.Unlock()

	// Filter out the rule to remove
	filtered := make([]ConsentRule, 0, len(cm.sessionRules))
	for _, rule := range cm.sessionRules {
		if rule.ToolID != toolID {
			filtered = append(filtered, rule)
		}
	}

	cm.sessionRules = filtered
	return nil
}

// removeSessionRuleByIndex removes a rule by its index (for cleanup after one-time use)
func (cm *consentManager) removeSessionRuleByIndex(index int) error {
	cm.sessionMutex.Lock()
	defer cm.sessionMutex.Unlock()

	if index < 0 || index >= len(cm.sessionRules) {
		return nil // Index out of bounds, nothing to remove
	}

	// Remove the rule at the specified index
	cm.sessionRules = append(cm.sessionRules[:index], cm.sessionRules[index+1:]...)
	return nil
}

// removeGlobalRule removes a specific rule from global configuration
func (cm *consentManager) removeGlobalRule(ctx context.Context, toolID string) error {
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
		if rule.ToolID != toolID {
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

	// Check session rules first
	cm.sessionMutex.RLock()
	sessionRules := cm.sessionRules
	cm.sessionMutex.RUnlock()

	if decision := cm.findMatchingUnifiedRule(sessionRules, request, isReadOnlyTool); decision != nil {
		return decision
	}

	// Check project rules
	if request.ProjectPath != "" {
		if projectRules, err := cm.getProjectRules(ctx, request.ProjectPath); err == nil {
			if decision := cm.findMatchingUnifiedRule(projectRules, request, isReadOnlyTool); decision != nil {
				return decision
			}
		}
	}

	// Check global rules
	if globalRules, err := cm.getGlobalRules(ctx); err == nil {
		if decision := cm.findMatchingUnifiedRule(globalRules, request, isReadOnlyTool); decision != nil {
			return decision
		}
	}

	return nil
}

// findMatchingUnifiedRule finds a matching rule using unified pattern and scope matching
func (cm *consentManager) findMatchingUnifiedRule(
	rules []ConsentRule,
	request ConsentRequest,
	isReadOnlyTool bool,
) *ConsentDecision {
	// Process rules in order: deny rules first, then allow rules
	// This implements: Explicit deny > Global scope > Server scope > Tool scope precedence

	// First pass: Check for deny rules
	for _, rule := range rules {
		if rule.Permission == ConsentDeny && rule.Type == request.Type && cm.ruleMatches(rule, request, isReadOnlyTool) {
			return &ConsentDecision{Allowed: false, Reason: "explicitly denied"}
		}
	}

	// Second pass: Check for allow rules in precedence order
	// Global patterns first (* pattern)
	for i, rule := range rules {
		if rule.Permission != ConsentDeny && rule.Type == request.Type && rule.ToolID == "*" &&
			cm.ruleMatches(rule, request, isReadOnlyTool) {
			return cm.evaluateAllowRule(rule, request, i)
		}
	}

	// Server patterns next (server/* pattern)
	serverPattern := fmt.Sprintf("%s/*", request.ServerName)
	for i, rule := range rules {
		if rule.Permission != ConsentDeny && rule.Type == request.Type && rule.ToolID == serverPattern &&
			cm.ruleMatches(rule, request, isReadOnlyTool) {
			return cm.evaluateAllowRule(rule, request, i)
		}
	}

	// Specific tool patterns last (exact match)
	for i, rule := range rules {
		if rule.Permission != ConsentDeny && rule.Type == request.Type && rule.ToolID == request.ToolID &&
			cm.ruleMatches(rule, request, isReadOnlyTool) {
			return cm.evaluateAllowRule(rule, request, i)
		}
	}

	return nil
}

// ruleMatches checks if a rule matches the request considering scope restrictions
func (cm *consentManager) ruleMatches(rule ConsentRule, request ConsentRequest, isReadOnlyTool bool) bool {
	// Default to "all" scope for backward compatibility
	ruleScope := rule.RuleScope
	if ruleScope == "" {
		ruleScope = RuleScopeAll
	}

	// Check scope restrictions
	switch ruleScope {
	case RuleScopeReadOnly:
		// Rule only applies to read-only tools
		return isReadOnlyTool
	case RuleScopeAll:
		// Rule applies to all tools
		return true
	default:
		// Unknown scope, default to not matching
		return false
	}
}

// evaluateAllowRule evaluates an allow rule and handles one-time cleanup
func (cm *consentManager) evaluateAllowRule(rule ConsentRule, request ConsentRequest, ruleIndex int) *ConsentDecision {
	decision := cm.evaluateRule(rule)

	// If this is a one-time consent rule, remove it after evaluation
	if decision.Allowed && rule.Permission == ConsentOnce {
		go func(index int) {
			cm.removeSessionRuleByIndex(index)
		}(ruleIndex)
	}

	return decision
}
