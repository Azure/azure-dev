// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

// projectCanProvision decides which deploy command `init` names, so it has to
// agree with what azd actually does rather than with what the layout suggests.
// azd infers the provider from the files in the infra directory; an empty or
// missing directory leaves it unspecified, falls back to Bicep, and fails on
// the absent infra/main.bicep.
func TestProjectCanProvision(t *testing.T) {
	write := func(t *testing.T, dir string, names ...string) string {
		t.Helper()
		root := t.TempDir()
		if dir != "" {
			require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0o750))
		}
		for _, n := range names {
			require.NoError(t,
				os.WriteFile(filepath.Join(root, dir, n), []byte("// x"), 0o600))
		}
		return root
	}

	t.Run("no infra directory", func(t *testing.T) {
		root := write(t, "")
		require.False(t, projectCanProvision(&azdext.ProjectConfig{Path: root}),
			"this is the eval-only project, where `azd up` exits 1")
	})

	t.Run("infra directory with bicep", func(t *testing.T) {
		root := write(t, "infra", "main.bicep")
		require.True(t, projectCanProvision(&azdext.ProjectConfig{Path: root}))
	})

	t.Run("infra directory with terraform", func(t *testing.T) {
		root := write(t, "infra", "main.tf")
		require.True(t, projectCanProvision(&azdext.ProjectConfig{Path: root}))
	})

	// An empty directory is the case a plain os.Stat would get wrong: the
	// directory exists, and azd still has nothing to compile.
	t.Run("empty infra directory", func(t *testing.T) {
		root := write(t, "infra")
		require.False(t, projectCanProvision(&azdext.ProjectConfig{Path: root}))
	})

	// Nothing recurses: azd reads one directory and skips subdirectories.
	t.Run("bicep only in a subdirectory", func(t *testing.T) {
		root := write(t, filepath.Join("infra", "modules"), "db.bicep")
		require.False(t, projectCanProvision(&azdext.ProjectConfig{Path: root}))
	})

	t.Run("project names its own infra directory", func(t *testing.T) {
		root := write(t, "deploy", "main.bicep")
		require.True(t, projectCanProvision(&azdext.ProjectConfig{
			Path:  root,
			Infra: &azdext.InfraOptions{Path: "deploy"},
		}), "the declared path is read, not the default one")
		require.False(t, projectCanProvision(&azdext.ProjectConfig{Path: root}),
			"and the default is empty here")
	})

	t.Run("no project", func(t *testing.T) {
		require.False(t, projectCanProvision(nil),
			"an unreadable project cannot be claimed to provision")
	})
}

// scaffoldFor runs planScaffold against a fresh configuration, which is what
// `init` does on a project that has never been initialized.
func scaffoldFor(t *testing.T, in scaffoldInput) (scaffold, *project.EvalConfig) {
	t.Helper()
	if in.cfg == nil {
		in.cfg = &project.EvalConfig{}
	}
	if in.evalDir == "" {
		in.evalDir = project.DefaultEvalDir
	}
	plan, err := planScaffold(in)
	require.NoError(t, err)
	return plan, in.cfg
}

// The scaffold must round-trip and validate, otherwise `azd up` fails on a
// config the tool itself produced.
func TestScaffold_RoundTripsAndValidates(t *testing.T) {
	dir := t.TempDir()
	_, cfg := scaffoldFor(t, scaffoldInput{
		evalName:   "support-agent-smoke",
		target:     "support-agent",
		judgeModel: "gpt-4.1-nano",
		dataset:    "prod-golden",
		evalDir:    dir,
	})

	require.NoError(t, project.SaveEvalConfig(dir, cfg))
	loaded, err := project.OpenEvalConfig(dir)
	require.NoError(t, err)
	require.NoError(t, loaded.Validate(), "the generated scaffold must be valid")

	eval, err := loaded.Eval("support-agent-smoke")
	require.NoError(t, err)
	require.Equal(t, project.TargetTypeAgent, eval.Target.Type)
	require.Equal(t, "support-agent", eval.Target.Name)
	require.Equal(t, project.EvaluationLevelTurn, eval.EvaluationLevel)
}

// Re-running init appends rather than replacing, so one file ends up holding
// every eval for the target.
func TestScaffold_AppendsToAnExistingConfiguration(t *testing.T) {
	dir := t.TempDir()
	_, cfg := scaffoldFor(t, scaffoldInput{
		evalName: "first", target: "support-agent", judgeModel: "m", evalDir: dir,
		dataset: "prod-golden",
	})
	// A different dataset, or the two evals are identical apart from their
	// names and the configuration's own validation refuses the pair.
	_, cfg = scaffoldFor(t, scaffoldInput{
		evalName: "second", target: "support-agent", judgeModel: "m", evalDir: dir, cfg: cfg,
		dataset: "nightly-golden",
	})

	require.Equal(t, []string{"first", "second"}, cfg.EvalNames())
	require.NoError(t, project.SaveEvalConfig(dir, cfg))
	loaded, err := project.OpenEvalConfig(dir)
	require.NoError(t, err)
	require.NoError(t, loaded.Validate())
}

// A trace-backed eval invokes nothing, so agent_name filters instead of
// targeting, and a scaffolded cap keeps the first run bounded rather than
// taking the service's default of 1000.
func TestScaffold_TraceSourceHasNoTarget(t *testing.T) {
	plan, _ := scaffoldFor(t, scaffoldInput{
		evalName:  "support-agent-trace-eval",
		target:    "support-agent",
		source:    initSourceTraces,
		maxTraces: project.DefaultScaffoldMaxTraces,
	})

	require.Nil(t, plan.eval.Target)
	require.NotNil(t, plan.eval.Source)
	require.Equal(t, project.SourceTypeTraces, plan.eval.Source.Type)
	require.Equal(t, "support-agent", plan.eval.Source.AgentName)
	require.Equal(t, 20, plan.eval.Source.MaxTraces)
}

// Omitting the cap leaves the key out, which is how the service default is
// taken — writing a zero would send one.
func TestScaffold_TraceCapIsOmittedWhenZero(t *testing.T) {
	plan, _ := scaffoldFor(t, scaffoldInput{
		evalName: "t", target: "a", source: initSourceTraces,
	})
	require.Zero(t, plan.eval.Source.MaxTraces)

	body, err := yaml.Marshal(plan.eval)
	require.NoError(t, err)
	require.NotContains(t, string(body), "max_traces")
}

// The default set is one built-in. It used to add a rubric generated from the
// agent's instructions, which declared an evaluator file nothing had produced.
func TestScaffold_DefaultEvaluators(t *testing.T) {
	plan, _ := scaffoldFor(t, scaffoldInput{
		evalName: "support-agent-smoke", target: "support-agent", judgeModel: "gpt-5.6-luna",
		dataset: "prod-golden",
	})

	require.Equal(t, []string{"builtin.task_adherence"}, plan.evaluatorNames())

	// Every evaluator carries the judge deployment, because the judging
	// built-ins declare it and an eval that leaves it off is rejected.
	for _, ref := range plan.eval.Evaluators {
		require.Equal(t, "gpt-5.6-luna", ref.InitializationParameters["model"],
			"%s must name a judge deployment", ref.Evaluator)
	}
}

// Passing --evaluator replaces the defaults.
func TestScaffold_ExplicitEvaluatorsReplaceTheDefault(t *testing.T) {
	plan, _ := scaffoldFor(t, scaffoldInput{
		evalName:   "smoke",
		target:     "support-agent",
		dataset:    "prod-golden",
		evaluators: []string{"builtin.task_adherence"},
		judgeModel: "m",
	})

	require.Equal(t, []string{"builtin.task_adherence"}, plan.evaluatorNames())
}

// `init` closes by naming the deploy. It never names a generation command:
// everything it wrote already resolves, so there is nothing left to produce.
func TestScaffold_NextStepsNameTheDeploy(t *testing.T) {
	plan, _ := scaffoldFor(t, scaffoldInput{
		evalName:   "smoke",
		target:     "support-agent",
		dataset:    "prod-golden",
		evaluators: []string{"builtin.task_adherence"},
		judgeModel: "m",
	})

	// Verified against azd 1.30.0. `azd up` on a project with no infra/
	// exits 1 compiling a missing infra/main.bicep, and `azd deploy` exits
	// 1 with "infrastructure has not been provisioned" in an environment
	// that never provisioned one. `azd ai eval create` needs neither.
	require.Equal(t, []string{"azd ai eval create", "azd ai eval run start"},
		plan.nextSteps("azd ai eval create"),
		"the deploy step is the one the project can actually run")
	require.Equal(t, []string{"azd up", "azd ai eval run start"},
		plan.nextSteps("azd up"),
		"where the project does provision, one command covers both")

	joined := strings.Join(plan.nextSteps("azd ai eval create"), "\n")
	require.NotContains(t, joined, "generate",
		"init writes nothing that has still to be generated")
}

// Which command deploys is decided in one place, so every message that names
// one agrees. The detection itself is covered by TestProjectCanProvision.
func TestDeployCommandName(t *testing.T) {
	root := t.TempDir()
	require.Equal(t, "azd ai eval create", deployCommandName(&azdext.ProjectConfig{Path: root}),
		"nothing to provision, so neither `azd up` nor `azd deploy` would run here")

	require.NoError(t, os.MkdirAll(filepath.Join(root, "infra"), 0o750))
	require.NoError(t,
		os.WriteFile(filepath.Join(root, "infra", "main.bicep"), []byte("// x"), 0o600))
	require.Equal(t, "azd up", deployCommandName(&azdext.ProjectConfig{Path: root}))

	require.Equal(t, "azd ai eval create", deployCommandName(nil),
		"a project we cannot read is not one we can claim provisions")
}

// The literals above are only as good as the surface they name. This resolves
// every step against the real command tree, so a step naming a command that has
// been renamed or removed fails here rather than in a user's terminal — which
// is how `azd ai eval dataset generate` survived being deleted.
func TestScaffold_NextStepsNameCommandsThatExist(t *testing.T) {
	inputs := []scaffoldInput{
		{evalName: "smoke", target: "support-agent", dataset: "prod-golden", judgeModel: "m"},
		// Reaches the deploy branch, so the command it names is resolved too.
		{evalName: "smoke", target: "support-agent", dataset: "prod-golden",
			evaluators: []string{"builtin.task_adherence"}, judgeModel: "m"},
	}

	for _, in := range inputs {
		plan, _ := scaffoldFor(t, in)
		for _, step := range plan.nextSteps("azd ai eval create") {
			// Steps that drive azd itself -- `azd up`, `azd deploy` -- are not
			// this extension's commands and resolve against a different tree.
			if !strings.HasPrefix(step, "azd ai eval ") {
				continue
			}
			words := strings.Fields(strings.TrimPrefix(step, "azd ai eval "))
			if len(words) == 0 {
				continue
			}
			// Stop at the first flag: what follows is arguments, not commands.
			var path []string
			for _, w := range words {
				if strings.HasPrefix(w, "-") {
					break
				}
				path = append(path, w)
			}

			cmd, rest, err := NewRootCommand().Find(path)
			require.NoErrorf(t, err, "%q names no command", step)
			require.Emptyf(t, rest, "%q left %v unresolved, so it is not a command", step, rest)
			require.Equalf(t, path[len(path)-1], strings.Fields(cmd.Use)[0],
				"%q resolved to %q, not the command it names", step, cmd.Use)
		}
	}
}

// Built-ins are referenced but never declared, so the scaffold must not give
// one a catalog entry to publish.
func TestScaffold_BuiltinEvaluatorsGetNoCatalogEntry(t *testing.T) {
	dir := t.TempDir()
	plan, cfg := scaffoldFor(t, scaffoldInput{
		evalName:   "smoke",
		target:     "support-agent",
		dataset:    "prod-golden",
		evaluators: []string{"builtin.task_adherence", "my-custom"},
		judgeModel: "m",
		evalDir:    dir,
		// my-custom has to be declared already: init writes only references
		// that resolve.
		cfg: &project.EvalConfig{Evaluators: []project.EvaluatorDecl{
			{Name: "my-custom", Source: "./evaluators/my-custom.json"},
		}},
	})

	require.Len(t, plan.eval.Evaluators, 2)
	require.True(t, plan.eval.Evaluators[0].IsBuiltin())
	require.False(t, plan.eval.Evaluators[1].IsBuiltin())

	require.Len(t, cfg.Evaluators, 1, "only the custom evaluator is declared")
	require.Equal(t, "my-custom", cfg.Evaluators[0].Name)
	require.Len(t, cfg.CustomEvaluators(), 1,
		"only the custom evaluator is this config's to publish")

	require.NoError(t, project.SaveEvalConfig(dir, cfg))
	loaded, err := project.OpenEvalConfig(dir)
	require.NoError(t, err)
	require.NoError(t, loaded.Validate())
}

// A bare name means an already-registered dataset; a path means a local file.
// Either way the dataset was supplied, so nothing is scheduled to generate it —
// only a missing --dataset produces a generation step.
func TestScaffold_DatasetReferenceForms(t *testing.T) {
	t.Run("local path becomes a source", func(t *testing.T) {
		// --dataset is relative to the working directory, but source: is
		// resolved relative to the eval config, so it has to be rebased.
		plan, cfg := scaffoldFor(t, scaffoldInput{
			evalName: "smoke", target: "a", dataset: "./tests/golden.jsonl", evalDir: "evals",
		})
		decl, ok := cfg.DatasetDeclaration("golden")
		require.True(t, ok)
		require.Equal(t, "../tests/golden.jsonl", decl.File,
			"a dataset outside the eval dir must be reached with ..")
		require.Equal(t, "golden", plan.eval.Dataset)
	})

	t.Run("bare name references a registered dataset", func(t *testing.T) {
		plan, cfg := scaffoldFor(t, scaffoldInput{
			evalName: "smoke", target: "a", dataset: "prod-sample",
		})
		decl, ok := cfg.DatasetDeclaration("prod-sample")
		require.True(t, ok)
		require.Empty(t, decl.File, "a registered dataset must not get a local source")
		require.Equal(t, "prod-sample", plan.eval.Dataset)
	})
}

func TestLooksLikeLocalDataset(t *testing.T) {
	require.True(t, looksLikeLocalDataset("./data/golden.jsonl"))
	require.True(t, looksLikeLocalDataset("golden.jsonl"))
	require.True(t, looksLikeLocalDataset(`data\golden.jsonl`))
	require.False(t, looksLikeLocalDataset("prod-sample"))
}

// Paths are used verbatim relative to the working directory; the doubling bug
// in the agent-scoped command must not reappear.
func TestSaveEvalConfig_UsesPathVerbatim(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "evals")

	require.NoError(t, project.SaveEvalConfig(nested, &project.EvalConfig{}))
	_, err := os.Stat(project.EvalConfigPath(nested))
	require.NoError(t, err, "the file must land exactly at the requested path")

	doubled := filepath.Join(dir, "evals", "evals")
	_, err = os.Stat(doubled)
	require.Error(t, err, "the path must not be re-rooted under itself")
}

// normalizeRubricBody accepts a bare definition or a full document.
func TestNormalizeRubricBody(t *testing.T) {
	t.Run("bare definition is wrapped", func(t *testing.T) {
		body, err := normalizeRubricBody("quality",
			[]byte(`{"type":"rubric","dimensions":[{"id":"q","weight":10}]}`))
		require.NoError(t, err)
		require.Contains(t, string(body), `"name":"quality"`)
		require.Contains(t, string(body), `"definition"`)
	})

	t.Run("full document keeps its definition and takes the flag name", func(t *testing.T) {
		body, err := normalizeRubricBody("renamed",
			[]byte(`{"name":"old","definition":{"type":"rubric","dimensions":[]}}`))
		require.NoError(t, err)
		require.Contains(t, string(body), `"name":"renamed"`)
	})

	t.Run("rejects a document with neither", func(t *testing.T) {
		_, err := normalizeRubricBody("x", []byte(`{"unrelated":true}`))
		require.ErrorContains(t, err, "dimensions")
	})

	t.Run("rejects invalid JSON", func(t *testing.T) {
		_, err := normalizeRubricBody("x", []byte(`not json`))
		require.ErrorContains(t, err, "not valid JSON")
	})
}
