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
// code — a folder of Python. They are different definition types on the wire,
// so exactly one of the two sources has to be named.
func newEvaluatorCreateCommand() *cobra.Command {
	var (
		name        string
		rubric      string
		folder      string
		initParams  string
		dataSchema  string
		metrics     string
		endpointFlg string
	)

	use := "create"
	short := "Register a rubric or code evaluator, publishing a new version."

	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return requireFlag("name")
			}
			flags := codeEvaluatorFlags{
				initParams: initParams,
				dataSchema: dataSchema,
				metrics:    metrics,
				endpoint:   endpointFlg,
			}
			if err := validateEvaluatorSource(rubric, folder, flags); err != nil {
				return err
			}

			ctx := cmd.Context()

			if folder != "" {
				return runEvaluatorCreateFromFolder(cmd, name, folder, flags)
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
	cmd.Flags().StringVar(&folder, "folder", "",
		"Path to a folder of Python holding the evaluator code.")
	cmd.Flags().StringVar(&initParams, "init-params", "",
		"Path to a JSON Schema for the evaluator's initialization parameters. "+
			"Overrides the folder's "+evalcore.CodeEvaluatorMetadataFile+".")
	cmd.Flags().StringVar(&dataSchema, "data-schema", "",
		"Path to a JSON Schema for the evaluator's input data. "+
			"Overrides the folder's "+evalcore.CodeEvaluatorMetadataFile+".")
	cmd.Flags().StringVar(&metrics, "metrics", "",
		"Path to a JSON object describing the metrics the evaluator produces. "+
			"Overrides the folder's "+evalcore.CodeEvaluatorMetadataFile+".")
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

// codeEvaluatorFlags are the optional overrides for a code evaluator.
type codeEvaluatorFlags struct {
	initParams string
	dataSchema string
	metrics    string
	endpoint   string
}

// validateEvaluatorSource enforces that exactly one source is named, and that
// the schema overrides are only used with the source they apply to.
//
// Deliberately checked here rather than with MarkFlagsMutuallyExclusive: that
// only rejects the "both" case, and its message names a flag group rather than
// saying what the two flags mean. Both mistakes deserve advice, and this is
// testable without driving cobra.
func validateEvaluatorSource(rubric, folder string, flags codeEvaluatorFlags) error {
	switch {
	case rubric == "" && folder == "":
		return fmt.Errorf(
			"one of --rubric or --folder is required: --rubric takes a JSON file of " +
				"weighted dimensions, --folder takes a directory of Python")
	case rubric != "" && folder != "":
		return fmt.Errorf(
			"--rubric and --folder cannot be used together: an evaluator is either a " +
				"rubric or code, not both")
	}

	// A rubric's schemas are fixed by the service, so these would be accepted
	// and then quietly dropped — the worst kind of no-op, because the author
	// believes the evaluator was published carrying them.
	if folder == "" {
		for _, named := range []struct {
			flag  string
			value string
		}{
			{"init-params", flags.initParams},
			{"data-schema", flags.dataSchema},
			{"metrics", flags.metrics},
		} {
			if named.value != "" {
				return fmt.Errorf(
					"--%s applies to a code evaluator and needs --folder; "+
						"a rubric's schemas are set by the service", named.flag)
			}
		}
	}
	return nil
}

// runEvaluatorCreateFromFolder validates the folder, then publishes it.
func runEvaluatorCreateFromFolder(
	cmd *cobra.Command,
	name string,
	folder string,
	flags codeEvaluatorFlags,
) error {
	pkg, err := evalcore.LoadCodeEvaluator(name, folder)
	if err != nil {
		return err
	}

	opts, err := codeEvaluatorOptions(pkg, flags)
	if err != nil {
		return err
	}

	ctx := cmd.Context()
	ec, err := newEvalContext(ctx, flags.endpoint)
	if err != nil {
		return err
	}
	defer ec.Close()

	created, err := ec.evalClient.UploadCodeEvaluatorVersion(
		ctx, pkg, opts, ProjectEndpointAPIVersion,
	)
	if err != nil {
		return fmt.Errorf("publishing evaluator %q: %w", name, err)
	}

	if isJSON(cmd) {
		return emitJSON(cmd.OutOrStdout(), created)
	}
	fmt.Fprintf(cmd.OutOrStdout(),
		"Published evaluator %s version %s from %d file(s) in %s\n",
		created.Name, created.Version, len(pkg.Files), folder)
	return nil
}

// codeEvaluatorOptions resolves the evaluator's schemas, preferring an
// explicit flag over whatever the folder declares.
//
// The folder is the better place for them — they describe the code and belong
// beside it — but a folder that has none must still be publishable without
// editing it, which is what the flags are for.
func codeEvaluatorOptions(
	pkg *evalcore.CodeEvaluatorPackage,
	flags codeEvaluatorFlags,
) (eval_api.CodeEvaluatorOptions, error) {
	var opts eval_api.CodeEvaluatorOptions
	if md := pkg.Metadata; md != nil {
		opts.DisplayName = md.DisplayName
		opts.Description = md.Description
		opts.Categories = md.Categories
		opts.InitParameters = md.InitParameters
		opts.DataSchema = md.DataSchema
		opts.Metrics = md.Metrics
	}

	for _, override := range []struct {
		path  string
		flag  string
		field *json.RawMessage
	}{
		{flags.initParams, "init-params", &opts.InitParameters},
		{flags.dataSchema, "data-schema", &opts.DataSchema},
		{flags.metrics, "metrics", &opts.Metrics},
	} {
		if override.path == "" {
			continue
		}
		raw, err := readJSONObject(override.path)
		if err != nil {
			return opts, fmt.Errorf("--%s %q: %w", override.flag, override.path, err)
		}
		*override.field = raw
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
