// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"azureaiagent/internal/featureflags"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// Status values match `azd config list-alpha`.
const (
	alphaStatusOn      = "On"
	alphaStatusOff     = "Off"
	alphaStatusUnknown = "Unknown"
)

// newConfigCommand returns the `azd ai agent config` parent command.
func newConfigCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect agent extension configuration.",
		Long: `Inspect agent extension configuration.

Use 'azd config' to change azd user configuration.`,
		Example: `  # Display the agent features in alpha stage
  azd ai agent config list-alpha`,
	}
	cmd.AddCommand(newConfigListAlphaCommand(extCtx))
	return cmd
}

// AlphaFeatureItem is the JSON contract for one agent alpha feature.
// Fields added later are additive. Consumers must ignore unknown fields.
type AlphaFeatureItem struct {
	// Name is the feature ID used under alpha in user config.
	Name string `json:"name"`
	// Description is a short summary of the feature.
	Description string `json:"description,omitempty"`
	// Status is On or Off for the selected environment, or Unknown when
	// Error is set.
	Status string `json:"status"`
	// EnvVar is the environment variable that turns the feature on.
	EnvVar string `json:"envVar"`
	// Error is set when the status could not be read.
	Error string `json:"error,omitempty"`
}

type alphaFeatureListResponse struct {
	Features []AlphaFeatureItem `json:"features"`
}

type alphaFeatureReader func(context.Context, featureflags.Feature) (bool, error)

func newConfigListAlphaCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list-alpha",
		Short: "Display the list of agent features in alpha stage.",
		Long: `Display the list of agent features in alpha stage with the status of each
in the selected environment and the environment variable that turns it on.

Use -e/--environment to read another azd environment. When a status cannot be
read, the entry reports the error and the command exits with an error after
listing every feature.`,
		Example: `  # Display the agent features in alpha stage
  azd ai agent config list-alpha

  # Display the features for another environment as JSON
  azd ai agent config list-alpha -e dev --output json

  # Turn on a specific alpha feature
  azd config set alpha.<feature-name> on`,
		Annotations: map[string]string{rejectEmptyEnvironmentAnnotation: "true"},
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			client, err := azdext.NewAzdClient()
			if err != nil {
				err = fmt.Errorf("connecting to azd to read alpha features: %w", err)
			} else {
				defer client.Close()
			}
			environmentName := featureflags.Environment(ctx)
			read := func(ctx context.Context, feature featureflags.Feature) (bool, error) {
				if err != nil {
					return false, err
				}
				return featureflags.IsEnabled(ctx, client, feature, environmentName)
			}
			return runConfigListAlpha(ctx, cmd.OutOrStdout(), extCtx.OutputFormat, read)
		},
	}
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name:          "output",
		AllowedValues: []string{"json", "text"},
		Default:       "text",
	})
	return cmd
}

// runConfigListAlpha reads every registered feature, writes the list, and
// then reports whether any status could not be read.
func runConfigListAlpha(ctx context.Context, out io.Writer, format string, read alphaFeatureReader) error {
	features := featureflags.All()
	items := make([]AlphaFeatureItem, 0, len(features))
	var failures []error
	for _, feature := range features {
		item := AlphaFeatureItem{
			Name:        feature.ID,
			Description: feature.Description,
			Status:      alphaStatusOff,
			EnvVar:      feature.EnvVar,
		}
		enabled, err := read(ctx, feature)
		switch {
		case err != nil:
			item.Status = alphaStatusUnknown
			item.Error = err.Error()
			if errors.Is(err, context.Canceled) {
				failures = append([]error{err}, failures...)
			} else {
				failures = append(failures, err)
			}
		case enabled:
			item.Status = alphaStatusOn
		}
		items = append(items, item)
	}

	var err error
	if format == "json" {
		err = writeJSON(out, alphaFeatureListResponse{Features: items})
	} else {
		err = writeAlphaFeatures(out, items)
	}
	if err != nil {
		return err
	}
	if len(failures) > 0 {
		return &alphaReadError{total: len(items), causes: failures}
	}
	return nil
}

// alphaReadError reports how many statuses could not be read. The listing
// already shows each message, so Error stays short while Unwrap keeps the
// read errors for classification. Cancellation is listed first.
type alphaReadError struct {
	total  int
	causes []error
}

func (e *alphaReadError) Error() string {
	return fmt.Sprintf("could not read the status of %d of %d alpha features", len(e.causes), e.total)
}

func (e *alphaReadError) Unwrap() []error { return e.causes }

// writeAlphaFeatures writes one block per feature in the layout of
// `azd config list-alpha`, plus the environment variable.
func writeAlphaFeatures(out io.Writer, items []AlphaFeatureItem) error {
	blocks := make([]string, 0, len(items))
	for _, item := range items {
		lines := []string{
			"Name: " + item.Name,
			"Description: " + item.Description,
			"Status: " + item.Status,
			"Environment variable: " + item.EnvVar,
		}
		if item.Error != "" {
			lines = append(lines, "Error: "+item.Error)
		}
		blocks = append(blocks, strings.Join(lines, "\n"))
	}
	_, err := fmt.Fprintln(out, strings.Join(blocks, "\n\n"))
	return err
}
