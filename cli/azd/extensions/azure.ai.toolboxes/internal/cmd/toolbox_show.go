// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"

	"azure.ai.toolboxes/internal/exterrors"
	"azure.ai.toolboxes/internal/pkg/azure"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// toolboxShowFlags carries the verb-specific flags for `toolbox show`.
type toolboxShowFlags struct {
	version string
}

// newToolboxShowCommand returns the `azd ai toolbox show <name>` command.
func newToolboxShowCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)
	flags := &toolboxShowFlags{}

	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show a toolbox version, including its computed MCP endpoint.",
		Long: `Show a toolbox.

By default shows the default version. Use --version to inspect a specific
version. The output includes the toolbox's runtime MCP endpoint, which agents
consume via the TOOLBOX_<NORMALIZED_NAME>_MCP_ENDPOINT environment variable
convention, where <NORMALIZED_NAME> is the toolbox name uppercased with
non-alphanumeric character runs replaced by underscores.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runToolboxShow(cmd.Context(), args[0], *flags, readToolboxFlags(cmd, extCtx))
		},
	}

	cmd.Flags().StringVar(
		&flags.version, "version", "",
		"Specific version to show. Defaults to the server's default version.",
	)
	registerToolboxOutputFlag(cmd)

	return cmd
}

func runToolboxShow(
	ctx context.Context, name string, verb toolboxShowFlags, parent toolboxFlags,
) error {
	if err := validateToolboxName(name); err != nil {
		return err
	}
	if err := validateOutputFormat(parent.output); err != nil {
		return err
	}

	client, resolved, err := resolveToolboxAndClient(ctx, parent)
	if err != nil {
		return err
	}
	logResolvedEndpoint("toolbox show", resolved)

	return runToolboxShowWith(ctx, client, resolved.Endpoint, name, verb, parent)
}

// runToolboxShowWith is the testable core.
func runToolboxShowWith(
	ctx context.Context, client toolboxClient, endpoint, name string,
	verb toolboxShowFlags, parent toolboxFlags,
) error {
	tb, err := client.GetToolbox(ctx, name)
	if err != nil {
		return toolboxNotFoundOrService(err, name, exterrors.OpGetToolbox)
	}

	shownVersion := verb.version
	if shownVersion == "" {
		shownVersion = tb.DefaultVersion
	}

	version, err := client.GetToolboxVersion(ctx, name, shownVersion)
	if err != nil {
		if isAzureNotFound(err) {
			return exterrors.Dependency(
				exterrors.CodeToolboxNotFound,
				fmt.Sprintf("version %q of toolbox %q not found", shownVersion, name),
				fmt.Sprintf("run 'azd ai toolbox show %q' to see the default version", name),
			)
		}
		return exterrors.ServiceFromAzure(err, exterrors.OpGetToolboxVersion)
	}

	mcpURL := buildToolboxMcpURL(endpoint, name, shownVersion)

	if parent.output == "json" {
		return emitShowJSON(tb, version, mcpURL)
	}
	return emitShowTable(tb, version, mcpURL)
}

// buildToolboxMcpURL computes the runtime MCP consumption URL.
// version is service-supplied so both segments are PathEscaped.
func buildToolboxMcpURL(endpoint, name, version string) string {
	return fmt.Sprintf(
		"%s/toolboxes/%s/versions/%s/mcp?api-version=v1",
		strings.TrimRight(endpoint, "/"),
		url.PathEscape(name),
		url.PathEscape(version),
	)
}

// emitShowJSON prints the JSON envelope for `toolbox show`.
func emitShowJSON(
	tb *azure.ToolboxObject, version *azure.ToolboxVersionObject, mcpURL string,
) error {
	return emitJSON(map[string]any{
		"toolbox":  tb,
		"version":  version,
		"endpoint": mcpURL,
	})
}

// emitShowTable renders the table form of `toolbox show`.
func emitShowTable(
	tb *azure.ToolboxObject, version *azure.ToolboxVersionObject, mcpURL string,
) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "FIELD\tVALUE")
	fmt.Fprintln(w, "-----\t-----")
	fmt.Fprintf(w, "Name\t%s\n", tb.Name)
	fmt.Fprintf(w, "Default version\t%s\n", tb.DefaultVersion)
	fmt.Fprintf(w, "Shown version\t%s\n", version.Version)
	fmt.Fprintf(w, "Description\t%s\n", version.Description)
	fmt.Fprintf(w, "Endpoint\t%s\n", mcpURL)
	fmt.Fprintf(w, "Tools\t%d\n", len(version.Tools))
	fmt.Fprintf(w, "Skills\t%d\n", len(version.Skills))
	if err := w.Flush(); err != nil {
		return err
	}

	if len(version.Tools) > 0 {
		fmt.Println()
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "TOOL\tTYPE\tDETAIL")
		fmt.Fprintln(tw, "----\t----\t------")
		for _, tool := range version.Tools {
			toolName, _ := tool["name"].(string)
			toolType, _ := tool["type"].(string)
			detail := describeToolDetail(tool)
			fmt.Fprintf(tw, "%s\t%s\t%s\n", toolName, toolType, detail)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}

	if len(version.Skills) > 0 {
		fmt.Println()
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "SKILL\tVERSION\tTYPE")
		fmt.Fprintln(tw, "-----\t-------\t----")
		for _, sk := range version.Skills {
			name, _ := sk["name"].(string)
			skType, _ := sk["type"].(string)
			ver, _ := sk["version"].(string)
			if ver == "" {
				ver = "(default)"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\n", name, ver, skType)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	return nil
}

// describeToolDetail returns the per-tool annotation used in the show table:
// "(connection:<id>)" when the entry references a project connection (in any
// recognized shape), otherwise "(builtin)". Driving this off the tool entry's
// shape rather than a hardcoded type allow-list means new connection-backed
// tool types are surfaced automatically.
func describeToolDetail(tool map[string]any) string {
	if id := firstConnectionID(tool); id != "" {
		return "(connection:" + id + ")"
	}
	return "(builtin)"
}

// firstConnectionID returns the first project_connection_id referenced by a
// tool entry — top-level on `mcp` tools, or nested under
// azure_ai_search.indexes[] for search tools.
func firstConnectionID(tool map[string]any) string {
	var found string
	toolEntryReferences(tool, func(id string) bool {
		found = id
		return true // stop at the first hit
	})
	return found
}
