// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/evalcore"
	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
	"google.golang.org/protobuf/types/known/structpb"
)

// Data sources `init` can point an eval at.
const (
	initSourceDataset = "dataset"
	initSourceTraces  = "traces"
)

// newInitCommand scaffolds the eval configuration. It makes no service calls at
// all, so it works offline and unauthenticated.
//
// It only ever adds. A name already declared is refused rather than
// overwritten, because the settings a reader tunes by hand — thresholds, judge
// model, data mapping — live nowhere but that entry and `init` cannot
// reproduce them. Editing an eval is a file edit.
func newInitCommand() *cobra.Command {
	var (
		evalName   string
		target     string
		source     string
		dataset    string
		maxTraces  int
		evaluators []string
		judgeModel string
		path       string
		force      bool
	)

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Scaffold evaluation config for an agent. Makes no service calls.",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()

			switch source {
			case "", initSourceDataset, initSourceTraces:
			default:
				return messages.SourceNotADataSource(
					source, initSourceDataset, initSourceTraces)
			}
			if source == initSourceTraces && dataset != "" {
				return messages.TracesTakesNoDataset()
			}
			if cmd.Flags().Changed("max-traces") && source != initSourceTraces {
				return messages.MaxTracesNeedsTraceSource()
			}
			if maxTraces < 0 {
				return messages.MaxTracesMustBePositive()
			}
			if source == "" {
				source = initSourceDataset
			}
			if path == "" {
				path = project.DefaultEvalDir
			}

			// Asked before anything is written: the project is the one thing
			// init cannot supply for itself, and failing after creating
			// directories leaves a half-scaffolded tree behind.
			azdProject, err := readAzdProject(cmd.Context())
			if err != nil {
				return err
			}

			// The target is what the whole scaffold is named and shaped around,
			// so it is settled before anything derived from it.
			if target == "" {
				target, err = resolveAgentTarget(cmd, azdProject)
				if err != nil {
					return err
				}
			}
			if evalName == "" {
				evalName = defaultEvalName(target, source)
			}
			if judgeModel == "" {
				judgeModel, err = resolveJudgeModel(cmd, azdProject)
				if err != nil {
					return err
				}
			}

			configPath := project.ResolveEvalConfigPath(path)
			cfg, err := project.OpenEvalConfig(path)
			if err != nil {
				return err
			}
			if cfg == nil {
				cfg = &project.EvalConfig{}
			}
			// Checked before the prompt as well as after it, so a name that is
			// already taken is reported without asking a question first.
			if cfg.HasEval(evalName) && !force {
				return messages.EvalAlreadyDeclared(
					evalName, filepath.ToSlash(configPath))
			}

			// Asked, not detected: an eval grades on a set, so there is no
			// "the only one" to settle on, and which criteria define quality
			// is the substantive decision in the configuration.
			//
			// Deliberately outside the lock below. This is an unbounded human
			// pause, and a lock held across it would either block a concurrent
			// `generate` for as long as someone leaves the terminal, or -- once
			// that side gave up waiting -- protect nothing at all. The listing
			// it offers is only a menu; the authoritative read is taken after.
			evaluatorsWereChosen := len(evaluators) > 0
			if len(evaluators) == 0 {
				var asked bool
				evaluators, asked, err = resolveEvaluators(
					cmd, cfg, target+"-quality", source == initSourceTraces)
				if err != nil {
					return err
				}
				evaluatorsWereChosen = asked
			}

			// The read-modify-write starts here, and nothing inside it waits on
			// a person. The configuration is read again because the copy above
			// was taken before the prompt, and a `generate` may well have
			// finished writing to it since.
			unlockConfig, err := project.LockEvalConfig(cmd.Context(), path)
			if err != nil {
				return err
			}
			defer unlockConfig()

			cfg, err = project.OpenEvalConfig(path)
			if err != nil {
				return err
			}
			if cfg == nil {
				cfg = &project.EvalConfig{}
			}
			if cfg.HasEval(evalName) {
				if !force {
					return messages.EvalAlreadyDeclared(
						evalName, filepath.ToSlash(configPath))
				}
				cfg.RemoveEval(evalName)
			}

			if err := os.MkdirAll(filepath.Join(path, project.DefaultDatasetsDir), 0o750); err != nil {
				return messages.CreatingDatasetsDir(err)
			}
			if err := os.MkdirAll(filepath.Join(path, project.DefaultEvaluatorsDir), 0o750); err != nil {
				return messages.CreatingEvaluatorsDir(err)
			}

			plan := planScaffold(scaffoldInput{
				evalName:   evalName,
				target:     target,
				source:     source,
				dataset:    dataset,
				maxTraces:  maxTraces,
				evaluators: evaluators,
				judgeModel: judgeModel,
				rubricName: target + "-quality",
				evalDir:    path,
				cfg:        cfg,
			})

			if err := project.SaveEvalConfig(path, cfg); err != nil {
				return err
			}

			// Scaffolding a config azd cannot see is half a step: the eval
			// service has to be referenced from the root config before any of
			// `azd up`, `azd deploy` or `azd ai eval run` will act on it.
			serviceName := target + "-evals"
			rootWiring, err := ensureRootEvalService(cmd.Context(), serviceName, target, configPath)
			if err != nil {
				return err
			}

			recordEvalPath(cmd.Context(), path)

			if isJSON(cmd) {
				return emitJSON(out, map[string]any{
					"eval":          evalName,
					"evalConfig":    configPath,
					"service":       serviceName,
					"datasetsDir":   filepath.Join(path, project.DefaultDatasetsDir),
					"evaluatorsDir": filepath.Join(path, project.DefaultEvaluatorsDir),
					"rootConfig":    rootWiring,
					"target":        target,
					"source":        source,
					"judgeModel":    judgeModel,
					"evaluators":    plan.evaluatorNames(),
				})
			}

			fmt.Fprint(out, messages.DetectedTarget(target))
			if source == initSourceTraces {
				fmt.Fprint(out, messages.UsingTraceSource())
			}
			// Only what was settled without asking: a reader who just picked
			// from a list does not need it read back to them.
			if names := plan.evaluatorNames(); len(names) > 0 && !evaluatorsWereChosen {
				fmt.Fprint(out, messages.GradingWith(names))
			}
			if judgeModel != "" {
				fmt.Fprint(out, messages.JudgeModelDeployment(judgeModel))
			}

			fmt.Fprint(out, messages.CreatedHeading())
			fmt.Fprint(out, messages.CreatedConfigLine(filepath.ToSlash(configPath)))
			switch rootWiring {
			case wiringAdded:
				fmt.Fprint(out, messages.AddedServiceLine(rootConfigName, serviceName))
			case wiringPresent:
				fmt.Fprint(out, messages.AlreadyDeclaresServiceLine(rootConfigName, serviceName))
			}

			// Only what was actually scheduled is offered. Suggesting
			// `dataset generate` for a dataset the caller supplied sends them
			// to submit a billed job for an artifact they already have.
			next := plan.nextSteps()
			fmt.Fprint(out, messages.FirstNextStep(next[0]))
			for _, step := range next[1:] {
				fmt.Fprint(out, messages.FurtherNextStep(step))
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&evalName, "name", "",
		"Name of the eval. Defaults to <target>-eval, or <target>-trace-eval under --source traces.")
	cmd.Flags().StringVar(&target, "target", "",
		"Name of the agent to evaluate. Detected when the project has one agent; prompts when it has several.")
	cmd.Flags().StringVar(&source, "source", "",
		"Where rows come from: dataset or traces. Defaults to dataset.")
	cmd.Flags().StringVar(&dataset, "dataset", "",
		"Path to a local .jsonl, or the name of a registered dataset.")
	cmd.Flags().IntVar(&maxTraces, "max-traces", project.DefaultScaffoldMaxTraces,
		"Cap on traces read by a --source traces eval. Delete max_traces from the "+
			"file to take the service default instead.")
	cmd.Flags().StringArrayVar(&evaluators, "evaluator", nil,
		"Evaluator reference, repeatable. Use builtin.<name> for a built-in. "+
			"Passing this replaces the defaults, so it also opts out of rubric generation.")
	cmd.Flags().StringVar(&judgeModel, "judge-model", "",
		"Model deployment the graders judge with. Detected from the project when omitted.")
	cmd.Flags().StringVar(&path, "path", project.DefaultEvalDir,
		"Directory to write the configuration into. Used verbatim, never re-rooted.")
	cmd.Flags().BoolVar(&force, "force", false,
		"Replace an eval of the same name instead of failing.")
	return cmd
}

// defaultEvalName names an eval after what it evaluates and what it reads.
func defaultEvalName(target, source string) string {
	if source == initSourceTraces {
		return target + "-trace-eval"
	}
	return target + "-eval"
}

// scaffoldInput is everything planScaffold needs, gathered so the signature
// does not grow a seventh positional string.
type scaffoldInput struct {
	evalName   string
	target     string
	source     string
	dataset    string
	maxTraces  int
	evaluators []string
	judgeModel string
	rubricName string
	evalDir    string
	cfg        *project.EvalConfig
}

// scaffold is what `init` added, and what it should suggest doing next.
type scaffold struct {
	eval            *project.Eval
	datasetName     string
	rubricName      string
	generateDataset bool
	generateRubric  bool
}

// planScaffold appends one eval to the configuration, adding any catalog
// entries it needs.
//
// The default evaluator set is a built-in plus a generated rubric: the built-in
// alone would be generic, and the rubric is what makes the baseline about this
// agent. Passing --evaluator replaces both, which is how a caller opts out of
// rubric generation.
func planScaffold(in scaffoldInput) scaffold {
	cfg := in.cfg
	out := scaffold{rubricName: in.rubricName}

	eval := project.Eval{
		Name:            in.evalName,
		Description:     fmt.Sprintf("Basic quality evaluation for %s", in.target),
		EvaluationLevel: project.EvaluationLevelTurn,
		Target: &project.Target{
			Type: project.TargetTypeAgent,
			Name: in.target,
		},
	}

	if in.source == initSourceTraces {
		// A trace-backed eval filters by agent rather than invoking one: the
		// conversations already happened.
		eval.Target = nil
		eval.Source = &project.SourceDecl{
			Type:      project.SourceTypeTraces,
			AgentName: in.target,
			MaxTraces: in.maxTraces,
		}
	} else {
		datasetName := in.evalName
		datasetSource := ""
		out.generateDataset = true
		if in.dataset != "" {
			out.generateDataset = false
			if looksLikeLocalDataset(in.dataset) {
				// --dataset is given relative to where the user is standing,
				// but source: resolves relative to the config, so the path has
				// to be rebased or the deploy looks for it inside evals/.
				datasetSource = relativeToConfig(in.dataset, in.evalDir)
				datasetName = strings.TrimSuffix(
					filepath.Base(in.dataset), filepath.Ext(in.dataset))
			} else {
				// A bare name references an already-registered dataset.
				datasetName = in.dataset
			}
		} else {
			datasetSource = fmt.Sprintf("./%s/%s.jsonl", project.DefaultDatasetsDir, datasetName)
		}
		eval.Dataset = datasetName
		out.datasetName = datasetName
		addDatasetDecl(cfg, project.DatasetDecl{Name: datasetName, Source: datasetSource})
	}

	// Every evaluator carries the judge deployment, because that is where the
	// service reads it from: judging built-ins declare it as required, so an
	// eval that leaves it off is rejected before it runs. The binding step
	// drops it again for a rule-based evaluator that declares no judge.
	initParams := map[string]any{}
	if in.judgeModel != "" {
		initParams["model"] = in.judgeModel
	}
	withModel := func(ref evalcore.EvaluatorRef) evalcore.EvaluatorRef {
		if len(initParams) == 0 {
			return ref
		}
		params := make(map[string]any, len(initParams))
		maps.Copy(params, initParams)
		ref.InitializationParameters = params
		return ref
	}

	refs := evalcore.EvaluatorList{}
	if len(in.evaluators) == 0 {
		refs = append(refs,
			withModel(evalcore.EvaluatorRef{
				Evaluator: evalcore.BuiltinPrefix + "task_adherence",
			}))
		if in.source != initSourceTraces {
			refs = append(refs, withModel(evalcore.EvaluatorRef{Evaluator: in.rubricName}))
			addEvaluatorDecl(cfg, project.EvaluatorDecl{
				Name:   in.rubricName,
				Source: fmt.Sprintf("./%s/%s.json", project.DefaultEvaluatorsDir, in.rubricName),
			})
			out.generateRubric = true
		}
	} else {
		for _, e := range in.evaluators {
			ref := evalcore.EvaluatorRef{Evaluator: e}
			refs = append(refs, withModel(ref))
			if ref.IsBuiltin() {
				continue
			}
			addEvaluatorDecl(cfg, project.EvaluatorDecl{
				Name:   e,
				Source: fmt.Sprintf("./%s/%s.json", project.DefaultEvaluatorsDir, e),
			})
			// Chosen, not defaulted, but it is still the rubric init offers to
			// write, so it still has to be generated. Without this the config
			// declares a file that nothing produces and `create` fails looking
			// for it.
			if e == in.rubricName {
				out.generateRubric = true
			}
		}
	}
	eval.Evaluators = refs

	cfg.Evals = append(cfg.Evals, eval)
	out.eval = &cfg.Evals[len(cfg.Evals)-1]
	return out
}

// addDatasetDecl adds a catalog entry unless the name is already declared.
func addDatasetDecl(cfg *project.EvalConfig, decl project.DatasetDecl) {
	if decl.Name == "" {
		return
	}
	// A source-less entry is still declared: it names a dataset already
	// registered on the project. Skipping it left the eval referencing a
	// dataset absent from the catalog, which its own validation rejects.
	if _, ok := cfg.DatasetDeclaration(decl.Name); ok {
		return
	}
	cfg.Datasets = append(cfg.Datasets, decl)
}

// addEvaluatorDecl adds a catalog entry unless the name is already declared.
func addEvaluatorDecl(cfg *project.EvalConfig, decl project.EvaluatorDecl) {
	if _, ok := cfg.EvaluatorDeclaration(decl.Name); ok {
		return
	}
	cfg.Evaluators = append(cfg.Evaluators, decl)
}

// evaluatorNames lists the evaluators the eval will run, in declaration order.
func (s scaffold) evaluatorNames() []string {
	names := make([]string, 0, len(s.eval.Evaluators))
	for _, ref := range s.eval.Evaluators {
		names = append(names, ref.Evaluator)
	}
	return names
}

// nextSteps are the commands to run after `init`, and only the ones that have
// something to do.
//
// A caller who supplied both a dataset and their evaluators has nothing left to
// generate, and pointing them at a generation command would submit a billed job
// for an artifact they already have.
func (s scaffold) nextSteps() []string {
	var steps []string
	switch {
	case s.generateDataset && s.generateRubric:
		// One command produces both, which is the whole point of the composite.
		steps = append(steps, "azd ai eval generate")
	case s.generateDataset:
		steps = append(steps,
			"azd ai eval generate --dataset --dataset-name "+s.datasetName)
	case s.generateRubric:
		steps = append(steps,
			"azd ai eval generate --evaluator --evaluator-name "+s.rubricName)
	}
	if len(steps) == 0 {
		// TODO: suggest `azd deploy`, not `azd up`. Eval resources are data-plane
		// only, so a project with no infra/ fails provision before reaching us.
		steps = append(steps, "azd up", "azd ai eval run start")
	}
	return steps
}

// relativeToConfig rewrites a path given relative to the working directory so
// it resolves from the directory holding the eval config.
func relativeToConfig(path, evalDir string) string {
	if filepath.IsAbs(path) {
		return path
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	absOut, err := filepath.Abs(evalDir)
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

// readAzdProject returns the project, without changing it.
func readAzdProject(ctx context.Context) (*azdext.ProjectConfig, error) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return nil, messages.NoAzdProject()
	}
	defer azdClient.Close()

	resp, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil || resp.GetProject() == nil {
		return nil, messages.NoAzdProject()
	}
	return resp.GetProject(), nil
}

// aiModelHost is the model-deployment service the sibling Foundry extensions
// declare, which is where a judge deployment can be read without a service
// call.
const aiModelHost = "azure.ai.model"

// detectModelDeployment finds the deployment the graders judge with, from what
// the project already declares.
//
// `init` makes no service calls, so detection is limited to the project file.
// Coming back empty leaves it to resolveJudgeModel, which reads the Foundry
// project's deployments: and then asks or names --judge-model.
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

// recordEvalPath remembers where the configuration was written, so the commands
// that read it afterwards do not need --path repeated.
//
// Best effort: `init` works outside an azd environment, and a path that could
// not be recorded only costs the caller a flag later. It is never a reason to
// fail a scaffold that already succeeded.
func recordEvalPath(ctx context.Context, path string) {
	if path == "" || path == project.DefaultEvalDir {
		return
	}
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return
	}
	defer azdClient.Close()

	env, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil || env.GetEnvironment() == nil {
		return
	}
	_, _ = azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: env.GetEnvironment().GetName(),
		Key:     envKeyEvalPath,
		Value:   filepath.ToSlash(path),
	})
}

// ensureRootEvalService declares the eval service in azd's project file.
//
// azd acts on nothing until the service exists, so the reference is made rather
// than described. It goes through azd's own Project().AddService, the same call
// the agents extension uses, so azd owns the edit and the project file keeps
// whatever shape azd gives it.
func ensureRootEvalService(
	ctx context.Context,
	serviceName, target, configPath string,
) (string, error) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return "", messages.ConnectingToAzd(err)
	}
	defer azdClient.Close()

	resp, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil || resp.GetProject() == nil {
		return "", messages.NoAzdProject()
	}

	// A service already pointing at this configuration is left alone:
	// re-adding it would deploy the same evals twice.
	if svc, ok := resp.GetProject().GetServices()[serviceName]; ok && svc.GetHost() == project.EvalHost {
		return wiringPresent, nil
	}

	props, err := structpb.NewStruct(map[string]any{
		"$ref": "./" + filepath.ToSlash(configPath),
	})
	if err != nil {
		return "", messages.BuildingServiceEntry(err)
	}

	_, err = azdClient.Project().AddService(ctx, &azdext.AddServiceRequest{
		Service: &azdext.ServiceConfig{
			Name:                 serviceName,
			Host:                 project.EvalHost,
			Uses:                 evalServiceUses(resp.GetProject(), target),
			AdditionalProperties: props,
		},
	})
	if err != nil {
		return "", messages.AddingServiceTo(rootConfigName, err)
	}
	return wiringAdded, nil
}

// evalServiceUses orders the eval after the things it reads.
//
// It is conditional for the same reason the agents extension makes it
// conditional: naming a service the project does not declare is a broken
// reference, and an eval config can perfectly well sit in a repo that reaches
// an existing Foundry project by endpoint and an agent deployed elsewhere.
//
// Catalog entries need no ordering of their own — datasets, evaluators and
// evals are reconciled in a fixed order inside one deploy, forced by the
// contract rather than chosen.
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

// looksLikeLocalDataset distinguishes a path from a registered dataset name.
func looksLikeLocalDataset(v string) bool {
	if strings.ContainsAny(v, `/\`) {
		return true
	}
	return strings.EqualFold(filepath.Ext(v), ".jsonl")
}

func writeYAML(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return messages.Creating(filepath.Dir(path), err)
	}
	data, err := yaml.Marshal(v)
	if err != nil {
		return messages.Serializing(path, err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return messages.Writing(path, err)
	}
	return nil
}
