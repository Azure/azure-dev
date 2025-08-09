// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package consent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/tmc/langchaingo/tools"
)

const (
	ConfigKeyMCPConsent = "mcp.consent"
)

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
	// Check for explicit deny rules first
	if decision := cm.checkExplicitRules(ctx, request); decision != nil && !decision.Allowed {
		return decision, nil
	}

	// Check if server is trusted
	if cm.isServerTrusted(ctx, request.ServerName) {
		return &ConsentDecision{Allowed: true, Reason: "trusted server"}, nil
	}

	// Check if read-only tools are globally allowed
	if request.Annotations != nil && request.Annotations.ReadOnlyHint != nil && *request.Annotations.ReadOnlyHint {
		if cm.isReadOnlyToolsAllowed(ctx) {
			return &ConsentDecision{Allowed: true, Reason: "read-only tool allowed"}, nil
		}
	}

	// Check existing consent rules
	if decision := cm.checkExplicitRules(ctx, request); decision != nil && decision.Allowed {
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

// WrapTool wraps a single langchaingo tool with consent protection
func (cm *consentManager) WrapTool(tool tools.Tool) tools.Tool {
	return newConsentWrapperTool(tool, cm.console, cm)
}

// WrapTools wraps multiple langchaingo tools with consent protection
func (cm *consentManager) WrapTools(langchainTools []tools.Tool) []tools.Tool {
	wrappedTools := make([]tools.Tool, len(langchainTools))

	for i, tool := range langchainTools {
		wrappedTools[i] = cm.WrapTool(tool)
	}

	return wrappedTools
}

// checkExplicitRules checks for explicit consent rules across all scopes
func (cm *consentManager) checkExplicitRules(ctx context.Context, request ConsentRequest) *ConsentDecision {
	// Check session rules first
	cm.sessionMutex.RLock()
	sessionRules := cm.sessionRules
	cm.sessionMutex.RUnlock()

	if len(sessionRules) > 0 {
		if decision := cm.findMatchingRule(sessionRules, request); decision != nil {
			return decision
		}
	}

	// Check project rules
	if request.ProjectPath != "" {
		if projectRules, err := cm.getProjectRules(ctx, request.ProjectPath); err == nil {
			if decision := cm.findMatchingRule(projectRules, request); decision != nil {
				return decision
			}
		}
	}

	// Check global rules
	if globalRules, err := cm.getGlobalRules(ctx); err == nil {
		if decision := cm.findMatchingRule(globalRules, request); decision != nil {
			return decision
		}
	}

	return nil
}

// findMatchingRule finds a matching rule for the request
func (cm *consentManager) findMatchingRule(rules []ConsentRule, request ConsentRequest) *ConsentDecision {
	serverName := request.ServerName

	for i, rule := range rules {
		// Check for exact tool match
		if rule.ToolID == request.ToolID {
			decision := cm.evaluateRule(rule)

			// If this is a one-time consent rule, remove it after evaluation
			if decision.Allowed && rule.Permission == ConsentOnce {
				// Clean up the one-time rule from session rules
				go func(ruleIndex int) {
					cm.removeSessionRuleByIndex(ruleIndex)
				}(i)
			}

			return decision
		}

		// Check for server-wide consent
		if rule.Permission == ConsentServerAlways && rule.ToolID == fmt.Sprintf("%s/*", serverName) {
			return &ConsentDecision{Allowed: true, Reason: "server trusted"}
		}
	}

	return nil
}

// evaluateRule evaluates a consent rule and returns a decision
func (cm *consentManager) evaluateRule(rule ConsentRule) *ConsentDecision {
	switch rule.Permission {
	case ConsentDeny:
		return &ConsentDecision{Allowed: false, Reason: "explicitly denied"}
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

// isServerTrusted checks if a server is in the trusted servers list
func (cm *consentManager) isServerTrusted(ctx context.Context, serverName string) bool {
	config, err := cm.getGlobalConsentConfig(ctx)
	if err != nil {
		return false
	}

	for _, trustedServer := range config.TrustedServers {
		if trustedServer == serverName {
			return true
		}
	}

	return false
}

// isReadOnlyToolsAllowed checks if read-only tools are globally allowed
func (cm *consentManager) isReadOnlyToolsAllowed(ctx context.Context) bool {
	config, err := cm.getGlobalConsentConfig(ctx)
	if err != nil {
		return false
	}

	return config.AllowReadOnlyTools
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
		Rules:              []ConsentRule{},
		AllowReadOnlyTools: false,
		TrustedServers:     []string{},
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
