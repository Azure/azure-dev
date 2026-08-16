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

// firstDatasetVersions are the versions a dataset's first publish can carry,
// probed when the version listing has not caught up yet.
//
// The service assigns nothing; the client picks. This CLI's first publish is
// NextVersion(""), so probing a hardcoded "1" never found a dataset this CLI
// had just created -- which is the one case the probe exists for. "1" is still
// probed because a generation job, the SDK or the portal can register one.
var firstDatasetVersions = []string{dataset_api.NextVersion(""), "1"}

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

			localDir, err := datasetUploadSource(fromFile)
			if err != nil {
				return err
			}

			ctx := cmd.Context()
			ec, err := newDatasetContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			existing, listErr := ec.datasetClient.ListDatasetVersions(
				ctx, name, ProjectEndpointAPIVersion,
			)
			exists := listErr == nil && existing != nil && len(existing.Value) > 0
			if !exists {
				// The version listing lags a publish, so a `create` followed by
				// an `update` was told the dataset it had just made does not
				// exist. A direct read usually settles it, catching up sooner
				// than the listing does.
				for _, v := range firstDatasetVersions {
					if _, err := ec.datasetClient.GetDataset(
						ctx, name, v, ProjectEndpointAPIVersion,
					); err == nil {
						exists = true
						break
					}
				}
			}
			// Only an outright 404 proves the name is unknown. An empty 200 does
			// not: latestRegisteredVersion documents that an unknown dataset and
			// a listing that has not caught up are indistinguishable.
			if err := checkAssetExistence(
				verb, "dataset", name, exists, dataset_api.IsNotFound(listErr),
			); err != nil {
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
	// Only on update. create publishes a first version, and the upload derives
	// the next version from whatever this holds, so `create --version 4.0`
	// would publish 5.0 -- and leave the existence probe, which looks for the
	// versions a first publish can carry, unable to find what it wrote.
	if verb == "update" {
		cmd.Flags().StringVar(&version, "version", "",
			"Current version to increment from. Omit to increment from the latest registered version.")
	}
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

// datasetUploadSource resolves what was named into the path the upload reads.
//
// A named file is returned as itself. Returning its directory would upload
// whichever .jsonl sorts first, so pointing --from-file at one dataset in a
// folder holding several would register a different one under that name.
//
// A directory is resolved to the single .jsonl inside it, which is what the
// flag offers. Several is not that, and picking one would be a guess.
func datasetUploadSource(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", messages.ReadingFromFile(path, err)
	}
	if !info.IsDir() {
		if !strings.EqualFold(filepath.Ext(path), ".jsonl") {
			return "", messages.FromFileMustBeJSONL(path)
		}
		return path, nil
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return "", messages.ReadingFromFile(path, err)
	}
	var found []string
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".jsonl") {
			found = append(found, e.Name())
		}
	}
	switch len(found) {
	case 0:
		return "", messages.FromFileDirectoryHasNoJSONL(path)
	case 1:
		return filepath.Join(path, found[0]), nil
	default:
		return "", messages.FromFileDirectoryIsAmbiguous(path, found)
	}
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
			return renderDatasets(cmd, list, messages.NoDatasets())
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
			if !validAssetName(name) {
				return messages.InvalidDatasetName(name)
			}

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
			// An unknown name lists nothing and succeeds; it is not an error.
			// `-o json` callers range over the array, and a delete is checked
			// for idempotence by listing what is left. The empty sentence names
			// the dataset, though: the project may hold plenty of others, so
			// "No datasets found." would be answering a different question.
			return renderDatasets(cmd, list, messages.NoDatasetVersions(name))
		},
	}

	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

// renderDatasets prints a dataset list, or, when it is empty, whatever the
// caller says an empty result means: listing every dataset and listing one
// name's versions come to the same renderer but not to the same sentence.
func renderDatasets(cmd *cobra.Command, list *dataset_api.DatasetList, whenEmpty string) error {
	// JSON is decided before emptiness: a caller piping this into a parser needs
	// an empty array, not the sentence a human would read.
	if list == nil {
		list = &dataset_api.DatasetList{}
	}
	if isJSON(cmd) {
		return emitJSONList(cmd.OutOrStdout(), list.Value)
	}
	rows := make([][]string, 0, len(list.Value))
	for _, d := range list.Value {
		rows = append(rows, []string{d.Name, d.Version, d.Type})
	}
	if len(rows) == 0 {
		fmt.Fprint(cmd.OutOrStdout(), whenEmpty)
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
			if !validAssetName(name) {
				return messages.InvalidDatasetName(name)
			}

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
			if !validAssetName(name) {
				return messages.InvalidDatasetName(name)
			}
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
