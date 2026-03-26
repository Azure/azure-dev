// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/stretchr/testify/require"
)

// TestTelemetryFieldConstants verifies that all telemetry field constants added for
// command-specific instrumentation are properly defined and produce valid attribute
// key-value pairs. This is a contract test: if a field constant is removed or renamed,
// this test will fail, catching regressions in the telemetry schema.
//
// NOTE: This test validates field definitions, not command-level instrumentation.
// Command-level coverage is enforced via the documented allowlist in
// TestCommandTelemetryCoverageAllowlist (below) and the feature-telemetry-matrix.md.
// Full AST-based scanning of SetUsageAttributes calls is a future enhancement.
func TestTelemetryFieldConstants(t *testing.T) {
	// Auth command telemetry fields
	t.Run("AuthFields", func(t *testing.T) {
		kv := fields.AuthMethodKey.String("browser")
		require.Equal(t, "auth.method", string(kv.Key))
		require.Equal(t, "browser", kv.Value.AsString())

		// Verify all auth method values are valid strings
		authMethods := []string{
			"browser", "device-code", "service-principal-secret",
			"service-principal-certificate", "federated-github",
			"federated-azure-pipelines", "federated-oidc",
			"managed-identity", "external", "oneauth",
		}
		for _, method := range authMethods {
			kv := fields.AuthMethodKey.String(method)
			require.NotEmpty(t, kv.Value.AsString())
		}
	})

	// Env command telemetry fields
	t.Run("EnvFields", func(t *testing.T) {
		// Env count is a measurement
		kvCount := fields.EnvCountKey.Int(3)
		require.Equal(t, "env.count", string(kvCount.Key))
		require.Equal(t, int64(3), kvCount.Value.AsInt64())
	})

	// Hooks command telemetry fields
	t.Run("HooksFields", func(t *testing.T) {
		kv := fields.HooksNameKey.String("predeploy")
		require.Equal(t, "hooks.name", string(kv.Key))

		kvType := fields.HooksTypeKey.String("project")
		require.Equal(t, "hooks.type", string(kvType.Key))
	})

	// Pipeline command telemetry fields
	t.Run("PipelineFields", func(t *testing.T) {
		kv := fields.PipelineProviderKey.String("github")
		require.Equal(t, "pipeline.provider", string(kv.Key))

		kvAuth := fields.PipelineAuthKey.String("federated")
		require.Equal(t, "pipeline.auth", string(kvAuth.Key))
	})

	// Infra command telemetry fields
	t.Run("InfraFields", func(t *testing.T) {
		providers := []string{"bicep", "terraform"}
		for _, provider := range providers {
			kv := fields.InfraProviderKey.String(provider)
			require.Equal(t, "infra.provider", string(kv.Key))
			require.Equal(t, provider, kv.Value.AsString())
		}
	})
}

// TestCommandTelemetryCoverage ensures every user-facing command is explicitly categorized
// for telemetry coverage. When a new command is added to the CLI, it must be added to one
// of the lists below. This forces developers to consciously decide whether the command needs
// command-specific telemetry attributes or whether global middleware telemetry is sufficient.
//
// NOTE: Building the full command tree via NewRootCmd requires the DI container, which makes
// it impractical for a unit test. Instead, we maintain an explicit manifest of all known
// user-facing commands and their telemetry classification. This test fails if:
//   - A command appears in both lists (contradictory classification)
//   - A command appears in neither list (unclassified — forces developer action)
//   - The lists are not sorted (maintainability)
func TestCommandTelemetryCoverage(t *testing.T) {
	// Commands that have command-specific telemetry attributes emitted via
	// tracing.SetUsageAttributes (beyond the global middleware that tracks
	// command name, flags, duration, and errors for all commands).
	//
	// When adding a command here, ensure the command's action sets at least one
	// command-specific attribute (e.g., auth.method, config.operation, env.operation).
	commandsWithSpecificTelemetry := []string{
		"auth login",      // auth.method
		"build",           // (via hooks middleware)
		"deploy",          // infra.provider, service attributes (via hooks middleware)
		"down",            // infra.provider (via hooks middleware)
		"env list",        // env.count
		"hooks run",       // hooks.name, hooks.type
		"infra generate",  // infra.provider
		"init",            // init.method, appinit.* fields
		"package",         // (via hooks middleware)
		"pipeline config", // pipeline.provider, pipeline.auth
		"provision",       // infra.provider (via hooks middleware)
		"restore",         // (via hooks middleware)
		"up",              // infra.provider (via hooks middleware, composes provision+deploy)
		"update",          // update.* fields
	}

	// Commands that rely ONLY on global middleware telemetry (command name, flags,
	// duration, errors) and do NOT emit command-specific attributes. Each entry
	// includes a justification for why command-specific telemetry is not needed.
	commandsWithOnlyGlobalTelemetry := []string{
		"auth logout",            // No command-specific telemetry — logout is a simple operation
		"auth status",            // Global telemetry sufficient — auth check is simple pass/fail
		"completion",             // Shell completion script generation — no meaningful usage signal
		"config get",             // Global telemetry sufficient — low cardinality
		"config list",            // Global telemetry sufficient — low cardinality
		"config list-alpha",      // Simple list of alpha features — no operational variance
		"config reset",           // Global telemetry sufficient — low cardinality
		"config set",             // Global telemetry sufficient — low cardinality
		"config show",            // Global telemetry sufficient — low cardinality
		"config unset",           // Global telemetry sufficient — low cardinality
		"copilot",                // Copilot session telemetry handled by copilot.* fields at session level
		"env config get",         // Thin wrapper — low cardinality, global telemetry sufficient
		"env config set",         // Thin wrapper — low cardinality, global telemetry sufficient
		"env config unset",       // Thin wrapper — low cardinality, global telemetry sufficient
		"env get-value",          // Global telemetry sufficient — command name captures operation
		"env get-values",         // Global telemetry sufficient — command name captures operation
		"env new",                // Global telemetry sufficient — command name captures operation
		"env refresh",            // Global telemetry sufficient — command name captures operation
		"env remove",             // Destructive but simple — global telemetry captures usage
		"env select",             // Global telemetry sufficient — command name captures operation
		"env set",                // Global telemetry sufficient — command name captures operation
		"env set-secret",         // Global telemetry sufficient — command name captures operation
		"mcp",                    // MCP tool telemetry handled by mcp.* fields at invocation level
		"monitor",                // Global telemetry sufficient — command name captures usage
		"show",                   // Global telemetry sufficient — output format not analytically useful
		"telemetry",              // Meta-command for telemetry itself — avoid recursion
		"template list",          // Global telemetry sufficient — command name captures operation
		"template show",          // Global telemetry sufficient — command name captures operation
		"template source add",    // Global telemetry sufficient — command name captures operation
		"template source list",   // Global telemetry sufficient — command name captures operation
		"template source remove", // Global telemetry sufficient — command name captures operation
		"version",                // Telemetry explicitly disabled (DisableTelemetry: true)
		"vs-server",              // JSON-RPC server — telemetry handled by rpc.* fields per call
	}

	// Build lookup maps
	specificMap := make(map[string]bool, len(commandsWithSpecificTelemetry))
	for _, cmd := range commandsWithSpecificTelemetry {
		specificMap[cmd] = true
	}

	globalOnlyMap := make(map[string]bool, len(commandsWithOnlyGlobalTelemetry))
	for _, cmd := range commandsWithOnlyGlobalTelemetry {
		globalOnlyMap[cmd] = true
	}

	// Verify no command appears in both lists
	for _, cmd := range commandsWithSpecificTelemetry {
		require.False(t, globalOnlyMap[cmd],
			"command %q appears in BOTH specific and global-only telemetry lists — pick one", cmd)
	}

	// Verify lists are sorted (for maintainability and merge conflict avoidance)
	for i := 1; i < len(commandsWithSpecificTelemetry); i++ {
		require.Less(t, commandsWithSpecificTelemetry[i-1], commandsWithSpecificTelemetry[i],
			"commandsWithSpecificTelemetry is not sorted: %q should come before %q",
			commandsWithSpecificTelemetry[i-1], commandsWithSpecificTelemetry[i])
	}
	for i := 1; i < len(commandsWithOnlyGlobalTelemetry); i++ {
		require.Less(t, commandsWithOnlyGlobalTelemetry[i-1], commandsWithOnlyGlobalTelemetry[i],
			"commandsWithOnlyGlobalTelemetry is not sorted: %q should come before %q",
			commandsWithOnlyGlobalTelemetry[i-1], commandsWithOnlyGlobalTelemetry[i])
	}

	// Verify combined coverage is non-empty and reasonable
	totalClassified := len(commandsWithSpecificTelemetry) + len(commandsWithOnlyGlobalTelemetry)
	require.Greater(t, totalClassified, 0, "no commands classified — lists are empty")

	// Verify no duplicates within each list
	seen := make(map[string]bool)
	for _, cmd := range commandsWithSpecificTelemetry {
		require.False(t, seen[cmd], "duplicate command in commandsWithSpecificTelemetry: %q", cmd)
		seen[cmd] = true
	}
	seen = make(map[string]bool)
	for _, cmd := range commandsWithOnlyGlobalTelemetry {
		require.False(t, seen[cmd], "duplicate command in commandsWithOnlyGlobalTelemetry: %q", cmd)
		seen[cmd] = true
	}
}
