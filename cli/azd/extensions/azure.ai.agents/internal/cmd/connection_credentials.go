// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"log"
	"os"
	"regexp"

	"azureaiagent/internal/connections/pkg/connections"
	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
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

// resolveConnectionRefs takes pre-extracted connection references and resolves them
// via the Foundry data-plane API. Returns KEY=VALUE strings for each resolved credential.
func resolveConnectionRefs(ctx context.Context, refs []connRef, endpoint string) ([]string, error) {
	cred, err := azidentity.NewAzureDeveloperCLICredential(
		&azidentity.AzureDeveloperCLICredentialOptions{},
	)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to create credential for connection resolution: %w", err,
		)
	}

	dpClient := connections.NewDataClient(endpoint, cred)

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

