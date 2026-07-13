// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"azure.ai.toolboxes/internal/exterrors"
	"azure.ai.toolboxes/internal/foundry/projectctx"
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

` + fileShapeBlurb(true) + `

At least one of 'connections' or 'tools' must be non-empty.

On success the toolbox's runtime MCP endpoint is written to the active azd
environment under the TOOLBOX_<NORMALIZED_NAME>_MCP_ENDPOINT variable (the same
key agents consume), where <NORMALIZED_NAME> is the toolbox name uppercased with
non-alphanumeric character runs replaced by underscores.

Examples:

  azd ai toolbox create research --from-file ./tools.json
  azd ai toolbox create research --from-file ./tools.yaml --output json
`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runToolboxCreate(
				cmd.Context(), args[0], *flags, readToolboxFlags(cmd, extCtx),
				defaultConnectionResolver{},
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
	if err := validateToolboxName(name); err != nil {
		return err
	}
	if err := validateOutputFormat(parent.output); err != nil {
		return err
	}

	resolved, err := projectctx.Resolve(ctx, projectctx.ResolveOpts{FlagValue: parent.projectEndpoint})
	if err != nil {
		return err
	}
	logResolvedEndpoint("toolbox create", resolved)

	client, err := newToolboxClient(resolved.Endpoint)
	if err != nil {
		return err
	}

	return runToolboxCreateWith(ctx, client, resolver, resolved.Endpoint, name, verb, parent)
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
	if _, err := client.GetToolbox(ctx, name); err == nil {
		return exterrors.Validation(
			exterrors.CodeInvalidToolboxName,
			fmt.Sprintf("toolbox %q already exists", name),
			"use 'connection add/remove' or 'skill add/remove' to create a new version, "+
				"then 'azd ai toolbox publish <name> <version>' to promote it",
		)
	} else if !isAzureNotFound(err) {
		return exterrors.ServiceFromAzure(err, exterrors.OpGetToolbox)
	}

	description := ""
	entries := []map[string]any{}
	skillEntries := []map[string]any{}
	var policies *azure.ToolboxPolicies

	if strings.TrimSpace(verb.fromFile) != "" {
		var input toolboxCreateFile
		if err := parseToolboxFile(verb.fromFile, &input); err != nil {
			return err
		}
		description = input.Description
		resolvedEntries, err := resolveConnectionSpecs(ctx, resolver, endpoint, input.Connections)
		if err != nil {
			return err
		}
		entries = append(entries, resolvedEntries...)
		for _, s := range input.Skills {
			if err := validateSkillName(s.Name); err != nil {
				return err
			}
			skillEntries = append(skillEntries, buildSkillEntry(skillSpec{
				Name:    strings.TrimSpace(s.Name),
				Version: strings.TrimSpace(s.Version),
			}))
		}

		rawEntries, err := validateRawToolEntries(input.Tools)
		if err != nil {
			return err
		}
		entries = append(entries, rawEntries...)

		policies, err = buildToolboxPolicies(input.Policies)
		if err != nil {
			return err
		}
	}

	if len(entries) == 0 {
		return exterrors.Validation(
			exterrors.CodeInvalidToolboxName,
			"toolbox create requires at least one tool entry",
			"pass --from-file with a non-empty 'connections' or 'tools' list",
		)
	}

	if err := validateNoDuplicateConnectionIDs(entries); err != nil {
		return err
	}
	if err := validateNoDuplicateSkills(skillEntries); err != nil {
		return err
	}
	if err := validateNoDuplicateToolNames(entries); err != nil {
		return err
	}

	req := &azure.CreateToolboxVersionRequest{
		Description: description,
		Tools:       entries,
		Skills:      skillEntries,
		Policies:    policies,
	}
	created, err := client.CreateToolboxVersion(ctx, name, req)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpCreateToolboxVersion)
	}

	mcpURL := buildToolboxMcpURL(endpoint, name, created.Version)

	// Surface the endpoint to agents (and the developer) without re-running.
	if err := setToolboxEndpointEnvFunc(ctx, name, mcpURL); err != nil {
		return err
	}

	return emitCreateResult(name, created.Version, parent.output, mcpURL)
}

func emitCreateResult(name, version, output, mcpURL string) error {
	if output == "json" {
		return emitJSON(map[string]any{
			"toolbox":  name,
			"version":  version,
			"endpoint": mcpURL,
		})
	}

	fmt.Printf("Created toolbox %s at version %s.\n", name, version)
	fmt.Printf("Endpoint: %s\n", mcpURL)
	return nil
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
	name := spec.RaiConfig.resolvedPolicyName()
	if name == "" {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"policies.rai_config requires a policy name",
			"set policies.rai_config.rai_policy_name (or 'name') to the RAI policy to apply",
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
