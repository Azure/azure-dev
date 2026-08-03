// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"azureaieval/internal/pkg/evalcore"
	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
	"google.golang.org/protobuf/types/known/structpb"
)

// newInitCommand scaffolds the eval configuration. It makes no service calls at
// all, so it works offline and unauthenticated.
func newInitCommand() *cobra.Command {
	var (
		evalName   string
		target     string
		dataset    string
		evaluators []string
		genModel   string
		outputDir  string
		force      bool
	)

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Scaffold evaluation config for an agent. Makes no service calls.",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()

			if target == "" {
				return requireFlag("target")
			}
			if outputDir == "" {
				outputDir = project.DefaultEvalDir
			}
			if evalName == "" {
				evalName = target + "-smoke"
			}

			evalPath := project.EvalConfigPath(outputDir, evalName)
			genPath := filepath.Join(outputDir, "generate.yaml")

			for _, p := range []string{evalPath, genPath} {
				if _, err := os.Stat(p); err == nil && !force {
					return fmt.Errorf("%s already exists; pass --force to overwrite", p)
				}
			}

			// Asked before anything is written: the project is the one thing init
			// cannot supply for itself, and failing after creating directories
			// leaves a half-scaffolded tree behind for the user to clean up.
			azdProject, err := readAzdProject(cmd.Context())
			if err != nil {
				return err
			}
			if genModel == "" {
				genModel = detectModelDeployment(azdProject)
			}

			if err := os.MkdirAll(filepath.Join(outputDir, project.DefaultDatasetsDir), 0o750); err != nil {
				return fmt.Errorf("creating the datasets directory: %w", err)
			}
			if err := os.MkdirAll(filepath.Join(outputDir, project.DefaultEvaluatorsDir), 0o750); err != nil {
				return fmt.Errorf("creating the evaluators directory: %w", err)
			}

			rubricName := target + "-quality"

			plan := planScaffold(evalName, target, rubricName, dataset, evaluators, genModel, outputDir)

			if err := writeYAML(evalPath, plan.eval); err != nil {
				return err
			}
			if err := writeYAML(genPath, plan.generate); err != nil {
				return err
			}

			// Scaffolding a config azd cannot see is half a step: the eval
			// service has to be referenced from the root config before any of
			// `azd up`, `azd deploy` or `azd ai eval run` will act on it.
			// Printing the block and leaving the edit to the reader was enough
			// to make the documented flow stop working between `init` and
			// `azd up`.
			rootWiring, err := ensureRootEvalService(cmd.Context(), evalName, target, evalPath)
			if err != nil {
				return err
			}

			if isJSON(cmd) {
				return emitJSON(out, map[string]any{
					"eval":            evalName,
					"evalConfig":      evalPath,
					"generateConfig":  genPath,
					"datasetsDir":     filepath.Join(outputDir, project.DefaultDatasetsDir),
					"evaluatorsDir":   filepath.Join(outputDir, project.DefaultEvaluatorsDir),
					"rootConfig":      rootWiring,
					"target":          target,
					"generationModel": genModel,
					"evaluators":      plan.evaluatorNames(),
				})
			}

			fmt.Fprintf(out, "%s Detected agent target: %s\n", doneMark, target)
			if genModel != "" {
				fmt.Fprintf(out, "%s Detected model deployment: %s\n", doneMark, genModel)
			}
			fmt.Fprintf(out, "%s Planned evaluators: %s\n", doneMark, plan.evaluatorSummary())

			fmt.Fprintln(out, "\nCreated")
			fmt.Fprintf(out, "  %-33s eval definition\n", filepath.ToSlash(evalPath))
			fmt.Fprintf(out, "  %-33s generation settings (%d samples, %d rubric)\n",
				filepath.ToSlash(genPath), project.DefaultSampleSize, plan.rubricCount())
			switch rootWiring {
			case wiringAdded:
				fmt.Fprintf(out, "  %-33s added service '%s'\n", rootConfigName, evalName)
			case wiringPresent:
				fmt.Fprintf(out, "  %-33s already declares service '%s'\n", rootConfigName, evalName)
			}

			// Only what was actually scheduled is offered. Suggesting
			// `dataset generate` for a dataset the caller supplied sends them
			// to submit a billed job for an artifact they already have.
			next := plan.nextSteps()
			fmt.Fprintf(out, "\nNext: %s\n", next[0])
			for _, step := range next[1:] {
				fmt.Fprintf(out, "      %s\n", step)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&evalName, "name", "", "Name of the eval. Defaults to <target>-smoke.")
	cmd.Flags().StringVar(&target, "target", "", "Name of the agent to evaluate.")
	cmd.Flags().StringVar(&dataset, "dataset", "", "Path to a local .jsonl, or the name of a registered dataset.")
	cmd.Flags().StringArrayVar(&evaluators, "evaluator", nil,
		"Evaluator reference, repeatable. Use builtin.<name> for a built-in. "+
			"Passing this replaces the defaults, so it also opts out of rubric generation.")
	cmd.Flags().StringVar(&genModel, "generation-model", "",
		"Model deployment that generates and judges. Detected from the project when omitted.")
	cmd.Flags().StringVar(&outputDir, "output-dir", project.DefaultEvalDir,
		"Directory to write the config into. Used verbatim, never re-rooted.")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite existing files.")
	return cmd
}

// rootConfigName is azd's project file, which the eval service is declared in.
const rootConfigName = "azure.yaml"

// aiProjectHost is the Foundry project service other extensions declare. The
// eval service uses it for ordering when the repo has one.
const aiProjectHost = "azure.ai.project"

// How the root config ended up referencing the eval service.
const (
	wiringAdded   = "added"   // the service was added to the project
	wiringPresent = "present" // an eval service was already declared
)

// noAzdProject is what init reports when there is nothing to attach to.
const noAzdProject = "no azd project found in this directory. Run `azd init` first, " +
	"or run this from the root of an existing one; the eval service is "

// readAzdProject returns the project, without changing it.
//
// It is read before anything is written: the project is the one thing init
// cannot supply for itself, and it also carries the agent and model detection
// that `init` reports.
func readAzdProject(ctx context.Context) (*azdext.ProjectConfig, error) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return nil, fmt.Errorf("%sadded to its azure.yaml", noAzdProject)
	}
	defer azdClient.Close()

	resp, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil || resp.GetProject() == nil {
		return nil, fmt.Errorf("%sadded to its azure.yaml", noAzdProject)
	}
	return resp.GetProject(), nil
}

// aiModelHost is the model-deployment service the sibling Foundry extensions
// declare, which is where a judge deployment can be read without a service
// call.
const aiModelHost = "azure.ai.model"

// detectModelDeployment finds the deployment generation and judging run
// against, from what the project already declares.
//
// `init` makes no service calls, so detection is limited to the project file.
// Coming back empty is not a failure: --generation-model supplies it, and the
// generate commands say so when it is missing.
func detectModelDeployment(proj *azdext.ProjectConfig) string {
	for name, svc := range proj.GetServices() {
		if svc.GetHost() != aiModelHost {
			continue
		}
		if props := svc.GetAdditionalProperties().AsMap(); props != nil {
			for _, key := range []string{"deployment", "deploymentName", "name", "model"} {
				if v, ok := props[key].(string); ok && v != "" {
					return v
				}
			}
		}
		return name
	}
	return ""
}

// ensureRootEvalService declares the eval service in azd's project file.
//
// azd acts on nothing until the service exists, so the reference is made rather
// than described. It goes through azd's own Project().AddService, the same call
// the agents extension uses, so azd owns the edit and the project file keeps
// whatever shape azd gives it.
//
// The service key is the eval's name — one `azure.ai.eval` service per eval —
// and the eval body stays in evals/<eval-name>.yaml, referenced with `$ref`.
// azd carries unknown keys through AdditionalProperties untouched, which is how
// the extension gets it back at deploy time.
func ensureRootEvalService(ctx context.Context, evalName, target, evalPath string) (string, error) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return "", fmt.Errorf("connecting to azd: %w", err)
	}
	defer azdClient.Close()

	resp, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil || resp.GetProject() == nil {
		// Evals attach to a project; they do not create one. Saying which
		// command makes one is more use than a gRPC error.
		return "", fmt.Errorf(
			"no azd project found in this directory. Run `azd init` first, "+
				"or run this from the root of an existing one; the eval service is "+
				"added to its %s", rootConfigName)
	}

	// A service already declaring this eval is left alone: re-adding it would
	// deploy the same eval twice. A differently-named eval service is not a
	// conflict, because one service is one eval.
	if svc, ok := resp.GetProject().GetServices()[evalName]; ok && svc.GetHost() == project.EvalHost {
		return wiringPresent, nil
	}

	props, err := structpb.NewStruct(map[string]any{
		"$ref": "./" + filepath.ToSlash(evalPath),
	})
	if err != nil {
		return "", fmt.Errorf("building the eval service entry: %w", err)
	}

	_, err = azdClient.Project().AddService(ctx, &azdext.AddServiceRequest{
		Service: &azdext.ServiceConfig{
			Name:                 evalName,
			Host:                 project.EvalHost,
			Uses:                 evalServiceUses(resp.GetProject(), target),
			AdditionalProperties: props,
		},
	})
	if err != nil {
		return "", fmt.Errorf("adding the eval service to %s: %w", rootConfigName, err)
	}
	return wiringAdded, nil
}

// evalServiceUses orders the eval after the things it reads.
//
// It is conditional for the same reason the agents extension makes it
// conditional: naming a service the project does not declare is a broken
// reference, and an eval config can perfectly well sit in a repo that reaches
// an existing Foundry project by endpoint and an agent that is deployed
// elsewhere.
func evalServiceUses(proj *azdext.ProjectConfig, target string) []string {
	var uses []string
	for name, svc := range proj.GetServices() {
		if svc.GetHost() == aiProjectHost {
			uses = append(uses, name)
			break
		}
	}
	if _, ok := proj.GetServices()[target]; ok {
		uses = append(uses, target)
	}
	return uses
}

// scaffold is what `init` writes: one eval body and the generation settings
// that fill in the artifacts it references.
type scaffold struct {
	eval        *project.EvalConfig
	generate    *project.GenerateConfig
	datasetName string
	rubricName  string
}

// evaluatorNames lists the evaluators the eval will run, in declaration order.
func (s scaffold) evaluatorNames() []string {
	names := make([]string, 0, len(s.eval.Evaluators))
	for _, ref := range s.eval.Evaluators {
		names = append(names, ref.Name)
	}
	return names
}

// evaluatorSummary is the one-line form `init` reports, marking the evaluator
// that still has to be generated.
func (s scaffold) evaluatorSummary() string {
	parts := make([]string, 0, len(s.eval.Evaluators))
	for _, ref := range s.eval.Evaluators {
		if ref.Name == s.rubricName && s.rubricCount() > 0 {
			parts = append(parts, ref.Name+" (rubric)")
			continue
		}
		parts = append(parts, ref.Name)
	}
	return strings.Join(parts, ", ")
}

// rubricCount is the number of evaluators `init` expects to be generated.
func (s scaffold) rubricCount() int {
	if s.generate == nil {
		return 0
	}
	return len(s.generate.Evaluator)
}

// nextSteps are the commands to run after `init`, and only the ones that have
// something to do.
//
// A caller who supplied both a dataset and their evaluators has nothing left to
// generate, and pointing them at a generation command would submit a billed job
// for an artifact they already have. With everything in place the next step is
// to deploy it.
func (s scaffold) nextSteps() []string {
	var steps []string
	if s.generate != nil && len(s.generate.Dataset) > 0 {
		steps = append(steps, "azd ai eval dataset generate "+s.datasetName)
	}
	if s.rubricCount() > 0 {
		steps = append(steps, "azd ai eval evaluator generate "+s.rubricName)
	}
	if len(steps) == 0 {
		steps = append(steps, "azd up", "azd ai eval run start")
	}
	return steps
}

// relativeToConfig rewrites a path given relative to the working directory so
// it resolves from the directory holding the eval config.
//
// `--dataset ./tests/golden.jsonl` means "relative to where I am", but
// `source:` is resolved relative to the config file, so writing the path
// through unchanged sends the deploy looking inside evals/. An absolute path is
// left alone, and forward slashes are kept so the config reads the same on
// every platform.
func relativeToConfig(path, outputDir string) string {
	if filepath.IsAbs(path) {
		return path
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	absOut, err := filepath.Abs(outputDir)
	if err != nil {
		return path
	}

	rel, err := filepath.Rel(absOut, absPath)
	if err != nil {
		return path
	}

	rel = filepath.ToSlash(rel)
	if !strings.HasPrefix(rel, ".") {
		rel = "./" + rel
	}
	return rel
}

// planScaffold builds both files `init` writes.
//
// The default evaluator set is a built-in plus a generated rubric: the built-in
// alone would be generic, and the rubric is what makes the baseline about this
// agent. Passing --evaluator replaces both, which is how a caller opts out of
// rubric generation.
func planScaffold(
	evalName, target, rubricName, dataset string,
	evaluators []string,
	genModel string,
	outputDir string,
) scaffold {
	cfg := &project.EvalConfig{
		Description: fmt.Sprintf("Basic quality evaluation for %s", target),
	}

	datasetName := evalName
	datasetSource := ""
	generateDataset := true
	if dataset != "" {
		if looksLikeLocalDataset(dataset) {
			// --dataset is given relative to where the user is standing, but
			// source: is resolved relative to the config, so the path has to be
			// rebased or the deploy looks for it inside evals/.
			datasetSource = relativeToConfig(dataset, outputDir)
			datasetName = strings.TrimSuffix(filepath.Base(dataset), filepath.Ext(dataset))
		} else {
			// A bare name references an already-registered dataset.
			datasetName = dataset
		}
		generateDataset = false
	} else {
		datasetSource = fmt.Sprintf("./%s/%s.jsonl", project.DefaultDatasetsDir, datasetName)
	}
	cfg.Dataset = &project.DatasetDecl{
		Name:   datasetName,
		Source: datasetSource,
	}

	// Every evaluator carries the judge deployment, because that is where the
	// service reads it from: built-ins declare `deployment_name` as required,
	// so an eval that leaves it off is rejected before it runs.
	initParams := map[string]any{}
	if genModel != "" {
		initParams["deployment_name"] = genModel
	}
	withModel := func(ref evalcore.EvaluatorRef) evalcore.EvaluatorRef {
		if len(initParams) == 0 {
			return ref
		}
		params := make(map[string]any, len(initParams))
		for k, v := range initParams {
			params[k] = v
		}
		ref.InitializationParameters = params
		return ref
	}

	refs := evalcore.EvaluatorList{}
	generateRubric := false
	if len(evaluators) == 0 {
		refs = append(refs,
			withModel(evalcore.EvaluatorRef{Name: evalcore.BuiltinPrefix + "task_adherence"}),
			withModel(evalcore.EvaluatorRef{
				Name:   rubricName,
				Source: fmt.Sprintf("./%s/%s.json", project.DefaultEvaluatorsDir, rubricName),
			}),
		)
		generateRubric = true
	} else {
		for _, e := range evaluators {
			ref := evalcore.EvaluatorRef{Name: e}
			if !ref.IsBuiltin() {
				ref.Source = fmt.Sprintf("./%s/%s.json", project.DefaultEvaluatorsDir, e)
			}
			refs = append(refs, withModel(ref))
		}
	}
	cfg.Evaluators = refs

	cfg.Target = &project.Target{
		Type: project.TargetTypeAgent,
		Name: target,
	}
	cfg.Options = &project.Options{
		MaxSamples:      project.DefaultSampleSize,
		EvaluationLevel: project.EvaluationLevelTurn,
	}

	gen := &project.GenerateConfig{GenerationModel: genModel}
	if generateDataset {
		gen.Dataset = map[string]project.DatasetGenSpec{
			datasetName: {
				SampleSize: project.DefaultSampleSize,
				OutputDir:  "./" + project.DefaultDatasetsDir,
				DeriveFrom: target,
			},
		}
	}
	if generateRubric {
		gen.Evaluator = map[string]project.EvaluatorGenSpec{
			rubricName: {
				OutputDir:  "./" + project.DefaultEvaluatorsDir,
				DeriveFrom: target,
			},
		}
	}

	return scaffold{
		eval:        cfg,
		generate:    gen,
		datasetName: datasetName,
		rubricName:  rubricName,
	}
}

// looksLikeLocalDataset distinguishes a path from a registered dataset name.
func looksLikeLocalDataset(v string) bool {
	if strings.ContainsAny(v, `/\`) {
		return true
	}
	return strings.EqualFold(filepath.Ext(v), ".jsonl")
}

func writeYAML(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("creating %q: %w", filepath.Dir(path), err)
	}
	data, err := yaml.Marshal(v)
	if err != nil {
		return fmt.Errorf("serializing %q: %w", path, err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("writing %q: %w", path, err)
	}
	return nil
}
