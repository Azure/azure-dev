// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/dataset_api"
	"azureaieval/internal/pkg/eval_api"

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

func newDatasetCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dataset",
		Short: "Manage evaluation datasets.",
	}
	cmd.AddCommand(
		newDatasetCreateCommand(),
		newDatasetUpdateCommand(),
		newDatasetListCommand(),
		newDatasetShowCommand(),
		newDatasetDeleteCommand(),
		newDatasetVersionsCommand(),
	)
	return cmd
}

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

// datasetPresence answers whether the dataset is already registered, and
// whether a "no" can be trusted.
//
// The version listing lags a publish, so a create followed straight by an
// update was told the dataset it had just made does not exist. A point read of
// the versions a first publish can carry usually settles that, catching up
// sooner than the listing.
//
// Absence is only certain when the listing itself answered 404. An empty 200
// does not prove it: an unknown dataset and a listing that has not caught up
// are indistinguishable.
//
// A read that failed for any other reason proves nothing at all, and is
// returned. Treating a 403 or a timeout as "not there" let `create` go on to
// publish a further version of a dataset that already existed -- the one thing
// separating create from update, decided by an error nobody looked at.
func datasetPresence(
	ctx context.Context,
	client *dataset_api.DatasetClient,
	name string,
) (exists, absenceCertain bool, err error) {
	existing, listErr := client.ListDatasetVersions(ctx, name, ProjectEndpointAPIVersion)
	if listErr != nil && !dataset_api.IsNotFound(listErr) {
		return false, false, messages.CheckingDataset(name, listErr)
	}
	if listErr == nil && existing != nil && len(existing.Value) > 0 {
		return true, false, nil
	}

	for _, v := range firstDatasetVersions {
		_, getErr := client.GetDataset(ctx, name, v, ProjectEndpointAPIVersion)
		if getErr == nil {
			return true, false, nil
		}
		if !dataset_api.IsNotFound(getErr) {
			return false, false, messages.CheckingDataset(name, getErr)
		}
	}
	return false, dataset_api.IsNotFound(listErr), nil
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

			localSource, err := datasetUploadSource(fromFile)
			if err != nil {
				return err
			}

			ctx := cmd.Context()
			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			exists, absenceCertain, err := datasetPresence(ctx, ec.datasetClient, name)
			if err != nil {
				return err
			}
			if err := checkAssetExistence(
				verb, "dataset", name, exists, absenceCertain,
			); err != nil {
				return err
			}

			ds, err := ec.datasetClient.UploadNextVersion(
				ctx, name, version, localSource, ProjectEndpointAPIVersion,
			)
			if err != nil {
				return messages.RegisteringDataset(name, err)
			}

			if err := ec.setEnvValue(ctx, envKeyDatasetVersion, ds.Version); err != nil {
				// Persisting is a convenience, so this never fails the command.
				// It goes to stdout because azd does not surface an extension's
				// stderr under `azd up`, which is where a deploy would lose it.
				// Skipped outside a project, where having nowhere to persist is
				// expected rather than notable.
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
// folder holding several would register a different one under that name — and
// the fingerprint would describe the file that was named, so the two would
// agree forever afterwards.
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
			ec, err := newEvalContext(ctx, endpointFlg)
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
			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			list, err := ec.datasetClient.ListDatasetVersions(ctx, name, ProjectEndpointAPIVersion)
			if err != nil {
				return messages.ListingDatasetVersions(name, err)
			}
			// An unknown name lists nothing and succeeds; it is not an error.
			// `-o json` callers range over the array, and `dataset delete` is
			// checked for idempotence by listing what is left. The empty sentence
			// names the dataset, though: the project may hold plenty of others, so
			// "No datasets found." would be answering a different question.
			return renderDatasets(cmd, list, messages.NoDatasetVersions(name))
		},
	}

	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

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
	// TYPE, not FORMAT: the service populates type (`uri_file`) and leaves
	// format empty, so the column was blank on every row.
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
			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			if version == "" {
				list, err := ec.datasetClient.ListDatasetVersions(ctx, name, ProjectEndpointAPIVersion)
				if err != nil {
					return messages.ResolvingLatestDatasetVersion(name, err)
				}
				if len(list.Value) == 0 {
					// The service answers an unknown name with an empty list
					// rather than a 404, and a dataset cannot exist with no
					// versions, so this is what "no such dataset" looks like.
					return messages.DatasetNotFound(name)
				}
				version = dataset_api.LatestVersion(list.Value)
			}

			ds, err := ec.datasetClient.GetDataset(ctx, name, version, ProjectEndpointAPIVersion)
			if err != nil {
				if eval_api.IsNotFound(err) {
					return messages.DatasetVersionNotFoundWithHint(name, version)
				}
				return messages.ReadingDatasetVersion(name, version, err)
			}

			if isJSON(cmd) {
				return emitJSON(cmd.OutOrStdout(), ds)
			}
			if err := emitDetail(cmd.OutOrStdout(), []field{
				{"Name", ds.Name},
				{"Version", ds.Version},
				{"Type", ds.Type},
				{"URI", ds.ResolvedBlobURI()},
			}); err != nil {
				return err
			}
			if prefix := ec.portalPrefix(ctx); prefix != nil {
				writePortalLink(cmd.OutOrStdout(), prefix.DatasetURL(ds.Name, ds.Version))
			}
			return nil
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
			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			if err := ec.datasetClient.DeleteDatasetVersion(
				ctx, name, version, ProjectEndpointAPIVersion,
			); err != nil {
				if eval_api.IsNotFound(err) {
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
