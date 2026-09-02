// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/dataset_api"

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
// datasetWriteFlags are the flags create and update share.
type datasetWriteFlags struct {
	fromFile    string
	version     string
	endpointFlg string
}

// datasetWriteAction publishes a dataset version.
type datasetWriteAction struct {
	cmd   *cobra.Command
	flags *datasetWriteFlags
	verb  string
	name  string
}

func newDatasetWriteCommand(verb, short string) *cobra.Command {
	flags := &datasetWriteFlags{}

	cmd := &cobra.Command{
		Use:   verb + " <name>",
		Short: short,
		Args:  requiredArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return (&datasetWriteAction{
				cmd: cmd, flags: flags, verb: verb, name: args[0],
			}).Run()
		},
	}

	cmd.Flags().StringVar(&flags.fromFile, "from-file", "",
		"Path to a .jsonl file, or a directory containing one.")
	cmd.Flags().StringVar(&flags.version, "version", "",
		"Version to publish. Omit to publish the next version after the latest registered.")
	cmd.Flags().StringVar(&flags.endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

func (a *datasetWriteAction) Run() error {
	if !validAssetName(a.name) {
		return messages.InvalidDatasetName(a.name)
	}
	if a.flags.fromFile == "" {
		return requireFlag("from-file")
	}

	localSource, err := datasetUploadSource(a.flags.fromFile)
	if err != nil {
		return err
	}

	ctx := a.cmd.Context()
	ec, err := newEvalContext(ctx, a.flags.endpointFlg)
	if err != nil {
		return err
	}
	defer ec.Close()

	exists, absenceCertain, err := datasetPresence(ctx, ec.datasetClient, a.name)
	if err != nil {
		return err
	}
	if err := checkAssetExistence(a.verb, "dataset", a.name, exists, absenceCertain); err != nil {
		return err
	}

	ds, err := a.publish(ctx, ec, localSource)
	if err != nil {
		return messages.RegisteringDataset(a.name, err)
	}

	if isJSON(a.cmd) {
		return emitJSON(a.cmd.OutOrStdout(), ds)
	}
	fmt.Fprint(a.cmd.OutOrStdout(), messages.DatasetRegistered(ds.Name, ds.Version))
	return nil
}

// publish writes the version, deriving it only when the author did not name one.
//
// A declared version is the version to publish, never one to count from, so it
// is written exactly as given. Only an omitted version is derived, and only that
// path walks past a conflict: a version the author named and the service
// already holds is theirs to resolve, and stepping past it would publish one
// they did not ask for.
func (a *datasetWriteAction) publish(
	ctx context.Context,
	ec *evalContext,
	localSource string,
) (*dataset_api.Dataset, error) {
	if a.flags.version != "" {
		return ec.datasetClient.UploadVersion(
			ctx, a.name, a.flags.version, localSource, ProjectEndpointAPIVersion)
	}
	return ec.datasetClient.UploadNextVersion(
		ctx, a.name, "", localSource, ProjectEndpointAPIVersion)
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

// datasetListAction lists the project's datasets.
type datasetListAction struct {
	cmd      *cobra.Command
	endpoint string
}

func newDatasetListCommand() *cobra.Command {
	var endpointFlg string
	var displayLimit int
	var showAll bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the project's datasets.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return (&datasetListAction{cmd: cmd, endpoint: endpointFlg}).Run()
		},
	}

	addDisplayPagingFlags(cmd, &displayLimit, &showAll, defaultPageSize)
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

func (a *datasetListAction) Run() error {
	ctx := a.cmd.Context()
	ec, err := newEvalContext(ctx, a.endpoint)
	if err != nil {
		return err
	}
	defer ec.Close()

	list, err := ec.datasetClient.ListDatasets(ctx, ProjectEndpointAPIVersion)
	if err != nil {
		return messages.ListingDatasets(err)
	}
	return renderDatasets(a.cmd, list, messages.NoDatasets())
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

// datasetVersionsListAction lists the versions of one dataset.
type datasetVersionsListAction struct {
	cmd      *cobra.Command
	endpoint string
	name     string
}

func newDatasetVersionsListCommand() *cobra.Command {
	var endpointFlg string
	var displayLimit int
	var showAll bool

	cmd := &cobra.Command{
		Use:   "list <name>",
		Short: "List the versions of a dataset.",
		Args:  requiredArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return (&datasetVersionsListAction{
				cmd: cmd, endpoint: endpointFlg, name: args[0],
			}).Run()
		},
	}

	addDisplayPagingFlags(cmd, &displayLimit, &showAll, defaultPageSize)
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

func (a *datasetVersionsListAction) Run() error {
	if !validLookupName(a.name) {
		return messages.InvalidDatasetName(a.name)
	}

	ctx := a.cmd.Context()
	ec, err := newEvalContext(ctx, a.endpoint)
	if err != nil {
		return err
	}
	defer ec.Close()

	list, err := ec.datasetClient.ListDatasetVersions(ctx, a.name, ProjectEndpointAPIVersion)
	if err != nil {
		return messages.ListingDatasetVersions(a.name, err)
	}
	// An unknown name lists nothing and succeeds; it is not an error. `-o json`
	// callers range over the array, and `dataset delete` is checked for
	// idempotence by listing what is left. The empty sentence names the dataset,
	// though: the project may hold plenty of others, so "No datasets found."
	// would be answering a different question.
	return renderDatasets(a.cmd, list, messages.NoDatasetVersions(a.name))
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
	shown, total, trimmed := trimForDisplay(cmd, list.Value)
	rows := make([][]string, 0, len(shown))
	for _, d := range shown {
		rows = append(rows, []string{d.Name, d.Version, d.Type})
	}
	if len(rows) == 0 {
		fmt.Fprint(cmd.OutOrStdout(), whenEmpty)
		return nil
	}
	// TYPE, not FORMAT: the service populates type (`uri_file`) and leaves
	// format empty, so the column was blank on every row.
	if err := emitTable(cmd.OutOrStdout(), []string{"NAME", "VERSION", "TYPE"}, rows); err != nil {
		return err
	}
	if trimmed {
		fmt.Fprint(cmd.OutOrStdout(), messages.ShowingSomeOf(len(rows), total))
	}
	return nil
}

// datasetShowAction reads one dataset version.
type datasetShowAction struct {
	cmd      *cobra.Command
	endpoint string
	version  string
	name     string
}

func newDatasetShowCommand() *cobra.Command {
	var (
		version     string
		endpointFlg string
	)

	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show a dataset version.",
		Args:  requiredArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return (&datasetShowAction{
				cmd: cmd, endpoint: endpointFlg, version: version, name: args[0],
			}).Run()
		},
	}

	cmd.Flags().StringVar(&version, "version", "", "Version to show. Omit for the latest.")
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

func (a *datasetShowAction) Run() error {
	if !validLookupName(a.name) {
		return messages.InvalidDatasetName(a.name)
	}

	ctx := a.cmd.Context()
	ec, err := newEvalContext(ctx, a.endpoint)
	if err != nil {
		return err
	}
	defer ec.Close()

	version := a.version
	if version == "" {
		list, err := ec.datasetClient.ListDatasetVersions(ctx, a.name, ProjectEndpointAPIVersion)
		if err != nil {
			return messages.ResolvingLatestDatasetVersion(a.name, err)
		}
		if len(list.Value) == 0 {
			// The service answers an unknown name with an empty list rather than
			// a 404, and a dataset cannot exist with no versions, so this is
			// what "no such dataset" looks like.
			return messages.DatasetNotFound(a.name)
		}
		version = dataset_api.LatestVersion(list.Value)
	}

	ds, err := ec.datasetClient.GetDataset(ctx, a.name, version, ProjectEndpointAPIVersion)
	if err != nil {
		if dataset_api.IsNotFound(err) {
			return messages.DatasetVersionNotFoundWithHint(a.name, version)
		}
		return messages.ReadingDatasetVersion(a.name, version, err)
	}

	if isJSON(a.cmd) {
		return emitJSON(a.cmd.OutOrStdout(), ds)
	}
	if err := emitDetail(a.cmd.OutOrStdout(), []field{
		{"Name", ds.Name},
		{"Version", ds.Version},
		{"Type", ds.Type},
		{"URI", ds.ResolvedBlobURI()},
	}); err != nil {
		return err
	}
	if prefix := ec.portalPrefix(ctx); prefix != nil {
		writePortalLink(a.cmd.OutOrStdout(), prefix.DatasetURL(ds.Name, ds.Version))
	}
	return nil
}

// datasetDeleteAction removes one dataset version.
type datasetDeleteAction struct {
	cmd      *cobra.Command
	endpoint string
	version  string
	force    bool
	name     string
}

func newDatasetDeleteCommand() *cobra.Command {
	var (
		version     string
		force       bool
		endpointFlg string
	)

	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a dataset version.",
		Long: "Delete a dataset version.\n\n" +
			"Asks before removing it. With --no-prompt, or with JSON output, " +
			"--force is required.",
		Args: requiredArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return (&datasetDeleteAction{
				cmd: cmd, endpoint: endpointFlg, version: version, force: force, name: args[0],
			}).Run()
		},
	}

	cmd.Flags().StringVar(&version, "version", "", "Version to delete.")
	registerForceFlag(cmd, &force)
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

func (a *datasetDeleteAction) Run() error {
	if !validLookupName(a.name) {
		return messages.InvalidDatasetName(a.name)
	}
	if a.version == "" {
		return requireFlag("version")
	}

	ctx := a.cmd.Context()
	ec, err := newEvalContext(ctx, a.endpoint)
	if err != nil {
		return err
	}
	defer ec.Close()

	subject := fmt.Sprintf("dataset %s version %s", a.name, a.version)
	goAhead, err := confirmDelete(a.cmd, ec, subject, a.force)
	if err != nil {
		return err
	}
	if !goAhead {
		return deleteDeclined(a.cmd, subject)
	}

	if err := ec.datasetClient.DeleteDatasetVersion(
		ctx, a.name, a.version, ProjectEndpointAPIVersion,
	); err != nil {
		if dataset_api.IsNotFound(err) {
			return messages.DatasetVersionNotFound(a.name, a.version)
		}
		return messages.DeletingDatasetVersion(a.name, a.version, err)
	}

	if isJSON(a.cmd) {
		return emitJSON(a.cmd.OutOrStdout(), map[string]string{
			"name": a.name, "version": a.version, "status": "deleted",
		})
	}
	fmt.Fprint(a.cmd.OutOrStdout(), messages.DatasetDeleted(a.name, a.version))
	return nil
}
