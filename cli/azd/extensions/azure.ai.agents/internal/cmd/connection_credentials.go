// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"azureaiagent/internal/connections/pkg/connections"
	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"go.yaml.in/yaml/v3"
)

// connectionRefPattern matches ${{connections.<name>.credentials.<key>}} references
// in agent manifest environment variable values.
var connectionRefPattern = regexp.MustCompile(
	`\$\{\{connections\.([^.]+)\.credentials\.([^}]+)\}\}`,
)

// connRef represents a single connection credential reference found in an
// agent manifest's environment_variables section.
type connRef struct {
	EnvName  string // the env var name (e.g., TAVILY_API_KEY)
	ConnName string // connection name (e.g., my-test-conn)
	CredKey  string // credential key (e.g., x-api-key)
}

// extractConnectionRefs scans environment variable definitions for
// ${{connections.<name>.credentials.<key>}} patterns and returns the parsed refs.
func extractConnectionRefs(
	envVars []agent_yaml.EnvironmentVariable,
) []connRef {
	var refs []connRef
	for _, ev := range envVars {
		matches := connectionRefPattern.FindStringSubmatch(ev.Value)
		if matches != nil {
			refs = append(refs, connRef{
				EnvName:  ev.Name,
				ConnName: matches[1],
				CredKey:  matches[2],
			})
		}
	}
	return refs
}

// lookupCredentialValue finds the value of a credential key on a connection.
// Returns the value and true if found, or empty string and false if not.
func lookupCredentialValue(
	conn *connections.Connection,
	credKey string,
) (string, bool) {
	if conn == nil || conn.Credentials == nil {
		return "", false
	}
	if credKey == "key" && conn.Credentials.Key != "" {
		return conn.Credentials.Key, true
	}
	if v, ok := conn.Credentials.CustomKeys[credKey]; ok {
		return v, true
	}
	return "", false
}

// resolveConnectionCredentials reads the agent manifest from projectDir,
// scans environment_variables for ${{connections.<name>.credentials.<key>}} patterns,
// fetches credential values from the Foundry data plane, and returns them as
// KEY=VALUE strings ready to inject into the agent process environment.
//
// This is additive to existing env var handling in run.go:
//   - ${VAR} references are already resolved via loadAzdEnvironment
//   - ${{connections...}} references are resolved here via data-plane API
//   - Literal values pass through unchanged
//
// Returns nil (no error) if no manifest is found, no env vars are declared,
// or no connection references are present — the agent still starts normally.
func resolveConnectionCredentials(
	ctx context.Context,
	projectDir string,
	endpoint string,
) ([]string, error) {
	if endpoint == "" {
		return nil, nil
	}

	// Find and parse the agent manifest
	manifestPath := findManifestInDir(projectDir)
	if manifestPath == "" {
		return nil, nil
	}

	manifestBytes, err := os.ReadFile(manifestPath) //nolint:gosec // G304: path is from findManifestInDir which only checks known filenames in the project directory
	if err != nil {
		log.Printf("run: could not read manifest %s: %v", manifestPath, err)
		return nil, nil
	}

	// Try parsing as AgentManifest (agent.manifest.yaml — has "template:" wrapper)
	var envVars []agent_yaml.EnvironmentVariable

	manifest, err := agent_yaml.LoadAndValidateAgentManifest(manifestBytes)
	if err == nil {
		if containerAgent, ok := manifest.Template.(agent_yaml.ContainerAgent); ok &&
			containerAgent.EnvironmentVariables != nil {
			envVars = *containerAgent.EnvironmentVariables
		}
	}

	// Fall back to parsing as ContainerAgent directly (agent.yaml — no wrapper)
	if len(envVars) == 0 {
		var agentDef agent_yaml.ContainerAgent
		if yamlErr := yaml.Unmarshal(manifestBytes, &agentDef); yamlErr == nil &&
			agentDef.EnvironmentVariables != nil {
			envVars = *agentDef.EnvironmentVariables
		}
	}

	if len(envVars) == 0 {
		return nil, nil
	}

	// Scan for connection references
	refs := extractConnectionRefs(envVars)
	if len(refs) == 0 {
		return nil, nil
	}

	// Create data-plane credential and client
	cred, err := azidentity.NewAzureDeveloperCLICredential(
		&azidentity.AzureDeveloperCLICredentialOptions{},
	)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to create credential for connection resolution: %w", err,
		)
	}

	dpClient := connections.NewDataClient(endpoint, cred)

	// Resolve each reference, caching per connection name
	connCache := map[string]*connections.Connection{}
	var result []string

	for _, ref := range refs {
		conn, cached := connCache[ref.ConnName]
		if !cached {
			conn, err = dpClient.GetConnectionWithCredentials(ctx, ref.ConnName)
			if err != nil {
				return nil, fmt.Errorf(
					"failed to resolve credential for %s (connection %q): %w",
					ref.EnvName, ref.ConnName, err,
				)
			}
			connCache[ref.ConnName] = conn
		}

		credValue, found := lookupCredentialValue(conn, ref.CredKey)
		if !found {
			return nil, fmt.Errorf(
				"credential key %q not found on connection %q (for env var %s)",
				ref.CredKey, ref.ConnName, ref.EnvName,
			)
		}

		result = append(result, fmt.Sprintf("%s=%s", ref.EnvName, credValue))
		// Log the key name only — NEVER log the value
		log.Printf(
			"run: resolved connection credential: %s (connection: %s, key: %s)",
			ref.EnvName, ref.ConnName, ref.CredKey,
		)
	}

	if len(result) > 0 {
		fmt.Fprintf(os.Stderr, "  %d connection credential(s) resolved\n", len(result))
	}

	return result, nil
}

// findManifestInDir looks for an agent manifest or definition file in the given directory.
// Checks agent.yaml first (the definition the agent app uses), then agent.manifest.yaml.
// Returns the first file that exists and contains environment_variables with connection references.
func findManifestInDir(dir string) string {
	// Check agent.yaml first — this is the file the agent app code references
	candidates := []string{
		"agent.yaml",
		"agent.manifest.yaml",
		"agent.yml",
		"agent.manifest.yml",
	}
	for _, name := range candidates {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			data, err := os.ReadFile(path) //nolint:gosec // G304: path is constructed from known candidate filenames joined with the project directory
			if err == nil && strings.Contains(string(data), "${{connections.") {
				return path
			}
		}
	}
	return ""
}
