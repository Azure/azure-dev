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

// newEvaluatorWriteCommand builds create and update, which send the same
// request and differ only in which starting state they accept. The service has
// one route for both and assigns the version either way, so the existence check
// is ours: without it, `create` on a name already in use would silently publish
// a further version of someone else's evaluator.
func newEvaluatorWriteCommand(verb, short string) *cobra.Command {
	var (
		fromFile    string
		endpointFlg string
	)

	cmd := &cobra.Command{
		Use:   verb + " <name>",
		Short: short,
		Long: short + "\n\n" +
			"An evaluator is a rubric: a JSON file of weighted scoring dimensions.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if !validAssetName(name) {
				return messages.InvalidEvaluatorName(name)
			}
			if fromFile == "" {
				return requireFlag("from-file")
			}

			raw, err := project.ReadFileNoBOM(fromFile)
			if err != nil {
				return messages.ReadingEvaluator(fromFile, err)
			}

			body, err := normalizeRubricBody(name, raw)
			if err != nil {
				return messages.EvaluatorProblem(fromFile, err)
			}

			ctx := cmd.Context()
			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			// Asked of the direct read, not the version listing. The listing
			// lags a publish by up to a second and a half, so an update
			// issued straight after a create would be told the evaluator it
			// just made does not exist.
			existing, readErr := ec.evalClient.GetEvaluatorRaw(
				ctx, name, "", ProjectEndpointAPIVersion,
			)
			if readErr != nil && !eval_api.IsNotFound(readErr) {
				return messages.CheckingEvaluatorExists(name, readErr)
			}
			// A non-404 already returned above, so reaching here means the read
			// either found the evaluator or the service said it is unknown.
			if err := checkAssetExistence(verb, "evaluator", name, readErr == nil, true); err != nil {
				return err
			}

			// What that read saw is what keeps the publish from being
			// answered with the same version and replacing it.
			if readErr != nil {
				existing = nil
			}

			created, err := ec.evalClient.CreateEvaluatorVersion(
				ctx, name, body, existing, ProjectEndpointAPIVersion,
			)
			if err != nil {
				return messages.RegisteringEvaluator(name, err)
			}

			if isJSON(cmd) {
				return emitJSON(cmd.OutOrStdout(), created)
			}
			fmt.Fprint(cmd.OutOrStdout(),
				messages.EvaluatorRegistered(created.Name, created.Version))
			return nil
		},
	}

	cmd.Flags().StringVar(&fromFile, "from-file", "", "Path to the evaluator JSON file.")
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
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

func newEvaluatorListCommand() *cobra.Command {
	var (
		builtin     bool
		endpointFlg string
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the project's evaluators, or the built-in ones.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			// The service filters by type, and asking for nothing returns only
			// the project's own evaluators.
			filter := ""
			if builtin {
				filter = eval_api.EvaluatorTypeBuiltin
			}
			list, err := ec.evalClient.ListEvaluators(ctx, filter, ProjectEndpointAPIVersion)
			if err != nil {
				return messages.ListingEvaluators(err)
			}
			return renderEvaluators(cmd, list)
		},
	}

	cmd.Flags().BoolVar(&builtin, "builtin", false,
		"List the built-in evaluators instead of the project's own.")
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
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

func newEvaluatorVersionsListCommand() *cobra.Command {
	var endpointFlg string

	cmd := &cobra.Command{
		Use:   "list <name>",
		Short: "List the versions of an evaluator.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if !validAssetName(name) {
				return messages.InvalidEvaluatorName(name)
			}

			ctx := cmd.Context()
			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			list, err := ec.evalClient.ListEvaluatorVersions(ctx, name, ProjectEndpointAPIVersion)
			if err != nil {
				// A name nobody published is the ordinary way to get here, and
				// it does not need the whole 404 body to explain it.
				if eval_api.IsNotFound(err) {
					return messages.EvaluatorNotFound(name)
				}
				return messages.ListingEvaluatorVersions(name, err)
			}
			return renderEvaluatorVersions(cmd, list)
		},
	}

	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

func renderEvaluators(cmd *cobra.Command, list *eval_api.EvaluatorListResponse) error {
	if isJSON(cmd) {
		return emitJSONList(cmd.OutOrStdout(), list.Value)
	}
	if len(list.Value) == 0 {
		fmt.Fprint(cmd.OutOrStdout(), messages.NoEvaluators())
		return nil
	}
	rows := make([][]string, 0, len(list.Value))
	for _, e := range list.Value {
		rows = append(rows, []string{e.Name, e.Version, e.Type()})
	}
	return emitTable(cmd.OutOrStdout(), []string{"NAME", "VERSION", "TYPE"}, rows)
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
	rows := make([][]string, 0, len(list.Value))
	for _, e := range list.Value {
		rows = append(rows, []string{
			e.Version,
			timestampString(e.CreatedAt),
			evaluatorPassThreshold(&e),
			e.Description,
		})
	}
	return emitTable(cmd.OutOrStdout(),
		[]string{"VERSION", "CREATED AT", "PASS THRESHOLD", "DESCRIPTION"}, rows)
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
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if !validAssetName(name) {
				return messages.InvalidEvaluatorName(name)
			}

			ctx := cmd.Context()
			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			raw, err := ec.evalClient.GetEvaluatorRaw(ctx, name, version, ProjectEndpointAPIVersion)
			if err != nil {
				if eval_api.IsNotFound(err) {
					return messages.EvaluatorNotFound(name)
				}
				return messages.ReadingEvaluator(name, err)
			}

			// --output-file writes the service's document verbatim, because
			// reconciliation points here to adopt a remote change over the local
			// definition: anything this view dropped would be lost on adoption.
			if outFile != "" {
				body := raw
				var pretty any
				if err := json.Unmarshal(raw, &pretty); err == nil {
					if indented, err := json.MarshalIndent(pretty, "", "  "); err == nil {
						body = append(indented, '\n')
					}
				}
				if err := writeFileAtomic(outFile, body); err != nil {
					return err
				}
				if !isJSON(cmd) {
					fmt.Fprint(cmd.OutOrStdout(), messages.WroteArtifact(outFile))
				}
				return nil
			}

			// -o json answers with the service's document untouched, because a
			// caller asking for JSON wants the evaluator, not this view of it.
			if isJSON(cmd) {
				var pretty any
				if err := json.Unmarshal(raw, &pretty); err != nil {
					fmt.Fprintln(cmd.OutOrStdout(), string(raw))
					return nil
				}
				return emitJSON(cmd.OutOrStdout(), pretty)
			}

			var summary eval_api.EvaluatorSummary
			if err := json.Unmarshal(raw, &summary); err != nil {
				// An evaluator shaped in a way this view cannot read is still
				// worth showing; falling back beats refusing to print it.
				fmt.Fprintln(cmd.OutOrStdout(), string(raw))
				return nil
			}
			return ec.renderEvaluator(ctx, cmd.OutOrStdout(), &summary)
		},
	}

	cmd.Flags().StringVar(&version, "version", "", "Version to show. Omit for the latest.")
	cmd.Flags().StringVar(&outFile, "output-file", "",
		"Write the evaluator document to this path instead of stdout.")
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
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
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if !validAssetName(name) {
				return messages.InvalidEvaluatorName(name)
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

			subject := fmt.Sprintf("evaluator %s version %s", name, version)
			goAhead, err := confirmDelete(cmd, ec, subject, force)
			if err != nil {
				return err
			}
			if !goAhead {
				return deleteDeclined(cmd, subject)
			}

			if err := ec.evalClient.DeleteEvaluatorVersion(
				ctx, name, version, ProjectEndpointAPIVersion,
			); err != nil {
				if eval_api.IsNotFound(err) {
					return messages.EvaluatorVersionNotFound(name, version)
				}
				return messages.DeletingEvaluatorVersion(name, version, err)
			}

			if isJSON(cmd) {
				return emitJSON(cmd.OutOrStdout(), map[string]string{
					"name": name, "version": version, "status": "deleted",
				})
			}
			fmt.Fprint(cmd.OutOrStdout(), messages.EvaluatorDeleted(name, version))
			return nil
		},
	}

	cmd.Flags().StringVar(&version, "version", "", "Version to delete.")
	registerForceFlag(cmd, &force)
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}
