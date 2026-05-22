// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type projectShowFlags struct {
	outputFmt string
}

type projectShowResult struct {
	Endpoint     string `json:"endpoint"`
	Source       string `json:"source"`
	SourceDetail string `json:"sourceDetail"`
	AzdEnv       string `json:"azdEnv"`
	SetAt        string `json:"setAt,omitempty"`
	// FromLegacyAgentsConfig mirrors [resolvedEndpoint.FromLegacyAgentsConfig].
	// True only on the run that migrated the legacy
	// `extensions.ai-agents.project.context` value into the new key, so
	// automation can detect the one-time migration notice without parsing stderr.
	FromLegacyAgentsConfig bool `json:"fromLegacyAgentsConfig,omitempty"`
}

// ProjectShowAction is the action for the `show` command.
type ProjectShowAction struct {
	flags *projectShowFlags
}

func newProjectShowCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)
	flags := &projectShowFlags{}

	cmd := &cobra.Command{
		Use:   "show",
		Short: "Display the currently resolved Foundry project endpoint.",
		Long: `Display the currently resolved Foundry project endpoint and the source
that provided it. Useful for debugging which endpoint commands will use.`,
		Example: `  # Show the resolved endpoint
  azd ai project show`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			flags.outputFmt = extCtx.OutputFormat

			action := &ProjectShowAction{flags: flags}
			return action.Run(cmd.Context())
		},
	}

	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name:          "output",
		AllowedValues: []string{"json", "table"},
		Default:       "table",
	})

	return cmd
}

// Run resolves and displays the current project endpoint and its source.
func (a *ProjectShowAction) Run(ctx context.Context) error {
	result, err := resolveProjectEndpoint(ctx, resolveProjectEndpointOpts{})
	if err != nil {
		// noProjectEndpointError already suggests `azd ai project set`, so
		// the structured error is actionable for `show` unchanged.
		return err
	}

	sourceDetail := humanSourceDetail(result.Source, result.AzdEnvName)

	switch a.flags.outputFmt {
	case "json":
		out := projectShowResult{
			Endpoint:               result.Endpoint,
			Source:                 string(result.Source),
			SourceDetail:           jsonSourceDetail(result.Source),
			AzdEnv:                 result.AzdEnvName,
			SetAt:                  result.SetAt,
			FromLegacyAgentsConfig: result.FromLegacyAgentsConfig,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	default:
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintf(w, "Project endpoint:\t%s\n", result.Endpoint)
		fmt.Fprintf(w, "Source:\t%s\n", sourceDetail)
		if result.Source == SourceGlobalConfig && result.SetAt != "" {
			fmt.Fprintf(w, "Set at:\t%s\n", result.SetAt)
		}
		if err := w.Flush(); err != nil {
			return err
		}
		if result.FromLegacyAgentsConfig {
			fmt.Fprintln(os.Stderr,
				"notice: migrated this endpoint from the legacy "+
					"`extensions.ai-agents.project.context` key to the new "+
					"`extensions.ai-projects.context` key. Future commands "+
					"will read from the new key directly.")
		}
		return nil
	}
}

// humanSourceDetail returns a human-readable label for the endpoint source.
func humanSourceDetail(source EndpointSource, azdEnvName string) string {
	switch source {
	case SourceFlag:
		return "--project-endpoint flag"
	case SourceAzdEnv:
		if azdEnvName != "" {
			return fmt.Sprintf("azd env (%s)", azdEnvName)
		}
		return "azd env"
	case SourceGlobalConfig:
		return "global config (~/.azd/config.json)"
	case SourceFoundryEnv:
		return "FOUNDRY_PROJECT_ENDPOINT"
	default:
		return string(source)
	}
}

// jsonSourceDetail returns a stable, machine-readable source detail string for
// use in JSON output. These values are part of the public JSON contract and
// must not change without a deprecation.
func jsonSourceDetail(source EndpointSource) string {
	switch source {
	case SourceGlobalConfig:
		return "~/.azd/config.json"
	case SourceFoundryEnv:
		return "FOUNDRY_PROJECT_ENDPOINT"
	case SourceFlag:
		return "--project-endpoint flag"
	case SourceAzdEnv:
		return "azd env"
	default:
		return string(source)
	}
}
