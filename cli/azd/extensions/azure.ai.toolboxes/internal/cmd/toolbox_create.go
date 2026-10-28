// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"azure.ai.toolboxes/internal/definition"
	"azure.ai.toolboxes/internal/exterrors"
	"azure.ai.toolboxes/internal/pkg/azure"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// toolboxCreateFlags holds the verb-specific flags for `toolbox create`.
type toolboxCreateFlags struct {
	fromFile string
}

// newToolboxCreateCommand returns the `azd ai toolbox create <name>` command.
func newToolboxCreateCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)
	flags := &toolboxCreateFlags{}

	cmd := &cobra.Command{
		Use:   "create <name> --from-file <path>",
		Short: "Create a toolbox and its initial version from a file.",
		Long: `Create a toolbox and its initial version.

The Foundry service requires the initial version to ship with at least one
tool entry, so 'create' takes its inputs from a JSON or YAML file via
--from-file.

For a verified matching active-environment project, create checks ARM roles
before its first Foundry request and may assign project-scoped Foundry User.
This can add the built-in role even when custom-role access already works.
Set AZD_AGENT_SKIP_ROLE_ASSIGNMENTS=true to opt out. Provisioning, toolbox
reuse, local authoring, reads, and other write commands do not assign roles.
Custom-subdomain endpoints with an unverified ARM binding use existing access.

` + fileShapeBlurb(true) + `

On success the toolbox's runtime MCP endpoint is written to the active azd
environment under the TOOLBOX_<NORMALIZED_NAME>_MCP_ENDPOINT variable (the same
key agents consume), where <NORMALIZED_NAME> is the toolbox name uppercased with
non-alphanumeric character runs replaced by underscores.`,
		Example: `  # Create a toolbox from a JSON definition
  azd ai toolbox create research --from-file ./tools.json

  # Use a YAML definition and return JSON
  azd ai toolbox create research --from-file ./tools.yaml --output json
`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			parent := readToolboxFlags(cmd, extCtx)
			if cmd.Flags().Changed("project-endpoint") && strings.TrimSpace(parent.projectEndpoint) == "" {
				return exterrors.Validation(exterrors.CodeInvalidParameter,
					"--project-endpoint must not be empty", "omit --project-endpoint to use the configured endpoint")
			}
			return runToolboxCreateReporting(
				cmd.Context(), args[0], *flags, parent,
				defaultConnectionResolver{runner: azdConnectionDescriptorRunner{stderr: cmd.ErrOrStderr()}},
				cmd.OutOrStdout(),
				func(message string) { fmt.Fprintln(cmd.ErrOrStderr(), message) },
			)
		},
	}

	cmd.Flags().StringVar(
		&flags.fromFile, "from-file", "",
		"Path to a JSON/YAML file describing the initial version (see --help for the file shape).",
	)
	if err := cmd.MarkFlagRequired("from-file"); err != nil {
		panic(err) // never fails; flag is registered above
	}
	registerToolboxOutputFlag(cmd)

	return cmd
}

func runToolboxCreate(
	ctx context.Context, name string, verb toolboxCreateFlags,
	parent toolboxFlags, resolver connectionResolver,
) error {
	return runToolboxCreateReporting(ctx, name, verb, parent, resolver, os.Stdout, nil)
}

func runToolboxCreateReporting(
	ctx context.Context, name string, verb toolboxCreateFlags,
	parent toolboxFlags, resolver connectionResolver, out io.Writer, report func(string),
) error {
	input, err := loadToolboxCreateInput(name, verb, parent)
	if err != nil {
		return err
	}

	resolved, err := resolveToolboxWriteEndpoint(ctx, parent.projectEndpoint)
	if err != nil {
		return err
	}
	logResolvedEndpoint("toolbox create", resolved)

	var access *toolboxAccess
	host, hostErr := azdext.NewAzdClient()
	if cancelErr := toolboxAccessCancellation(ctx, hostErr); cancelErr != nil {
		return cancelErr
	}
	if hostErr == nil {
		defer host.Close()
		access = newToolboxAccess(host)
	}
	scope, err := access.resolve(ctx, resolved.Endpoint)
	if err != nil {
		return err
	}
	tenantID := ""
	if scope != nil {
		tenantID = scope.tenantID
	}
	client, err := newToolboxClientForTenant(resolved.Endpoint, tenantID)
	if cancelErr := toolboxAccessCancellation(ctx, err); cancelErr != nil {
		return cancelErr
	}
	if err != nil {
		return exterrors.Auth(exterrors.CodeCredentialCreationFailed,
			"could not create the Foundry credential", "run 'azd auth login' and retry")
	}
	return createToolboxFromInput(ctx, client, resolver, resolved.Endpoint, name, input, parent, access, scope, out, report)
}

func loadToolboxCreateInput(
	name string, verb toolboxCreateFlags, parent toolboxFlags,
) (*definition.Definition, error) {
	if err := validateToolboxName(name); err != nil {
		return nil, err
	}
	if err := validateOutputFormat(parent.output); err != nil {
		return nil, err
	}
	if strings.TrimSpace(verb.fromFile) == "" {
		return nil, exterrors.Validation(exterrors.CodeInvalidParameter,
			"--from-file must not be empty", "provide a JSON or YAML toolbox definition with --from-file")
	}
	input, err := loadLocalToolboxDefinition(verb.fromFile)
	if err != nil {
		return nil, err
	}
	if input.Name != "" && input.Name != name {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidToolboxName,
			fmt.Sprintf("toolbox name %q in %q does not match command name %q", input.Name, verb.fromFile, name),
			"use the same toolbox name in the definition and command, or remove 'name' from the definition",
		)
	}
	if err := validateLocalToolboxDefinition(input); err != nil {
		return nil, err
	}
	return input, nil
}

// runToolboxCreateWith is the testable core. It accepts injected client and
// resolver so unit tests can drive every branch without an HTTP server.
func runToolboxCreateWith(
	ctx context.Context,
	client toolboxClient,
	resolver connectionResolver,
	endpoint, name string,
	verb toolboxCreateFlags,
	parent toolboxFlags,
) error {
	input, err := loadToolboxCreateInput(name, verb, parent)
	if err != nil {
		return err
	}
	return createToolboxFromInput(ctx, client, resolver, endpoint, name, input, parent, nil, nil, os.Stdout, nil)
}

func createToolboxFromInput(
	ctx context.Context, client toolboxClient, resolver connectionResolver, endpoint, name string,
	input *definition.Definition, parent toolboxFlags, access *toolboxAccess, scope *toolboxAccessScope,
	out io.Writer, report func(string),
) error {
	existing, err := access.ensure(ctx, scope, client, name, report)
	if err != nil {
		return err
	}
	if existing != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidToolboxName,
			fmt.Sprintf("toolbox %q already exists", name),
			"use 'connection add/remove' or 'skill add/remove' to create a new version, "+
				"then 'azd ai toolbox publish <name> <version>' to promote it",
		)
	}
	req, err := buildToolboxVersionRequest(ctx, resolver, endpoint, input)
	if err != nil {
		return err
	}
	created, err := client.CreateToolboxVersion(ctx, name, req)
	if err != nil {
		return toolboxWriteError(ctx, err, exterrors.OpCreateToolboxVersion, scope, "", "")
	}

	if cancelErr := toolboxAccessCancellation(ctx, nil); cancelErr != nil {
		return cancelErr
	}

	mcpURL := buildToolboxMcpURL(endpoint, name, created.Version)

	// Surface the endpoint to agents (and the developer) without re-running.
	if err := setToolboxEndpointEnvFunc(ctx, name, mcpURL, ""); err != nil {
		return err
	}

	return emitCreateResult(name, created.Version, parent.output, mcpURL, out)
}

// validateLocalToolboxDefinition checks local invariants before credentials or grants.
func validateLocalToolboxDefinition(input *definition.Definition) error {
	raw, err := validateRawToolEntries(input.Tools)
	if err != nil {
		return err
	}
	names := slices.Clone(raw)
	connections := make(map[string]bool)
	for _, spec := range input.Connections {
		name := strings.TrimSpace(spec.Name)
		if name == "" {
			return exterrors.Validation(exterrors.CodeInvalidParameter,
				"connection name must not be empty", "set connections[].name in the input file")
		}
		if err := validateToolName(name); err != nil {
			return err
		}
		if connections[name] {
			return exterrors.Validation(exterrors.CodeDuplicateConnection,
				fmt.Sprintf("connection %q appears more than once in the input", name), "remove duplicate connection entries")
		}
		if strings.TrimSpace(spec.Index) != "" && strings.TrimSpace(spec.InstanceName) != "" {
			return exterrors.Validation(exterrors.CodeConflictingArguments,
				"connections[].index conflicts with connections[].instanceName",
				"use index for CognitiveSearch or instanceName for GroundingWithCustomSearch, not both")
		}
		connections[name] = true
		names = append(names, map[string]any{"name": name})
	}
	for _, tool := range raw {
		if value, present := tool["connection"]; present {
			if name, ok := value.(string); !ok || strings.TrimSpace(name) == "" {
				return exterrors.Validation(exterrors.CodeInvalidParameter,
					"tools[].connection must be a non-empty name", "set a connection name or omit the connection property")
			}
			if _, present := tool["project_connection_id"]; present {
				return exterrors.Validation(exterrors.CodeConflictingArguments,
					"tools[].connection conflicts with tools[].project_connection_id",
					"specify the connection name or its project_connection_id, not both")
			}
		}
	}
	skills := make([]map[string]any, 0, len(input.Skills))
	for _, spec := range input.Skills {
		if err := validateSkillName(spec.Name); err != nil {
			return err
		}
		skills = append(skills, buildSkillEntry(skillSpec{
			Name: strings.TrimSpace(spec.Name), Version: strings.TrimSpace(spec.Version),
		}))
	}
	if _, err := buildToolboxPolicies(input.Policies); err != nil {
		return err
	}
	if len(raw)+len(input.Connections)+len(skills) == 0 {
		return exterrors.Validation(exterrors.CodeInvalidToolboxName,
			"toolbox definition requires at least one tool or skill entry",
			"add a connection, skill, or raw tool to the toolbox definition")
	}
	if err := validateNoDuplicateConnectionIDs(raw); err != nil {
		return err
	}
	if err := validateNoDuplicateSkills(skills); err != nil {
		return err
	}
	return validateNoDuplicateToolNames(names)
}

func buildToolboxVersionRequest(
	ctx context.Context,
	resolver connectionResolver,
	endpoint string,
	input *definition.Definition,
) (*azure.CreateToolboxVersionRequest, error) {
	entries, err := resolveConnectionSpecs(ctx, resolver, endpoint, input.Connections)
	if err != nil {
		return nil, err
	}

	skillEntries := make([]map[string]any, 0, len(input.Skills))
	for _, skill := range input.Skills {
		if err := validateSkillName(skill.Name); err != nil {
			return nil, err
		}
		skillEntries = append(skillEntries, buildSkillEntry(skillSpec{
			Name:    strings.TrimSpace(skill.Name),
			Version: strings.TrimSpace(skill.Version),
		}))
	}

	rawEntries, err := validateRawToolEntries(input.Tools)
	if err != nil {
		return nil, err
	}
	entries = append(entries, rawEntries...)

	policies, err := buildToolboxPolicies(input.Policies)
	if err != nil {
		return nil, err
	}

	if len(entries) == 0 && len(skillEntries) == 0 {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidToolboxName,
			"toolbox definition requires at least one tool or skill entry",
			"add a connection, skill, or raw tool to the toolbox definition",
		)
	}
	if err := validateNoDuplicateConnectionIDs(entries); err != nil {
		return nil, err
	}
	if err := validateNoDuplicateSkills(skillEntries); err != nil {
		return nil, err
	}
	if err := validateNoDuplicateToolNames(entries); err != nil {
		return nil, err
	}

	return &azure.CreateToolboxVersionRequest{
		Description: input.Description,
		Metadata:    input.Metadata,
		Tools:       entries,
		Skills:      skillEntries,
		Policies:    policies,
	}, nil
}

func emitCreateResult(name, version, output, mcpURL string, out io.Writer) error {
	if output == "json" {
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(map[string]any{
			"toolbox":  name,
			"version":  version,
			"endpoint": mcpURL,
		})
	}

	_, err := fmt.Fprintf(out, "Created toolbox %s at version %s.\nEndpoint: %s\n", name, version, mcpURL)
	return err
}

// resolveConnectionSpecs walks the file's connection list and converts each
// entry into a service tool entry via the connection resolver and buildToolEntry.
func resolveConnectionSpecs(
	ctx context.Context,
	resolver connectionResolver,
	endpoint string,
	specs []toolboxConnectionSpec,
) ([]map[string]any, error) {
	entries := make([]map[string]any, 0, len(specs))
	for _, spec := range specs {
		if strings.TrimSpace(spec.Name) == "" {
			return nil, exterrors.Validation(
				exterrors.CodeInvalidParameter,
				"connection name must not be empty",
				"set connections[].name in the input file",
			)
		}
		conn, err := resolver.resolveConnection(ctx, endpoint, spec.Name)
		if err != nil {
			return nil, err
		}
		entry, err := buildToolEntry(conn, spec.Index, spec.InstanceName)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// validateNoDuplicateConnectionIDs rejects entries that reference the same
// project_connection_id more than once.
func validateNoDuplicateConnectionIDs(entries []map[string]any) error {
	ids := []string{}
	forEachToolConnectionID(entries, func(id string) bool {
		ids = append(ids, id)
		return false
	})

	slices.Sort(ids)
	for i := 1; i < len(ids); i++ {
		if ids[i] == ids[i-1] {
			return exterrors.Validation(
				exterrors.CodeDuplicateConnection,
				fmt.Sprintf("connection %q appears more than once in the input", ids[i]),
				"remove duplicate connection entries",
			)
		}
	}
	return nil
}

// buildToolboxPolicies converts the file-shaped policy spec into the
// data-plane request shape. Returns nil when no policies are configured.
// Currently the only supported policy is the RAI policy name.
func buildToolboxPolicies(spec *toolboxPoliciesSpec) (*azure.ToolboxPolicies, error) {
	if spec == nil || spec.RaiConfig == nil {
		return nil, nil
	}
	name := spec.RaiConfig.ResolvedPolicyName()
	if name == "" {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"policies.raiConfig requires a policy name",
			"set policies.raiConfig.raiPolicyName (or 'name') to the RAI policy to apply",
		)
	}
	return &azure.ToolboxPolicies{
		RaiConfig: &azure.RaiConfig{RaiPolicyName: name},
	}, nil
}

// validateRawToolEntries checks the local invariants on a verbatim tools[]
// entry: non-empty object, required `type` discriminator, and (when set) a
// `name` that matches the service regex. Type-specific shape is left to the
// data plane.
func validateRawToolEntries(tools []map[string]any) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(tools))
	for i, t := range tools {
		if len(t) == 0 {
			return nil, exterrors.Validation(
				exterrors.CodeInvalidParameter,
				fmt.Sprintf("tools[%d] is empty", i),
				"each entry must be an object with at least a 'type' field",
			)
		}
		typeVal, _ := t["type"].(string)
		if strings.TrimSpace(typeVal) == "" {
			return nil, exterrors.Validation(
				exterrors.CodeMissingToolType,
				fmt.Sprintf("tools[%d] is missing the required 'type' field", i),
				"set 'type' to a Foundry tool type (e.g. web_search, file_search, code_interpreter)",
			)
		}
		if raw, present := t["name"]; present {
			nameVal, ok := raw.(string)
			if !ok {
				return nil, exterrors.Validation(
					exterrors.CodeInvalidParameter,
					fmt.Sprintf("tools[%d].name must be a string, got %T", i, raw),
					"set 'name' to a string matching ^[A-Za-z0-9_-]+$ or omit it",
				)
			}
			if err := validateToolName(nameVal); err != nil {
				return nil, err
			}
		}
		out = append(out, t)
	}
	return out, nil
}

// validateNoDuplicateToolNames rejects entries that share the same top-level
// `name`. Names are optional on raw entries but collide in `tools/list` and
// tool_search when set.
func validateNoDuplicateToolNames(entries []map[string]any) error {
	names := []string{}
	for _, t := range entries {
		name, _ := t["name"].(string)
		if strings.TrimSpace(name) == "" {
			continue
		}
		names = append(names, name)
	}
	slices.Sort(names)
	for i := 1; i < len(names); i++ {
		if names[i] == names[i-1] {
			return exterrors.Validation(
				exterrors.CodeDuplicateToolName,
				fmt.Sprintf("tool name %q appears more than once in the input", names[i]),
				"give each tool entry a unique 'name'",
			)
		}
	}
	return nil
}
