// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"azureaieval/internal/pkg/dataset_api"

	"github.com/spf13/cobra"
)

func newDatasetCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dataset",
		Short: "Manage evaluation datasets.",
	}
	cmd.AddCommand(
		newDatasetCreateCommand(),
		newDatasetListCommand(),
		newDatasetShowCommand(),
		newDatasetDeleteCommand(),
	)
	return cmd
}

// newDatasetCreateCommand builds `dataset create`.
//
// There is no separate `update`: every registration publishes a new immutable
// version and the server auto-increments, so `create` covers both the first
// version and every later one.
func newDatasetCreateCommand() *cobra.Command {
	var (
		name        string
		file        string
		version     string
		endpointFlg string
	)

	use := "create"
	short := "Register a dataset, publishing a new version."

	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return requireFlag("name")
			}
			if file == "" {
				return requireFlag("file")
			}

			info, err := os.Stat(file)
			if err != nil {
				return fmt.Errorf("reading --file %q: %w", file, err)
			}
			// The upload helper scans a directory for the first .jsonl, so pass
			// the containing directory when given a file path.
			localDir := file
			if !info.IsDir() {
				if !strings.EqualFold(filepath.Ext(file), ".jsonl") {
					return fmt.Errorf("--file must be a .jsonl file or a directory containing one, got %q", file)
				}
				localDir = filepath.Dir(file)
			}

			ctx := cmd.Context()
			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			ds, err := ec.datasetClient.UploadNextVersion(
				ctx, name, version, localDir, ProjectEndpointAPIVersion,
			)
			if err != nil {
				return fmt.Errorf("registering dataset %q: %w", name, err)
			}

			if err := ec.setEnvValue(ctx, envKeyDatasetVersion, ds.Version); err != nil {
				// Persisting is a convenience, so this never fails the command.
				// It goes to stdout because azd does not surface an extension's
				// stderr, and is skipped outside a project, where having nowhere
				// to persist is expected rather than notable.
				if !errors.Is(err, errNoAzdEnvironment) && !isJSON(cmd) {
					fmt.Fprintf(cmd.OutOrStdout(), "warning: %v\n", err)
				}
			}

			if isJSON(cmd) {
				return emitJSON(cmd.OutOrStdout(), ds)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Registered dataset %s version %s\n", ds.Name, ds.Version)
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Name of the dataset.")
	cmd.Flags().StringVar(&file, "file", "", "Path to a .jsonl file, or a directory containing one.")
	cmd.Flags().StringVar(&version, "version", "",
		"Current version to increment from. Omit to increment from the latest registered version.")
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

func newDatasetListCommand() *cobra.Command {
	var (
		name        string
		endpointFlg string
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List registered datasets, or the versions of one dataset.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			var list *dataset_api.DatasetList
			if name != "" {
				list, err = ec.datasetClient.ListDatasetVersions(ctx, name, ProjectEndpointAPIVersion)
			} else {
				list, err = ec.datasetClient.ListDatasets(ctx, ProjectEndpointAPIVersion)
			}
			if err != nil {
				return fmt.Errorf("listing datasets: %w", err)
			}

			if isJSON(cmd) {
				return emitJSONList(cmd.OutOrStdout(), list.Value)
			}
			rows := make([][]string, 0, len(list.Value))
			for _, d := range list.Value {
				rows = append(rows, []string{d.Name, d.Version, d.Format})
			}
			if len(rows) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No datasets found.")
				return nil
			}
			return emitTable(cmd.OutOrStdout(), []string{"NAME", "VERSION", "FORMAT"}, rows)
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Limit the listing to versions of this dataset.")
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

func newDatasetShowCommand() *cobra.Command {
	var (
		name        string
		version     string
		endpointFlg string
	)

	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show a dataset version.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return requireFlag("name")
			}

			ctx := cmd.Context()
			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			if version == "" {
				list, err := ec.datasetClient.ListDatasetVersions(ctx, name, ProjectEndpointAPIVersion)
				if err != nil {
					return fmt.Errorf("resolving the latest version of %q: %w", name, err)
				}
				if len(list.Value) == 0 {
					return fmt.Errorf("dataset %q has no versions", name)
				}
				version = dataset_api.LatestVersion(list.Value)
			}

			ds, err := ec.datasetClient.GetDataset(ctx, name, version, ProjectEndpointAPIVersion)
			if err != nil {
				return fmt.Errorf("reading dataset %q version %q: %w", name, version, err)
			}

			if isJSON(cmd) {
				return emitJSON(cmd.OutOrStdout(), ds)
			}
			return emitTable(cmd.OutOrStdout(),
				[]string{"NAME", "VERSION", "FORMAT", "URI"},
				[][]string{{ds.Name, ds.Version, ds.Format, ds.ResolvedBlobURI()}},
			)
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Name of the dataset.")
	cmd.Flags().StringVar(&version, "version", "", "Version to show. Omit for the latest.")
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

func newDatasetDeleteCommand() *cobra.Command {
	var (
		name        string
		version     string
		endpointFlg string
	)

	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete a dataset version.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return requireFlag("name")
			}
			if version == "" {
				return requireFlag("version")
			}

			ctx := cmd.Context()
			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			if err := ec.datasetClient.DeleteDatasetVersion(
				ctx, name, version, ProjectEndpointAPIVersion,
			); err != nil {
				return fmt.Errorf("deleting dataset %q version %q: %w", name, version, err)
			}

			if isJSON(cmd) {
				return emitJSON(cmd.OutOrStdout(), map[string]string{
					"name": name, "version": version, "status": "deleted",
				})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted dataset %s version %s\n", name, version)
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Name of the dataset.")
	cmd.Flags().StringVar(&version, "version", "", "Version to delete.")
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}
