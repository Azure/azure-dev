// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"fmt"
	"os"

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
		newEvaluatorListCommand(),
		newEvaluatorShowCommand(),
		newEvaluatorDeleteCommand(),
	)
	return cmd
}

// newEvaluatorCreateCommand builds `evaluator create`, named to match
// `dataset create`: both register an artifact and both publish a new immutable
// version every time, so there is nothing for a separate `update` to do.
//
// M1 supports rubric evaluators only. Code evaluators need a folder walk,
// multi-blob upload, and the Azure AI User role assignment, so they land later.
func newEvaluatorCreateCommand() *cobra.Command {
	var (
		name        string
		rubric      string
		endpointFlg string
	)

	use := "create"
	short := "Register a rubric evaluator, publishing a new version."

	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return requireFlag("name")
			}
			if rubric == "" {
				return requireFlag("rubric")
			}

			raw, err := os.ReadFile(rubric)
			if err != nil {
				return fmt.Errorf("reading rubric %q: %w", rubric, err)
			}

			body, err := normalizeRubricBody(name, raw)
			if err != nil {
				return fmt.Errorf("rubric %q: %w", rubric, err)
			}

			ctx := cmd.Context()
			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			created, err := ec.evalClient.CreateEvaluatorVersion(
				ctx, name, body, ProjectEndpointAPIVersion,
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

	cmd.Flags().StringVar(&name, "name", "", "Name of the evaluator.")
	cmd.Flags().StringVar(&rubric, "rubric", "", "Path to the rubric JSON file.")
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

// normalizeRubricBody accepts either a bare definition ({type, dimensions}) or
// a full evaluator document ({name, definition}) and returns the request body.
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

func normalizeRubricBody(name string, raw []byte) (json.RawMessage, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("not valid JSON: %w", err)
	}

	if definition, hasDefinition := probe["definition"]; hasDefinition {
		// Already a full document; make sure the name matches the flag.
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
		name        string
		builtin     bool
		endpointFlg string
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List evaluators, the versions of one evaluator, or the built-in evaluators.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			var list *eval_api.EvaluatorListResponse
			switch {
			case name != "":
				list, err = ec.evalClient.ListEvaluatorVersions(ctx, name, ProjectEndpointAPIVersion)
			case builtin:
				// The service filters by type, and asking for nothing returns
				// only the project's own evaluators.
				list, err = ec.evalClient.ListEvaluators(
					ctx, eval_api.EvaluatorTypeBuiltin, ProjectEndpointAPIVersion)
			default:
				list, err = ec.evalClient.ListEvaluators(ctx, "", ProjectEndpointAPIVersion)
			}
			if err != nil {
				return fmt.Errorf("listing evaluators: %w", err)
			}
			return renderEvaluators(cmd, list)
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Limit the listing to versions of this evaluator.")
	cmd.Flags().BoolVar(&builtin, "builtin", false, "List the built-in evaluators instead of the project's own.")
	cmd.MarkFlagsMutuallyExclusive("name", "builtin")
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
		name        string
		version     string
		endpointFlg string
	)

	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show an evaluator definition.",
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

			raw, err := ec.evalClient.GetEvaluatorRaw(ctx, name, version, ProjectEndpointAPIVersion)
			if err != nil {
				return fmt.Errorf("reading evaluator %q: %w", name, err)
			}

			var pretty any
			if err := json.Unmarshal(raw, &pretty); err != nil {
				fmt.Fprintln(cmd.OutOrStdout(), string(raw))
				return nil
			}
			return emitJSON(cmd.OutOrStdout(), pretty)
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Name of the evaluator.")
	cmd.Flags().StringVar(&version, "version", "", "Version to show. Omit for the latest.")
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

func newEvaluatorDeleteCommand() *cobra.Command {
	var (
		name        string
		version     string
		endpointFlg string
	)

	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete an evaluator version.",
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

			if err := ec.evalClient.DeleteEvaluatorVersion(
				ctx, name, version, ProjectEndpointAPIVersion,
			); err != nil {
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

	cmd.Flags().StringVar(&name, "name", "", "Name of the evaluator.")
	cmd.Flags().StringVar(&version, "version", "", "Version to delete.")
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}
