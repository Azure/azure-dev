// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"text/tabwriter"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type projectShowResult struct {
	Endpoint     string `json:"endpoint"`
	Source       string `json:"source"`
	SourceDetail string `json:"sourceDetail"`
	AzdEnv       string `json:"azdEnv"`
	SetAt        string `json:"setAt,omitempty"`
}

func newProjectShowCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)
	var flagEndpoint string

	cmd := &cobra.Command{
		Use:   "show",
		Short: "Display the currently resolved Foundry project endpoint.",
		Long: `Display the currently resolved Foundry project endpoint and the source
that provided it. Useful for debugging which endpoint agent commands will use.`,
		Example: `  # Show the resolved endpoint
  azd ai agent project show

  # Check what a specific endpoint would resolve to
  azd ai agent project show -p https://my-project.services.ai.azure.com/api/projects/my-project`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			outputFmt := extCtx.OutputFormat
			ctx := cmd.Context()

			result, err := resolveProjectEndpoint(ctx, resolveProjectEndpointOpts{
				FlagValue: flagEndpoint,
			})
			if err != nil {
				// project show exposes -p / --project-endpoint, so prepend that as the
				// first suggestion bullet to the generic missing-endpoint error.
				if localErr, ok := errors.AsType[*azdext.LocalError](err); ok &&
					localErr.Code == exterrors.CodeMissingProjectEndpoint {
					return exterrors.Dependency(
						exterrors.CodeMissingProjectEndpoint,
						localErr.Message,
						"pass --project-endpoint <url> on this command, or "+localErr.Suggestion,
					)
				}
				return err
			}

			sourceDetail := humanSourceDetail(result.Source, result.AzdEnvName)

			switch outputFmt {
			case "json":
				out := projectShowResult{
					Endpoint:     result.Endpoint,
					Source:       string(result.Source),
					SourceDetail: jsonSourceDetail(result.Source),
					AzdEnv:       result.AzdEnvName,
					SetAt:        result.SetAt,
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
				return w.Flush()
			}
		},
	}

	cmd.Flags().StringVarP(
		&flagEndpoint, "project-endpoint", "p", "",
		"Override the endpoint for this command (useful for debugging)",
	)

	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name:          "output",
		AllowedValues: []string{"json", "table"},
		Default:       "table",
	})

	return cmd
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
