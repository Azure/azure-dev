// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"azureaidataset/internal/messages"
	"azureaidataset/internal/pkg/dataset_api"

	"github.com/spf13/cobra"
)

// firstDatasetVersion is the version the service assigns to a dataset's first
// publish, and so the one that exists for every dataset that exists at all.
const firstDatasetVersion = "1"

// newDatasetCreateCommand builds `dataset create <name>`, which registers a
// dataset that does not exist yet.
func newDatasetCreateCommand() *cobra.Command {
	return newDatasetWriteCommand("create", "Register a dataset, publishing its first version.")
}

// newDatasetUpdateCommand builds `dataset update <name>`, which publishes a
// further version of one that does.
func newDatasetUpdateCommand() *cobra.Command {
	return newDatasetWriteCommand("update", "Publish a new version of a dataset.")
}

// newDatasetWriteCommand builds create and update. Both run the same upload,
// and the existence check is the only thing that separates them: a version is
// brought into being by startPendingUpload, which neither knows nor cares
// whether the name was already in use.
func newDatasetWriteCommand(verb, short string) *cobra.Command {
	var (
		fromFile    string
		version     string
		endpointFlg string
	)

	cmd := &cobra.Command{
		Use:   verb + " <name>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if !validAssetName(name) {
				return messages.InvalidDatasetName(name)
			}
			if fromFile == "" {
				return requireFlag("from-file")
			}

			localDir, err := datasetUploadDir(fromFile)
			if err != nil {
				return err
			}

			ctx := cmd.Context()
			ec, err := newDatasetContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			existing, err := ec.datasetClient.ListDatasetVersions(
				ctx, name, ProjectEndpointAPIVersion,
			)
			exists := err == nil && existing != nil && len(existing.Value) > 0
			if !exists {
				// The version listing lags a publish, so a `create` followed by
				// an `update` was told the dataset it had just made does not
				// exist. A direct read of the first version settles it: point
				// reads go consistent immediately.
				if _, err := ec.datasetClient.GetDataset(
					ctx, name, firstDatasetVersion, ProjectEndpointAPIVersion,
				); err == nil {
					exists = true
				}
			}
			if err := checkAssetExistence(verb, "dataset", name, exists); err != nil {
				return err
			}

			ds, err := ec.datasetClient.UploadNextVersion(
				ctx, name, version, localDir, ProjectEndpointAPIVersion,
			)
			if err != nil {
				return messages.RegisteringDataset(name, err)
			}

			if err := ec.setEnvValue(ctx, envKeyDatasetVersion, ds.Version); err != nil {
				// Persisting is a convenience, so this never fails the command.
				// It goes to stdout because azd does not surface an extension's
				// stderr, and is skipped outside a project, where having nowhere
				// to persist is expected rather than notable.
				if !errors.Is(err, errNoAzdEnvironment) && !isJSON(cmd) {
					fmt.Fprint(cmd.OutOrStdout(), messages.Warning(err))
				}
			}

			if isJSON(cmd) {
				return emitJSON(cmd.OutOrStdout(), ds)
			}
			fmt.Fprint(cmd.OutOrStdout(), messages.DatasetRegistered(ds.Name, ds.Version))
			return nil
		},
	}

	cmd.Flags().StringVar(&fromFile, "from-file", "",
		"Path to a .jsonl file, or a directory containing one.")
	cmd.Flags().StringVar(&version, "version", "",
		"Current version to increment from. Omit to increment from the latest registered version.")
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

// datasetUploadDir resolves what was named into the directory the upload scans.
func datasetUploadDir(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", messages.ReadingFromFile(path, err)
	}
	if info.IsDir() {
		return path, nil
	}
	if !strings.EqualFold(filepath.Ext(path), ".jsonl") {
		return "", messages.FromFileMustBeJSONL(path)
	}
	return filepath.Dir(path), nil
}

func newDatasetListCommand() *cobra.Command {
	var endpointFlg string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the project's datasets.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			ec, err := newDatasetContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			list, err := ec.datasetClient.ListDatasets(ctx, ProjectEndpointAPIVersion)
			if err != nil {
				return messages.ListingDatasets(err)
			}
			return renderDatasets(cmd, list)
		},
	}

	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

// newDatasetVersionsCommand groups the version listing, so that `list` means
// the assets rather than the history of one of them.
func newDatasetVersionsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "versions",
		Short: "Inspect the versions of one dataset.",
	}
	cmd.AddCommand(newDatasetVersionsListCommand())
	return cmd
}

func newDatasetVersionsListCommand() *cobra.Command {
	var endpointFlg string

	cmd := &cobra.Command{
		Use:   "list <name>",
		Short: "List the versions of a dataset.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			ctx := cmd.Context()
			ec, err := newDatasetContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			list, err := ec.datasetClient.ListDatasetVersions(ctx, name, ProjectEndpointAPIVersion)
			if err != nil {
				return messages.ListingDatasetVersions(name, err)
			}
			// An unknown name comes back as an empty list, and "No datasets
			// found" reads as though the project had none at all.
			if len(list.Value) == 0 {
				return messages.DatasetNotFound(name)
			}
			return renderDatasets(cmd, list)
		},
	}

	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

func renderDatasets(cmd *cobra.Command, list *dataset_api.DatasetList) error {
	if list == nil {
		fmt.Fprint(cmd.OutOrStdout(), messages.NoDatasets())
		return nil
	}
	if isJSON(cmd) {
		return emitJSONList(cmd.OutOrStdout(), list.Value)
	}
	rows := make([][]string, 0, len(list.Value))
	for _, d := range list.Value {
		rows = append(rows, []string{d.Name, d.Version, d.Type})
	}
	if len(rows) == 0 {
		fmt.Fprint(cmd.OutOrStdout(), messages.NoDatasets())
		return nil
	}
	// TYPE, not FORMAT: format is a field this API accepts on upload and never
	// returns, so the column it filled was empty for every dataset ever listed.
	return emitTable(cmd.OutOrStdout(), []string{"NAME", "VERSION", "TYPE"}, rows)
}

func newDatasetShowCommand() *cobra.Command {
	var (
		version     string
		endpointFlg string
	)

	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show a dataset version.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			ctx := cmd.Context()
			ec, err := newDatasetContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			if version == "" {
				list, err := ec.datasetClient.ListDatasetVersions(ctx, name, ProjectEndpointAPIVersion)
				if err != nil {
					return messages.ResolvingLatestDatasetVersion(name, err)
				}
				// The service answers an unknown name with an empty list rather
				// than a 404, so this is what "no such dataset" looks like. A
				// dataset cannot exist with no versions.
				if len(list.Value) == 0 {
					return messages.DatasetNotFound(name)
				}
				version = dataset_api.LatestVersion(list.Value)
			}

			ds, err := ec.datasetClient.GetDataset(ctx, name, version, ProjectEndpointAPIVersion)
			if err != nil {
				if dataset_api.IsNotFound(err) {
					return messages.DatasetVersionNotFoundWithHint(name, version)
				}
				return messages.ReadingDatasetVersion(name, version, err)
			}

			if isJSON(cmd) {
				return emitJSON(cmd.OutOrStdout(), ds)
			}
			return emitDetail(cmd.OutOrStdout(), []field{
				{"Name", ds.Name},
				{"Version", ds.Version},
				{"Type", ds.Type},
				{"URI", ds.ResolvedBlobURI()},
			})
		},
	}

	cmd.Flags().StringVar(&version, "version", "", "Version to show. Omit for the latest.")
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

func newDatasetDeleteCommand() *cobra.Command {
	var (
		version     string
		endpointFlg string
	)

	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a dataset version.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if version == "" {
				return requireFlag("version")
			}

			ctx := cmd.Context()
			ec, err := newDatasetContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			if err := ec.datasetClient.DeleteDatasetVersion(
				ctx, name, version, ProjectEndpointAPIVersion,
			); err != nil {
				if dataset_api.IsNotFound(err) {
					return messages.DatasetVersionNotFound(name, version)
				}
				return messages.DeletingDatasetVersion(name, version, err)
			}

			if isJSON(cmd) {
				return emitJSON(cmd.OutOrStdout(), map[string]string{
					"name": name, "version": version, "status": "deleted",
				})
			}
			fmt.Fprint(cmd.OutOrStdout(), messages.DatasetDeleted(name, version))
			return nil
		},
	}

	cmd.Flags().StringVar(&version, "version", "", "Version to delete.")
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}
