// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/pkg/evalcore"

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
// An evaluator is either a rubric — a JSON file of weighted dimensions — or
// code — one self-contained Python script. They are different definition types
// on the wire, so exactly one of the two sources has to be named.
func newEvaluatorCreateCommand() *cobra.Command {
	var (
		name        string
		rubric      string
		file        string
		imageTag    string
		initParams  string
		dataSchema  string
		metrics     string
		endpointFlg string
	)

	use := "create"
	short := "Register a rubric or code evaluator, publishing a new version."
	long := short + "\n\n" +
		"A rubric (--rubric) is a JSON file of weighted dimensions.\n\n" +
		"A code evaluator (--file) is a single Python script declaring a top-level\n" +
		"grade(sample, item) function that returns a float. It runs as a python\n" +
		"grader, which is handed the script's source and nothing else: there is no\n" +
		"package and no import path, so a helper module beside the script cannot be\n" +
		"imported. Dependencies come from the image named by --image-tag."

	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Long:  long,
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return requireFlag("name")
			}
			flags := codeEvaluatorFlags{
				imageTag:   imageTag,
				initParams: initParams,
				dataSchema: dataSchema,
				metrics:    metrics,
				endpoint:   endpointFlg,
			}
			if err := validateEvaluatorSource(rubric, file, flags); err != nil {
				return err
			}

			ctx := cmd.Context()

			if file != "" {
				return runEvaluatorCreateFromFile(cmd, name, file, flags)
			}

			raw, err := os.ReadFile(rubric)
			if err != nil {
				return fmt.Errorf("reading rubric %q: %w", rubric, err)
			}

			body, err := normalizeRubricBody(name, raw)
			if err != nil {
				return fmt.Errorf("rubric %q: %w", rubric, err)
			}

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
	cmd.Flags().StringVar(&file, "file", "",
		"Path to a single Python script declaring a top-level grade(sample, item) function.")
	cmd.Flags().StringVar(&imageTag, "image-tag", "",
		"Container image the evaluator runs in. Its packages are the only "+
			"dependencies the script can import beyond the standard library.")
	cmd.Flags().StringVar(&initParams, "init-params", "",
		"Path to a JSON Schema for the evaluator's initialization parameters.")
	cmd.Flags().StringVar(&dataSchema, "data-schema", "",
		"Path to a JSON Schema for the evaluator's input data.")
	cmd.Flags().StringVar(&metrics, "metrics", "",
		"Path to a JSON object describing the metrics the evaluator produces.")
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

// codeEvaluatorFlags are the optional settings for a code evaluator.
type codeEvaluatorFlags struct {
	imageTag   string
	initParams string
	dataSchema string
	metrics    string
	endpoint   string
}

// validateEvaluatorSource enforces that exactly one source is named, and that
// the code-only settings are only used with the source they apply to.
//
// Deliberately checked here rather than with MarkFlagsMutuallyExclusive: that
// only rejects the "both" case, and its message names a flag group rather than
// saying what the two flags mean. Both mistakes deserve advice, and this is
// testable without driving cobra.
func validateEvaluatorSource(rubric, file string, flags codeEvaluatorFlags) error {
	switch {
	case rubric == "" && file == "":
		return fmt.Errorf(
			"one of --rubric or --file is required: --rubric takes a JSON file of " +
				"weighted dimensions, --file takes a single Python script")
	case rubric != "" && file != "":
		return fmt.Errorf(
			"--rubric and --file cannot be used together: an evaluator is either a " +
				"rubric or code, not both")
	}

	// A rubric's schemas are fixed by the service and a rubric runs no code, so
	// these would be accepted and then quietly dropped — the worst kind of
	// no-op, because the author believes the evaluator was published carrying
	// them.
	if file == "" {
		for _, named := range []struct {
			flag  string
			value string
		}{
			{"image-tag", flags.imageTag},
			{"init-params", flags.initParams},
			{"data-schema", flags.dataSchema},
			{"metrics", flags.metrics},
		} {
			if named.value != "" {
				return fmt.Errorf(
					"--%s applies to a code evaluator and needs --file; "+
						"a rubric runs no code and its schemas are set by the service",
					named.flag)
			}
		}
	}
	return nil
}

// runEvaluatorCreateFromFile validates the script, then publishes it.
func runEvaluatorCreateFromFile(
	cmd *cobra.Command,
	name string,
	file string,
	flags codeEvaluatorFlags,
) error {
	script, err := evalcore.LoadCodeEvaluator(name, file)
	if err != nil {
		return err
	}

	opts, err := codeEvaluatorOptions(flags)
	if err != nil {
		return err
	}

	ctx := cmd.Context()
	ec, err := newEvalContext(ctx, flags.endpoint)
	if err != nil {
		return err
	}
	defer ec.Close()

	created, err := ec.evalClient.CreateCodeEvaluatorVersion(
		ctx, script, opts, ProjectEndpointAPIVersion,
	)
	if err != nil {
		return fmt.Errorf("publishing evaluator %q: %w", name, err)
	}

	if isJSON(cmd) {
		return emitJSON(cmd.OutOrStdout(), created)
	}
	fmt.Fprintf(cmd.OutOrStdout(),
		"Published evaluator %s version %s from %s\n",
		created.Name, created.Version, file)
	return nil
}

// codeEvaluatorOptions resolves the evaluator's schemas from the flags.
//
// They are not read from the script and not read from a descriptor beside it:
// the grader is handed one file of source, so anything the service needs that
// is not Python has to be named on the command line or carried in the eval
// config.
func codeEvaluatorOptions(flags codeEvaluatorFlags) (eval_api.CodeEvaluatorOptions, error) {
	opts := eval_api.CodeEvaluatorOptions{ImageTag: flags.imageTag}

	for _, declared := range []struct {
		path  string
		flag  string
		field *json.RawMessage
	}{
		{flags.initParams, "init-params", &opts.InitParameters},
		{flags.dataSchema, "data-schema", &opts.DataSchema},
		{flags.metrics, "metrics", &opts.Metrics},
	} {
		if declared.path == "" {
			continue
		}
		raw, err := readJSONObject(declared.path)
		if err != nil {
			return opts, fmt.Errorf("--%s %q: %w", declared.flag, declared.path, err)
		}
		*declared.field = raw
	}

	return opts, nil
}

// readJSONObject reads a file that must hold a JSON object.
//
// Parsing here rather than letting the service reject it keeps a typo from
// costing an upload and a published version, and names the file that is wrong.
func readJSONObject(path string) (json.RawMessage, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("not a JSON object: %w", err)
	}
	return json.RawMessage(raw), nil
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
