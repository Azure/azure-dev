// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"azureaieval/internal/pkg/eval_api"

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
		newEvaluatorGenerateCommand(),
		newEvaluatorListCommand(),
		newEvaluatorShowCommand(),
		newEvaluatorDeleteCommand(),
		newEvaluatorVersionsCommand(),
		newJobCommand(evaluatorJobs),
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
			if fromFile == "" {
				return requireFlag("from-file")
			}

			raw, err := os.ReadFile(fromFile)
			if err != nil {
				return fmt.Errorf("reading evaluator %q: %w", fromFile, err)
			}

			body, err := normalizeRubricBody(name, raw)
			if err != nil {
				return fmt.Errorf("evaluator %q: %w", fromFile, err)
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
				return fmt.Errorf("checking whether evaluator %q exists: %w", name, readErr)
			}
			if err := checkAssetExistence(verb, "evaluator", name, readErr == nil); err != nil {
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
				return fmt.Errorf("registering evaluator %q: %w", name, err)
			}

			if isJSON(cmd) {
				return emitJSON(cmd.OutOrStdout(), created)
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"Registered evaluator %s version %s\n", created.Name, created.Version)
			return nil
		},
	}

	cmd.Flags().StringVar(&fromFile, "from-file", "", "Path to the evaluator JSON file.")
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

// checkAssetExistence enforces the one difference between create and update.
func checkAssetExistence(verb, kind, name string, exists bool) error {
	switch {
	case verb == "create" && exists:
		return fmt.Errorf(
			"%s %q already exists: use `update` to publish a new version", kind, name)
	case verb == "update" && !exists:
		return fmt.Errorf(
			"%s %q does not exist: use `create` to register it", kind, name)
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
		return nil, fmt.Errorf("the definition is not a JSON object: %w", err)
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
		return nil, fmt.Errorf("not valid JSON: %w", err)
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
		return nil, fmt.Errorf(
			"expected a rubric definition with 'dimensions', or a document with 'definition'")
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
				return fmt.Errorf("listing evaluators: %w", err)
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

			ctx := cmd.Context()
			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			list, err := ec.evalClient.ListEvaluatorVersions(ctx, name, ProjectEndpointAPIVersion)
			if err != nil {
				return fmt.Errorf("listing versions of evaluator %q: %w", name, err)
			}
			return renderEvaluators(cmd, list)
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
		fmt.Fprintln(cmd.OutOrStdout(), "No evaluators found.")
		return nil
	}
	rows := make([][]string, 0, len(list.Value))
	for _, e := range list.Value {
		rows = append(rows, []string{e.Name, e.Version, e.Type()})
	}
	return emitTable(cmd.OutOrStdout(), []string{"NAME", "VERSION", "TYPE"}, rows)
}

func newEvaluatorShowCommand() *cobra.Command {
	var (
		version     string
		endpointFlg string
	)

	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show an evaluator definition.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			ctx := cmd.Context()
			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			raw, err := ec.evalClient.GetEvaluatorRaw(ctx, name, version, ProjectEndpointAPIVersion)
			if err != nil {
				if eval_api.IsNotFound(err) {
					return fmt.Errorf(
						"no evaluator %q in this project; "+
							"`azd ai eval evaluator list` shows the ones there are", name)
				}
				return fmt.Errorf("reading evaluator %q: %w", name, err)
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
		endpointFlg string
	)

	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete an evaluator version.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if version == "" {
				return requireFlag("version")
			}

			ctx := cmd.Context()
			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			if err := ec.evalClient.DeleteEvaluatorVersion(
				ctx, name, version, ProjectEndpointAPIVersion,
			); err != nil {
				if eval_api.IsNotFound(err) {
					return fmt.Errorf(
						"no evaluator %q at version %q in this project", name, version)
				}
				return fmt.Errorf("deleting evaluator %q version %q: %w", name, version, err)
			}

			if isJSON(cmd) {
				return emitJSON(cmd.OutOrStdout(), map[string]string{
					"name": name, "version": version, "status": "deleted",
				})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted evaluator %s version %s\n", name, version)
			return nil
		},
	}

	cmd.Flags().StringVar(&version, "version", "", "Version to delete.")
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}
