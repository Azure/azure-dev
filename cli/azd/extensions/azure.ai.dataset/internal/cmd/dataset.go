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
	"time"

	"azureaidataset/internal/exterrors"
	"azureaidataset/internal/messages"
	"azureaidataset/internal/pkg/dataset_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
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

// datasetPresence answers whether the dataset is already registered, and
// whether a "no" can be trusted.
//
// The version listing lags a publish, so a create followed straight by an
// update was told the dataset it had just made does not exist. A point read of
// the versions a first publish can carry usually settles that, catching up
// sooner than the listing.
//
// Absence is only certain when the listing itself answered 404. An empty 200
// does not prove it: latestRegisteredVersion documents that an unknown dataset
// and a listing that has not caught up are indistinguishable.
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

// How long a version listing is given to catch up before it is believed.
const (
	versionListingSettleAttempts = 5
	versionListingSettleDelay    = 400 * time.Millisecond
)

// settledLatestVersion gives the version listing a moment to catch up before
// `update` counts from it.
//
// An empty listing does not distinguish a dataset the service has not caught up
// on from one that is not there, and `update` derives the next version from that
// answer. A listing still behind a `create --version 7.0` therefore publishes
// 1.0: a version nobody asked for, with the sequence running backwards. The
// probe above does not cover this, because it only knows the versions a first
// publish can carry, so an explicitly versioned create is invisible to it.
//
// This narrows the window rather than closing it. An empty answer after the last
// attempt is still taken at face value, and the caller falls back to counting
// from nothing.
func settledLatestVersion(
	ctx context.Context,
	client *dataset_api.DatasetClient,
	name string,
) (string, error) {
	for attempt := range versionListingSettleAttempts {
		list, err := client.ListDatasetVersions(ctx, name, ProjectEndpointAPIVersion)
		switch {
		case err == nil && list != nil && len(list.Value) > 0:
			return dataset_api.LatestVersion(list.Value), nil
		case err != nil && dataset_api.IsNotFound(err):
			// The service naming the dataset unknown is an answer, not a lag.
			return "", nil
		case err != nil:
			// A read that failed proves nothing, and must not read as "no versions".
			return "", messages.CheckingDataset(name, err)
		}
		if attempt == versionListingSettleAttempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(versionListingSettleDelay):
		}
	}
	return "", nil
}

// newDatasetUpdateCommand builds `dataset update <name>`, which publishes a
// further version of one that does.
func newDatasetUpdateCommand() *cobra.Command {
	return newDatasetWriteCommand("update", "Publish a new version of a dataset.")
}

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

// newDatasetWriteCommand builds create and update. Both run the same upload,
// and the existence check is the only thing that separates them: a version is
// brought into being by startPendingUpload, which neither knows nor cares
// whether the name was already in use.
func newDatasetWriteCommand(verb, short string) *cobra.Command {
	flags := &datasetWriteFlags{}

	cmd := &cobra.Command{
		Use:   verb + " <name>",
		Short: short,
		Args:  cobra.ExactArgs(1),
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
	registerOutputFormats(cmd)
	return cmd
}

func (a *datasetWriteAction) Run() error {
	if !validAssetName(a.name) {
		return messages.InvalidDatasetName(a.name)
	}
	if a.flags.fromFile == "" {
		return requireFlag("from-file")
	}

	localDir, err := datasetUploadSource(a.flags.fromFile)
	if err != nil {
		return err
	}
	// Read the rows before the first network call, so a malformed file is
	// reported against the file rather than from behind whatever the service
	// happened to say first. The upload publishes what is read here rather than
	// reading it again.
	content, err := dataset_api.ReadFirstJSONLFile(localDir)
	if err != nil {
		return err
	}

	ctx := a.cmd.Context()
	ec, err := newDatasetContext(ctx, a.flags.endpointFlg)
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

	ds, err := a.publish(ctx, ec, content)
	if err != nil {
		return messages.RegisteringDataset(a.name, err)
	}

	if err := ec.setEnvValue(ctx, envKeyDatasetVersion, ds.Version); err != nil {
		// Persisting is a convenience, so this never fails the command. It goes
		// to stdout because azd does not surface an extension's stderr under
		// `azd up`, which is where a deploy would lose it. Skipped outside a
		// project, where having nowhere to persist is expected rather than
		// notable.
		if !errors.Is(err, errNoAzdEnvironment) && !isJSON(a.cmd) {
			fmt.Fprint(a.cmd.OutOrStdout(), messages.Warning(err))
		}
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
	ec *datasetContext,
	content string,
) (*dataset_api.Dataset, error) {
	if a.flags.version != "" {
		return ec.datasetClient.UploadVersion(
			ctx, a.name, a.flags.version, content, ProjectEndpointAPIVersion)
	}
	// `update` counts from the latest registered version, so the listing is
	// given a moment to settle first. `create` has nothing to count from and
	// does not wait.
	current := ""
	if a.verb == "update" {
		latest, err := settledLatestVersion(ctx, ec.datasetClient, a.name)
		if err != nil {
			return nil, err
		}
		current = latest
	}
	return ec.datasetClient.UploadNextVersion(
		ctx, a.name, current, content, ProjectEndpointAPIVersion)
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

// datasetListAction lists the project's datasets.
type datasetListAction struct {
	cmd      *cobra.Command
	endpoint string
}

func newDatasetListCommand() *cobra.Command {
	var endpointFlg string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the project's datasets.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return (&datasetListAction{cmd: cmd, endpoint: endpointFlg}).Run()
		},
	}

	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	registerOutputFormats(cmd)
	return cmd
}

func (a *datasetListAction) Run() error {
	ctx := a.cmd.Context()
	ec, err := newDatasetContext(ctx, a.endpoint)
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

// listVersionsForDisplay answers what `versions list` shows for a name.
//
// `ListDatasetVersions` reports an unknown name as a 404 error, so taking the
// error path here refused a name the command is supposed to answer for. This is
// a filter rather than a lookup: nothing matched is an empty result, which is
// what `-o json` callers range over and what a delete's idempotence check reads.
// `show` is the lookup, and still refuses.
func listVersionsForDisplay(
	ctx context.Context,
	client *dataset_api.DatasetClient,
	name string,
) (*dataset_api.DatasetList, error) {
	list, err := client.ListDatasetVersions(ctx, name, ProjectEndpointAPIVersion)
	switch {
	case err == nil:
		return list, nil
	case dataset_api.IsNotFound(err):
		return &dataset_api.DatasetList{}, nil
	default:
		return nil, messages.ListingDatasetVersions(name, err)
	}
}

// datasetVersionsListAction lists the versions of one dataset.
type datasetVersionsListAction struct {
	cmd      *cobra.Command
	endpoint string
	name     string
}

func newDatasetVersionsListCommand() *cobra.Command {
	var endpointFlg string

	cmd := &cobra.Command{
		Use:   "list <name>",
		Short: "List the versions of a dataset.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return (&datasetVersionsListAction{
				cmd: cmd, endpoint: endpointFlg, name: args[0],
			}).Run()
		},
	}

	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	registerOutputFormats(cmd)
	return cmd
}

func (a *datasetVersionsListAction) Run() error {
	if !validAssetName(a.name) {
		return messages.InvalidDatasetName(a.name)
	}

	ctx := a.cmd.Context()
	ec, err := newDatasetContext(ctx, a.endpoint)
	if err != nil {
		return err
	}
	defer ec.Close()

	list, err := listVersionsForDisplay(ctx, ec.datasetClient, a.name)
	if err != nil {
		return err
	}
	// An unknown name lists nothing and succeeds; it is not an error. `-o json`
	// callers range over the array, and a delete is checked for idempotence by
	// listing what is left. The empty sentence names the dataset, though: the
	// project may hold plenty of others, so "No datasets found." would be
	// answering a different question.
	return renderDatasets(a.cmd, list, messages.NoDatasetVersions(a.name))
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

// latestVersionForShow resolves the version `show` reads when none was named.
//
// A dataset cannot exist with no versions, so both shapes the service uses for
// an unknown name -- a 404, or a 200 with an empty list -- mean the same thing
// and get the same short sentence. Wrapping the 404 instead carried the whole
// HTTP response into the message for something one line explains.
func latestVersionForShow(
	ctx context.Context,
	client *dataset_api.DatasetClient,
	name string,
) (string, error) {
	list, err := client.ListDatasetVersions(ctx, name, ProjectEndpointAPIVersion)
	if err != nil {
		if dataset_api.IsNotFound(err) {
			return "", messages.DatasetNotFound(name)
		}
		return "", messages.ResolvingLatestDatasetVersion(name, err)
	}
	if list == nil || len(list.Value) == 0 {
		return "", messages.DatasetNotFound(name)
	}
	return dataset_api.LatestVersion(list.Value), nil
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
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return (&datasetShowAction{
				cmd: cmd, endpoint: endpointFlg, version: version, name: args[0],
			}).Run()
		},
	}

	cmd.Flags().StringVar(&version, "version", "", "Version to show. Omit for the latest.")
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	registerOutputFormats(cmd)
	return cmd
}

func (a *datasetShowAction) Run() error {
	if !validAssetName(a.name) {
		return messages.InvalidDatasetName(a.name)
	}

	ctx := a.cmd.Context()
	ec, err := newDatasetContext(ctx, a.endpoint)
	if err != nil {
		return err
	}
	defer ec.Close()

	version := a.version
	if version == "" {
		resolved, err := latestVersionForShow(ctx, ec.datasetClient, a.name)
		if err != nil {
			return err
		}
		version = resolved
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
	return emitDetail(a.cmd.OutOrStdout(), []field{
		{"Name", ds.Name},
		{"Version", ds.Version},
		{"Type", ds.Type},
		{"URI", ds.ResolvedBlobURI()},
	})
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
		endpointFlg string
		force       bool
	)

	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a dataset version.",
		Long: `Delete a dataset version from the resolved Foundry project.

The version is removed immediately and cannot be recovered. The CLI asks for
confirmation first; pass --force to skip the question. With --no-prompt, or
with JSON output, --force is required.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return (&datasetDeleteAction{
				cmd: cmd, endpoint: endpointFlg, version: version, force: force, name: args[0],
			}).Run()
		},
	}

	cmd.Flags().StringVar(&version, "version", "", "Version to delete.")
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	cmd.Flags().BoolVar(&force, "force", false, "Delete without asking for confirmation.")
	registerOutputFormats(cmd)
	return cmd
}

func (a *datasetDeleteAction) Run() error {
	if !validAssetName(a.name) {
		return messages.InvalidDatasetName(a.name)
	}
	if a.version == "" {
		return requireFlag("version")
	}

	ctx := a.cmd.Context()
	ec, err := newDatasetContext(ctx, a.endpoint)
	if err != nil {
		return err
	}
	defer ec.Close()

	goAhead, err := confirmDelete(a.cmd, ec, a.name, a.version, a.force)
	if err != nil {
		return err
	}
	if !goAhead {
		fmt.Fprint(a.cmd.OutOrStdout(), messages.DeleteCancelled(a.name, a.version))
		return nil
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

// confirmDelete asks before removing a version that cannot be recovered.
//
// The same contract the other Foundry delete commands use: ask by default, skip
// on --force, and refuse rather than assume when nobody can answer. A prompt
// written into a JSON document, or into a script running with --no-prompt, is a
// hang rather than a question, which is why those require the flag instead.
func confirmDelete(
	cmd *cobra.Command,
	ec *datasetContext,
	name, version string,
	force bool,
) (bool, error) {
	if force {
		return true, nil
	}
	if noPrompt(cmd) {
		return false, messages.DeleteNeedsForce(name, version)
	}

	defaultNo := false
	resp, err := ec.azdClient.Prompt().Confirm(cmd.Context(), &azdext.ConfirmRequest{
		Options: &azdext.ConfirmOptions{
			Message:      messages.ConfirmDeleteDataset(name, version),
			DefaultValue: &defaultNo,
		},
	})
	if err != nil {
		// A question that could not be drawn at all -- stdout redirected, no
		// console attached -- fails in the transport, and the caller cannot act
		// on a gRPC status. They can act on --force.
		if exterrors.IsPromptUnavailable(err) {
			return false, messages.DeleteNeedsForce(name, version)
		}
		return false, exterrors.FromPrompt(err, "confirming the delete")
	}
	return resp.GetValue(), nil
}
