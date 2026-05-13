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
)

// connectionRefPattern matches ${{connections.<name>.credentials.<key>}} references
// in agent manifest environment variable values.
var connectionRefPattern = regexp.MustCompile(`\$\{\{connections\.([^.]+)\.credentials\.([^}]+)\}\}`)

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

	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		log.Printf("run: could not read manifest %s: %v", manifestPath, err)
		return nil, nil
	}

	manifest, err := agent_yaml.LoadAndValidateAgentManifest(manifestBytes)
	if err != nil {
		log.Printf("run: could not parse manifest %s: %v", manifestPath, err)
		return nil, nil
	}

	// Extract environment variables from the manifest
	containerAgent, ok := manifest.Template.(agent_yaml.ContainerAgent)
	if !ok || containerAgent.EnvironmentVariables == nil {
		return nil, nil
	}

	// Scan for connection references
	type connRef struct {
		envName  string // the env var name (e.g., TAVILY_API_KEY)
		connName string // connection name (e.g., my-test-conn)
		credKey  string // credential key (e.g., x-api-key)
	}

	var refs []connRef
	for _, ev := range *containerAgent.EnvironmentVariables {
		matches := connectionRefPattern.FindStringSubmatch(ev.Value)
		if matches != nil {
			refs = append(refs, connRef{
				envName:  ev.Name,
				connName: matches[1],
				credKey:  matches[2],
			})
		}
	}

	if len(refs) == 0 {
		return nil, nil
	}

	// Create data-plane credential and client
	cred, err := azidentity.NewAzureDeveloperCLICredential(
		&azidentity.AzureDeveloperCLICredentialOptions{},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create credential for connection resolution: %w", err)
	}

	dpClient := connections.NewDataClient(endpoint, cred)

	// Resolve each reference, caching per connection name
	connCache := map[string]*connections.Connection{}
	var result []string

	for _, ref := range refs {
		conn, cached := connCache[ref.connName]
		if !cached {
			conn, err = dpClient.GetConnectionWithCredentials(ctx, ref.connName)
			if err != nil {
				return nil, fmt.Errorf(
					"failed to resolve credential for %s (connection %q): %w",
					ref.envName, ref.connName, err,
				)
			}
			connCache[ref.connName] = conn
		}

		// Look up the credential key
		var credValue string
		if ref.credKey == "key" && conn.Credentials != nil && conn.Credentials.Key != "" {
			credValue = conn.Credentials.Key
		} else if conn.Credentials != nil {
			if v, ok := conn.Credentials.CustomKeys[ref.credKey]; ok {
				credValue = v
			}
		}

		if credValue == "" {
			return nil, fmt.Errorf(
				"credential key %q not found on connection %q (for env var %s)",
				ref.credKey, ref.connName, ref.envName,
			)
		}

		result = append(result, fmt.Sprintf("%s=%s", ref.envName, credValue))
		// Log the key name only — NEVER log the value
		log.Printf("run: resolved connection credential: %s (connection: %s, key: %s)",
			ref.envName, ref.connName, ref.credKey)
	}

	if len(result) > 0 {
		fmt.Fprintf(os.Stderr, "  %d connection credential(s) resolved\n", len(result))
	}

	return result, nil
}

// findManifestInDir looks for an agent manifest file in the given directory.
// Checks: agent.manifest.yaml, agent.yaml, agent.manifest.yml, agent.yml
func findManifestInDir(dir string) string {
	candidates := []string{
		"agent.manifest.yaml",
		"agent.yaml",
		"agent.manifest.yml",
		"agent.yml",
	}
	for _, name := range candidates {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			// Quick check: must contain "template" key to be a manifest
			data, err := os.ReadFile(path)
			if err == nil && strings.Contains(string(data), "template:") {
				return path
			}
		}
	}
	return ""
}
