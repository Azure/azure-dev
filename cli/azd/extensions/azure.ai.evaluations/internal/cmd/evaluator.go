// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"

	"github.com/spf13/cobra"
)

func newEvaluatorCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "evaluator",
		Short: "Manage custom evaluators.",
	}
	cmd.AddCommand(
		newEvaluatorCreateCommand(),
		newEvaluatorUpdateCommand(),
		newEvaluatorListCommand(),
		newEvaluatorShowCommand(),
		newEvaluatorDeleteCommand(),
		newEvaluatorVersionsCommand(),
	)
	return cmd
}

// newEvaluatorCreateCommand builds `evaluator create <name>`, which registers
// an evaluator that does not exist yet.
func newEvaluatorCreateCommand() *cobra.Command {
	return newEvaluatorWriteCommand("create", "Register an evaluator, publishing its first version.")
}

// newEvaluatorUpdateCommand builds `evaluator update <name>`, which publishes a
// further version of one that does.
func newEvaluatorUpdateCommand() *cobra.Command {
	return newEvaluatorWriteCommand("update", "Publish a new version of an evaluator.")
}

// evaluatorWriteFlags are the flags create and update share.
type evaluatorWriteFlags struct {
	fromFile    string
	endpointFlg string
}

// evaluatorWriteAction publishes an evaluator version.
type evaluatorWriteAction struct {
	cmd   *cobra.Command
	flags *evaluatorWriteFlags
	verb  string
	name  string
}

// newEvaluatorWriteCommand builds create and update, which send the same
// request and differ only in which starting state they accept. The service has
// one route for both and assigns the version either way, so the existence check
// is ours: without it, `create` on a name already in use would silently publish
// a further version of someone else's evaluator.
func newEvaluatorWriteCommand(verb, short string) *cobra.Command {
	flags := &evaluatorWriteFlags{}

	cmd := &cobra.Command{
		Use:   verb + " <name>",
		Short: short,
		Long: short + "\n\n" +
			"An evaluator is a rubric: a JSON file of weighted scoring dimensions.",
		Args: requiredArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return (&evaluatorWriteAction{
				cmd: cmd, flags: flags, verb: verb, name: args[0],
			}).Run()
		},
	}

	cmd.Flags().StringVar(&flags.fromFile, "from-file", "", "Path to the evaluator JSON file.")
	cmd.Flags().StringVar(&flags.endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

func (a *evaluatorWriteAction) Run() error {
	if !validAssetName(a.name) {
		return messages.InvalidEvaluatorName(a.name)
	}
	if a.flags.fromFile == "" {
		return requireFlag("from-file")
	}

	raw, err := project.ReadFileNoBOM(a.flags.fromFile)
	if err != nil {
		return messages.ReadingEvaluator(a.flags.fromFile, err)
	}

	body, err := normalizeRubricBody(a.name, raw)
	if err != nil {
		return messages.EvaluatorProblem(a.flags.fromFile, err)
	}

	ctx := a.cmd.Context()
	ec, err := newEvalContext(ctx, a.flags.endpointFlg)
	if err != nil {
		return err
	}
	defer ec.Close()

	// Asked of the direct read, not the version listing. The listing lags a
	// publish by up to a second and a half, so an update issued straight after a
	// create would be told the evaluator it just made does not exist.
	existing, readErr := ec.evalClient.GetEvaluatorRaw(ctx, a.name, "", ProjectEndpointAPIVersion)
	if readErr != nil && !eval_api.IsNotFound(readErr) {
		return messages.CheckingEvaluatorExists(a.name, readErr)
	}
	// A non-404 already returned above, so reaching here means the read either
	// found the evaluator or the service said it is unknown.
	if err := checkAssetExistence(a.verb, "evaluator", a.name, readErr == nil, true); err != nil {
		return err
	}

	// What that read saw is what keeps the publish from being answered with the
	// same version and replacing it.
	if readErr != nil {
		existing = nil
	}

	created, err := ec.evalClient.CreateEvaluatorVersion(
		ctx, a.name, body, existing, ProjectEndpointAPIVersion,
	)
	if err != nil {
		return messages.RegisteringEvaluator(a.name, err)
	}

	if isJSON(a.cmd) {
		return emitJSON(a.cmd.OutOrStdout(), created)
	}
	fmt.Fprint(a.cmd.OutOrStdout(), messages.EvaluatorRegistered(created.Name, created.Version))
	return nil
}

// checkAssetExistence enforces the one difference between create and update.
//
// absenceCertain separates "the service says this name is unknown" from "nothing
// came back", and only the former refuses an update. A caller reading an
// eventually consistent version listing cannot prove absence, so an update
// issued moments after a create would otherwise be refused for a dataset that
// plainly exists -- and sent to `create`, which fails in turn once the listing
// catches up. Callers that read a point endpoint can prove it and pass true.
func checkAssetExistence(verb, kind, name string, exists, absenceCertain bool) error {
	switch {
	case verb == "create" && exists:
		return messages.AssetAlreadyExists(kind, name)
	case verb == "update" && !exists && absenceCertain:
		return messages.AssetDoesNotExist(kind, name)
	}
	return nil
}

// rubricDefinitionType is the discriminator the service uses to deserialize a
// rubric definition.
const rubricDefinitionType = "rubric"

// ensureDefinitionType adds the type discriminator when a definition omits it.
//
// Without it the service cannot tell which definition kind it is holding and
// rejects the whole request with "The request field is required", which points
// at the wrong field entirely. Generated rubrics carry the type; hand-authored
// ones written to the shape the spec documents — a bare list of weighted
// dimensions — do not.
func ensureDefinitionType(definition json.RawMessage) (json.RawMessage, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(definition, &doc); err != nil {
		return nil, messages.DefinitionNotJSONObject(err)
	}
	if _, ok := doc["type"]; ok {
		return definition, nil
	}
	doc["type"] = json.RawMessage(fmt.Sprintf("%q", rubricDefinitionType))
	return json.Marshal(doc)
}

// normalizeRubricBody accepts either a bare definition ({type, dimensions}) or
// a full evaluator document ({name, definition}) and returns the request body.
func normalizeRubricBody(name string, raw []byte) (json.RawMessage, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, messages.NotValidJSON(err)
	}

	if definition, hasDefinition := probe["definition"]; hasDefinition {
		// Already a full document; make sure the name matches the argument.
		typed, err := ensureDefinitionType(definition)
		if err != nil {
			return nil, err
		}
		probe["definition"] = typed
		probe["name"] = json.RawMessage(fmt.Sprintf("%q", name))
		out, err := json.Marshal(probe)
		if err != nil {
			return nil, err
		}
		return out, nil
	}

	if _, hasDimensions := probe["dimensions"]; !hasDimensions {
		return nil, messages.RubricMissingDimensions()
	}

	typed, err := ensureDefinitionType(raw)
	if err != nil {
		return nil, err
	}
	doc := map[string]any{
		"name":       name,
		"definition": typed,
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// evaluatorListAction lists the project's evaluators, or the built-in ones.
type evaluatorListAction struct {
	cmd      *cobra.Command
	endpoint string
	builtin  bool
}

func newEvaluatorListCommand() *cobra.Command {
	var (
		displayLimit int
		showAll      bool
		builtin      bool
		endpointFlg  string
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the project's evaluators, or the built-in ones.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return (&evaluatorListAction{
				cmd: cmd, endpoint: endpointFlg, builtin: builtin,
			}).Run()
		},
	}

	cmd.Flags().BoolVar(&builtin, "builtin", false,
		"List the built-in evaluators instead of the project's own.")
	addDisplayPagingFlags(cmd, &displayLimit, &showAll, defaultPageSize)
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

func (a *evaluatorListAction) Run() error {
	ctx := a.cmd.Context()
	ec, err := newEvalContext(ctx, a.endpoint)
	if err != nil {
		return err
	}
	defer ec.Close()

	// The service filters by type, and asking for nothing returns only the
	// project's own evaluators.
	filter := ""
	if a.builtin {
		filter = eval_api.EvaluatorTypeBuiltin
	}
	list, err := ec.evalClient.ListEvaluators(ctx, filter, ProjectEndpointAPIVersion)
	if err != nil {
		return messages.ListingEvaluators(err)
	}
	return renderEvaluators(a.cmd, list)
}

// newEvaluatorVersionsCommand groups the version listing, so that `list` means
// the same thing for evaluators as it does for datasets: the assets, not their
// history.
func newEvaluatorVersionsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "versions",
		Short: "Inspect the versions of one evaluator.",
	}
	cmd.AddCommand(newEvaluatorVersionsListCommand())
	return cmd
}

// evaluatorVersionsListAction lists the versions of one evaluator.
type evaluatorVersionsListAction struct {
	cmd      *cobra.Command
	endpoint string
	name     string
}

func newEvaluatorVersionsListCommand() *cobra.Command {
	var endpointFlg string
	var displayLimit int
	var showAll bool

	cmd := &cobra.Command{
		Use:   "list <name>",
		Short: "List the versions of an evaluator.",
		Args:  requiredArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return (&evaluatorVersionsListAction{
				cmd: cmd, endpoint: endpointFlg, name: args[0],
			}).Run()
		},
	}

	addDisplayPagingFlags(cmd, &displayLimit, &showAll, defaultPageSize)
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

func (a *evaluatorVersionsListAction) Run() error {
	if !validLookupName(a.name) {
		return messages.InvalidEvaluatorName(a.name)
	}

	ctx := a.cmd.Context()
	ec, err := newEvalContext(ctx, a.endpoint)
	if err != nil {
		return err
	}
	defer ec.Close()

	list, err := ec.evalClient.ListEvaluatorVersions(ctx, a.name, ProjectEndpointAPIVersion)
	if err != nil {
		// A name nobody published is the ordinary way to get here, and it does
		// not need the whole 404 body to explain it.
		if eval_api.IsNotFound(err) {
			return messages.EvaluatorNotFound(a.name)
		}
		return messages.ListingEvaluatorVersions(a.name, err)
	}
	return renderEvaluatorVersions(a.cmd, list)
}

func renderEvaluators(cmd *cobra.Command, list *eval_api.EvaluatorListResponse) error {
	if isJSON(cmd) {
		return emitJSONList(cmd.OutOrStdout(), list.Value)
	}
	if len(list.Value) == 0 {
		fmt.Fprint(cmd.OutOrStdout(), messages.NoEvaluators())
		return nil
	}
	shown, total, trimmed := trimForDisplay(cmd, list.Value)
	rows := make([][]string, 0, len(shown))
	for _, e := range shown {
		rows = append(rows, []string{e.Name, e.Version, e.Type()})
	}
	if err := emitTable(cmd.OutOrStdout(), []string{"NAME", "VERSION", "TYPE"}, rows); err != nil {
		return err
	}
	if trimmed {
		fmt.Fprint(cmd.OutOrStdout(), messages.ShowingSomeOf(len(rows), total))
	}
	return nil
}

// evaluatorPassThreshold renders the rubric's pass mark for a table cell,
// empty when the evaluator does not carry one.
//
// Two decimals because the scale is normalized 0.0-1.0, where the difference
// between 0.7 and 0.75 is a real change in what passes.
func evaluatorPassThreshold(e *eval_api.EvaluatorSummary) string {
	if e == nil || e.Definition == nil || e.Definition.PassThreshold == nil {
		return ""
	}
	return strconv.FormatFloat(*e.Definition.PassThreshold, 'f', 2, 64)
}

// renderEvaluatorVersions lists one evaluator's history.
//
// Name and type are the same on every row here, so they say nothing. What the
// scenario reads a version list for is how the rubric changed, which is the
// date, the pass mark and the description the author left.
func renderEvaluatorVersions(cmd *cobra.Command, list *eval_api.EvaluatorListResponse) error {
	if isJSON(cmd) {
		return emitJSONList(cmd.OutOrStdout(), list.Value)
	}
	if len(list.Value) == 0 {
		fmt.Fprint(cmd.OutOrStdout(), messages.NoEvaluators())
		return nil
	}
	shown, total, trimmed := trimForDisplay(cmd, list.Value)
	rows := make([][]string, 0, len(shown))
	for _, e := range shown {
		rows = append(rows, []string{
			e.Version,
			timestampString(e.CreatedAt),
			evaluatorPassThreshold(&e),
			e.Description,
		})
	}
	if err := emitTable(cmd.OutOrStdout(),
		[]string{"VERSION", "CREATED AT", "PASS THRESHOLD", "DESCRIPTION"}, rows); err != nil {
		return err
	}
	if trimmed {
		fmt.Fprint(cmd.OutOrStdout(), messages.ShowingSomeOf(len(rows), total))
	}
	return nil
}

// evaluatorShowAction reads one evaluator definition.
type evaluatorShowAction struct {
	cmd      *cobra.Command
	endpoint string
	version  string
	outFile  string
	name     string
}

func newEvaluatorShowCommand() *cobra.Command {
	var (
		version     string
		outFile     string
		endpointFlg string
	)

	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show an evaluator definition.",
		Args:  requiredArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return (&evaluatorShowAction{
				cmd: cmd, endpoint: endpointFlg, version: version, outFile: outFile, name: args[0],
			}).Run()
		},
	}

	cmd.Flags().StringVar(&version, "version", "", "Version to show. Omit for the latest.")
	cmd.Flags().StringVar(&outFile, "output-file", "",
		"Write the evaluator document to this path instead of stdout.")
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

func (a *evaluatorShowAction) Run() error {
	if !validLookupName(a.name) {
		return messages.InvalidEvaluatorName(a.name)
	}

	ctx := a.cmd.Context()
	ec, err := newEvalContext(ctx, a.endpoint)
	if err != nil {
		return err
	}
	defer ec.Close()

	raw, err := ec.evalClient.GetEvaluatorRaw(ctx, a.name, a.version, ProjectEndpointAPIVersion)
	if err != nil {
		if eval_api.IsNotFound(err) {
			return messages.EvaluatorNotFound(a.name)
		}
		return messages.ReadingEvaluator(a.name, err)
	}

	if a.outFile != "" {
		return a.writeDocument(raw)
	}

	// -o json answers with the service's document untouched, because a caller
	// asking for JSON wants the evaluator, not this view of it.
	if isJSON(a.cmd) {
		var pretty any
		if err := json.Unmarshal(raw, &pretty); err != nil {
			fmt.Fprintln(a.cmd.OutOrStdout(), string(raw))
			return nil
		}
		return emitJSON(a.cmd.OutOrStdout(), pretty)
	}

	var summary eval_api.EvaluatorSummary
	if err := json.Unmarshal(raw, &summary); err != nil {
		// An evaluator shaped in a way this view cannot read is still worth
		// showing; falling back beats refusing to print it.
		fmt.Fprintln(a.cmd.OutOrStdout(), string(raw))
		return nil
	}
	return ec.renderEvaluator(ctx, a.cmd.OutOrStdout(), &summary)
}

// writeDocument writes the service's document verbatim.
//
// Reconciliation points --output-file here to adopt a remote change over the
// local definition, so anything the detail view dropped would be lost on
// adoption. Only the indentation is this command's.
func (a *evaluatorShowAction) writeDocument(raw []byte) error {
	body := raw
	var pretty any
	if err := json.Unmarshal(raw, &pretty); err == nil {
		if indented, err := json.MarshalIndent(pretty, "", "  "); err == nil {
			body = append(indented, '\n')
		}
	}
	if err := writeFileAtomic(a.outFile, body); err != nil {
		return err
	}
	if !isJSON(a.cmd) {
		fmt.Fprint(a.cmd.OutOrStdout(), messages.WroteArtifact(a.outFile))
	}
	return nil
}

// renderEvaluator prints the detail view for one evaluator, closing with its
// portal link.
//
// The columns an evaluator is identified by, then what it grades and where it
// can run. The full definition is in `-o json`; a schema printed here would
// bury the four lines a reader came for.
func (ec *evalContext) renderEvaluator(
	ctx context.Context,
	out io.Writer,
	e *eval_api.EvaluatorSummary,
) error {
	if err := emitDetail(out, []field{
		{"Name", e.Name},
		{"Version", e.Version},
		{"Type", e.Type()},
		{"Pass Threshold", evaluatorPassThreshold(e)},
		{"Description", e.Description},
		{"Categories", strings.Join(e.Categories, ", ")},
		{"Evaluation Levels", strings.Join(e.SupportedEvaluationLevels, ", ")},
	}); err != nil {
		return err
	}
	if prefix := ec.portalPrefix(ctx); prefix != nil && e.Name != "" {
		writePortalLink(out, prefix.EvaluatorURL(e.Name, e.Version))
	}
	return nil
}

// evaluatorDeleteAction removes one evaluator version.
type evaluatorDeleteAction struct {
	cmd      *cobra.Command
	endpoint string
	version  string
	force    bool
	name     string
}

func newEvaluatorDeleteCommand() *cobra.Command {
	var (
		version     string
		force       bool
		endpointFlg string
	)

	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete an evaluator version.",
		Long: "Delete an evaluator version.\n\n" +
			"Asks before removing it. With --no-prompt, or with JSON output, " +
			"--force is required.",
		Args: requiredArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return (&evaluatorDeleteAction{
				cmd: cmd, endpoint: endpointFlg, version: version, force: force, name: args[0],
			}).Run()
		},
	}

	cmd.Flags().StringVar(&version, "version", "", "Version to delete.")
	registerForceFlag(cmd, &force)
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

func (a *evaluatorDeleteAction) Run() error {
	if !validLookupName(a.name) {
		return messages.InvalidEvaluatorName(a.name)
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

	subject := fmt.Sprintf("evaluator %s version %s", a.name, a.version)
	goAhead, err := confirmDelete(a.cmd, ec, subject, a.force)
	if err != nil {
		return err
	}
	if !goAhead {
		return deleteDeclined(a.cmd, subject)
	}

	if err := ec.evalClient.DeleteEvaluatorVersion(
		ctx, a.name, a.version, ProjectEndpointAPIVersion,
	); err != nil {
		if eval_api.IsNotFound(err) {
			return messages.EvaluatorVersionNotFound(a.name, a.version)
		}
		return messages.DeletingEvaluatorVersion(a.name, a.version, err)
	}

	if isJSON(a.cmd) {
		return emitJSON(a.cmd.OutOrStdout(), map[string]string{
			"name": a.name, "version": a.version, "status": "deleted",
		})
	}
	fmt.Fprint(a.cmd.OutOrStdout(), messages.EvaluatorDeleted(a.name, a.version))
	return nil
}
