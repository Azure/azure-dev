// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/azure"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

// connectionAction is the resolution outcome for a single declared connection.
type connectionAction int

const (
	// connActionUseExisting means a connection with this name already exists in
	// the project and is used as-is (ladder rung 1).
	connActionUseExisting connectionAction = iota
	// connActionCreate means the connection is created against a known target
	// (ladder rung 2, with the target possibly auto-filled at rung 3).
	connActionCreate
	// connActionProvision means the backing resource must be provisioned first
	// (ladder rung 4, opt-in via Provision).
	connActionProvision
	// connActionFailFast means nothing could be resolved and the user must act
	// (ladder rung 4, no opt-in).
	connActionFailFast
)

// connectionRoleAssignments below map a tool type to the Azure role its
// connection's identity needs on the backing resource. Only tools that require
// a data-plane role are listed; others authenticate through the connection
// itself and need no role assignment.
//
// Role IDs are Azure built-in role definition GUIDs.
var toolRequiredRoles = map[string]struct {
	RoleID   string
	RoleName string
}{
	// Search Index Data Reader.
	"azure_ai_search": {"1407120a-92aa-4202-b7e9-c0e197c71c8f", "Search Index Data Reader"},
}

// requiredRoleForTool returns the role a tool's connection identity needs, if
// any. ok is false when the tool type needs no explicit role assignment.
func requiredRoleForTool(toolType string) (roleID, roleName string, ok bool) {
	r, found := toolRequiredRoles[toolType]
	if !found {
		return "", "", false
	}
	return r.RoleID, r.RoleName, true
}

// targetFromEnv attempts to auto-fill a connection target from azd provisioning
// outputs (ladder rung 3). It scans the connection-oriented env exports for an
// entry keyed by the connection name. Returns "" when nothing matches.
func targetFromEnv(name string, env map[string]string) string {
	if env == nil || strings.TrimSpace(name) == "" {
		return ""
	}
	// The provisioning layer exports connection targets as NAME=target pairs in
	// AI_PROJECT_CONNECTIONS (semicolon-separated) and dependent resources in
	// AI_PROJECT_DEPENDENT_RESOURCES. Both are scanned.
	for _, key := range []string{"AI_PROJECT_CONNECTIONS", "AI_PROJECT_DEPENDENT_RESOURCES"} {
		raw, ok := env[key]
		if !ok || strings.TrimSpace(raw) == "" {
			continue
		}
		for _, pair := range strings.Split(raw, ";") {
			pair = strings.TrimSpace(pair)
			eq := strings.IndexByte(pair, '=')
			if eq <= 0 {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(pair[:eq]), name) {
				return strings.TrimSpace(pair[eq+1:])
			}
		}
	}
	return ""
}

// resolveConnectionAction decides how to satisfy one declared connection given
// the set of existing connection names and the azd environment. It is pure and
// table-testable; the connection node performs the side effects.
//
// The returned PromptConnection carries any auto-filled target so the caller can
// create the connection without re-deriving it.
func resolveConnectionAction(
	decl agent_yaml.PromptConnection,
	existing map[string]string,
	env map[string]string,
) (connectionAction, agent_yaml.PromptConnection, error) {
	if strings.TrimSpace(decl.Name) == "" {
		return connActionFailFast, decl, exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			"a declared connection is missing a name",
			"set 'name' on each entry under connections:",
		)
	}

	// Rung 1: an existing connection with this name is used as-is.
	if _, ok := existing[decl.Name]; ok {
		return connActionUseExisting, decl, nil
	}

	// Rung 3: auto-fill the target from provisioning outputs when absent.
	resolved := decl
	if strings.TrimSpace(resolved.Target) == "" {
		if t := targetFromEnv(decl.Name, env); t != "" {
			resolved.Target = t
		}
	}

	// Rung 2: with a known target, create the connection (Entra default).
	if strings.TrimSpace(resolved.Target) != "" {
		return connActionCreate, resolved, nil
	}

	// Rung 4: no target — provision if opted in, else fail fast.
	if decl.Provision {
		return connActionProvision, resolved, nil
	}
	return connActionFailFast, resolved, exterrors.Validation(
		exterrors.CodeInvalidAgentManifest,
		fmt.Sprintf(
			"connection %q has no existing connection and no resolvable target", decl.Name,
		),
		"set connections["+decl.Name+"].target, or set provision: true to create the backing resource",
	)
}

// connectionResolver performs the side effects of the ladder: listing existing
// connections, creating missing ones, and assigning roles. The seam keeps the
// connection node unit-testable without a live endpoint.
type connectionResolver interface {
	// Existing returns the names of connections already present in the project,
	// mapped to their ids.
	Existing(ctx context.Context) (map[string]string, error)
	// Create creates a connection from the (possibly target-filled) declaration
	// and returns its id.
	Create(ctx context.Context, decl agent_yaml.PromptConnection) (id string, err error)
	// AssignRole assigns roleID to the agent/project identity on the connection's
	// backing resource. Implementations may no-op with a warning when the
	// principal or scope is not yet known.
	AssignRole(ctx context.Context, decl agent_yaml.PromptConnection, roleID, roleName string) error
}

// connectionsNode builds the connection + rbac graph node. It resolves every
// declared connection through the ladder, creates the missing ones, and assigns
// each referenced tool's required role. Returns nil when nothing is declared.
func connectionsNode(
	g *promptGraph,
	newResolver func() (connectionResolver, error),
) *promptNode {
	decls := g.managed.Connections
	if len(decls) == 0 {
		return nil
	}
	return &promptNode{
		Kind: nodeConnection,
		ID:   "connections",
		Validate: func() error {
			for _, d := range decls {
				if strings.TrimSpace(d.Name) == "" {
					return exterrors.Validation(
						exterrors.CodeInvalidAgentManifest,
						"a declared connection is missing a name",
						"set 'name' on each entry under connections:",
					)
				}
			}
			return nil
		},
		Resolve: func(ctx context.Context) error {
			resolver, err := newResolver()
			if err != nil {
				return err
			}
			existing, err := resolver.Existing(ctx)
			if err != nil {
				return err
			}

			for _, decl := range decls {
				action, resolved, decideErr := resolveConnectionAction(decl, existing, g.env)
				if decideErr != nil {
					return decideErr
				}
				switch action {
				case connActionUseExisting:
					// Nothing to create.
				case connActionCreate, connActionProvision:
					id, createErr := resolver.Create(ctx, resolved)
					if createErr != nil {
						return fmt.Errorf("creating connection %q: %w", resolved.Name, createErr)
					}
					existing[resolved.Name] = id
				case connActionFailFast:
					// resolveConnectionAction already returned an error for this
					// case; defensively guard here.
					return exterrors.Validation(
						exterrors.CodeInvalidAgentManifest,
						fmt.Sprintf("connection %q could not be resolved", resolved.Name),
						"declare a target or set provision: true",
					)
				}
			}

			// Assign roles for tools that reference a connection needing one.
			return assignConnectionRoles(ctx, resolver, g.managed)
		},
	}
}

// assignConnectionRoles walks the agent's tools, and for each tool that both
// requires a role and references a declared connection, assigns that role.
func assignConnectionRoles(
	ctx context.Context,
	resolver connectionResolver,
	managed *agent_yaml.PromptAgent,
) error {
	byName := map[string]agent_yaml.PromptConnection{}
	for _, c := range managed.Connections {
		byName[c.Name] = c
	}

	for _, raw := range managed.Tools {
		tool, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		toolType := fmt.Sprintf("%v", tool["type"])
		roleID, roleName, need := requiredRoleForTool(toolType)
		if !need {
			continue
		}
		connName := toolConnectionName(tool)
		if connName == "" {
			continue
		}
		decl, ok := byName[connName]
		if !ok {
			continue
		}
		if err := resolver.AssignRole(ctx, decl, roleID, roleName); err != nil {
			return fmt.Errorf("assigning %s for connection %q: %w", roleName, connName, err)
		}
	}
	return nil
}

// toolConnectionName extracts the connection name a tool references, tolerating
// both a top-level `connection` string and a nested `project_connection_id`.
func toolConnectionName(tool map[string]any) string {
	if v, ok := tool["connection"]; ok {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			return s
		}
	}
	if v, ok := tool["project_connection_id"]; ok {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// foundryConnectionResolver is the live connectionResolver backed by the
// Foundry project connections data-plane.
type foundryConnectionResolver struct {
	client *azure.FoundryProjectsClient
}

// Existing lists the project's connections as a name -> id map.
func (r *foundryConnectionResolver) Existing(ctx context.Context) (map[string]string, error) {
	conns, err := r.client.GetAllConnections(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing project connections: %w", err)
	}
	out := make(map[string]string, len(conns))
	for _, c := range conns {
		out[c.Name] = c.ID
	}
	return out, nil
}

// Create creates a connection from the declaration, defaulting to Entra auth.
func (r *foundryConnectionResolver) Create(
	ctx context.Context, decl agent_yaml.PromptConnection,
) (string, error) {
	created, err := r.client.CreateConnection(ctx, decl.Name, &azure.CreateConnectionRequest{
		Category:    decl.Category,
		Target:      decl.Target,
		AuthType:    decl.AuthType, // empty defaults to AAD in the client
		Credentials: decl.Credentials,
		Metadata:    decl.Metadata,
	})
	if err != nil {
		return "", err
	}
	return created.ID, nil
}

// AssignRole is best-effort at deploy time. The agent's instance identity is
// only known after the agent version is created, and a data-plane connection
// does not expose its backing resource's ARM scope, so a fully automatic
// assignment is not possible here. Rather than fail the deploy, surface the
// exact manual command so the operator can grant access, consistent with the
// hosted-agent RBAC UX.
func (r *foundryConnectionResolver) AssignRole(
	_ context.Context, decl agent_yaml.PromptConnection, _ string, roleName string,
) error {
	fmt.Printf("%s\n", output.WithWarningFormat(
		"Connection %q needs the %q role on its backing resource (%s).\n"+
			"    Automatic assignment is not available at deploy time for prompt agents.\n"+
			"    Grant it to the agent identity once the agent is created, e.g.:\n"+
			"      az role assignment create --assignee <agent-principal-id> "+
			"--role %q --scope <backing-resource-id>",
		decl.Name, roleName, decl.Target, roleName,
	))
	return nil
}

// newFoundryConnectionResolver builds the live resolver from prompt settings by
// parsing the account/project from the project endpoint.
func newFoundryConnectionResolver(settings *PromptAgentSettings) (connectionResolver, error) {
	if settings == nil || strings.TrimSpace(settings.ProjectEndpoint) == "" {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			"a Foundry project endpoint is required to resolve connections",
			"run `azd up` to provision a Foundry project, or remove the connections: block",
		)
	}
	account, project, err := parseAccountProject(settings.ProjectEndpoint)
	if err != nil {
		return nil, err
	}
	client, err := azure.NewFoundryProjectsClient(account, project, promptCredential())
	if err != nil {
		return nil, err
	}
	return &foundryConnectionResolver{client: client}, nil
}

// parseAccountProject extracts the account and project names from a Foundry
// project endpoint of the form
// https://<account>.services.ai.azure.com/api/projects/<project>.
func parseAccountProject(endpoint string) (account, project string, err error) {
	u, parseErr := url.Parse(strings.TrimSpace(endpoint))
	if parseErr != nil || u.Host == "" {
		return "", "", exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("could not parse project endpoint %q", endpoint),
			"ensure the project endpoint looks like https://<account>.services.ai.azure.com/api/projects/<project>",
		)
	}
	account = strings.SplitN(u.Host, ".", 2)[0]
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == "projects" {
			project = parts[i+1]
			break
		}
	}
	if account == "" || project == "" {
		return "", "", exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("project endpoint %q is missing an account or project segment", endpoint),
			"ensure the project endpoint includes /api/projects/<project>",
		)
	}
	return account, project, nil
}
