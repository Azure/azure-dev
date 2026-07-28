// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"azureaieval/internal/pkg/evalcore"
	"azureaieval/internal/project"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
)

// newInitCommand scaffolds the eval configuration. It makes no service calls at
// all, so it works offline and unauthenticated.
func newInitCommand() *cobra.Command {
	var (
		target     string
		dataset    string
		evaluators []string
		evalModel  string
		outDir     string
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
			if outDir == "" {
				outDir = project.DefaultEvalDir
			}

			genPath := filepath.Join(outDir, "eval_generate.yaml")
			depPath := filepath.Join(outDir, "azure.yaml")

			for _, p := range []string{genPath, depPath} {
				if _, err := os.Stat(p); err == nil && !force {
					return fmt.Errorf("%s already exists; pass --force to overwrite", p)
				}
			}

			if err := os.MkdirAll(filepath.Join(outDir, project.DefaultDatasetsDir), 0o750); err != nil {
				return fmt.Errorf("creating the datasets directory: %w", err)
			}
			if err := os.MkdirAll(filepath.Join(outDir, project.DefaultEvaluatorsDir), 0o750); err != nil {
				return fmt.Errorf("creating the evaluators directory: %w", err)
			}

			rubricName := fmt.Sprintf("%s-quality", target)

			genCfg := buildGenerateScaffold(target, rubricName, evalModel)
			if err := writeYAML(genPath, genCfg); err != nil {
				return err
			}

			depCfg := buildDeployScaffold(target, rubricName, dataset, evaluators, evalModel, outDir)
			if err := writeYAML(depPath, depCfg); err != nil {
				return err
			}

			if isJSON(cmd) {
				return emitJSON(out, map[string]any{
					"generateConfig": genPath,
					"deployConfig":   depPath,
					"datasetsDir":    filepath.Join(outDir, project.DefaultDatasetsDir),
					"evaluatorsDir":  filepath.Join(outDir, project.DefaultEvaluatorsDir),
				})
			}

			fmt.Fprintf(out, "Wrote %s\n", genPath)
			fmt.Fprintf(out, "Wrote %s\n", depPath)
			fmt.Fprintln(out, "\nNext:")
			fmt.Fprintf(out, "  1. Reference %s from your root azure.yaml:\n", depPath)
			fmt.Fprintln(out, "       services:")
			fmt.Fprintln(out, "         evals:")
			fmt.Fprintln(out, "           host: azure.ai.eval")
			fmt.Fprintln(out, "           uses: [ai-project]")
			fmt.Fprintf(out, "           $ref: ./%s\n", filepath.ToSlash(depPath))
			fmt.Fprintln(out, "  2. azd ai eval generate    (or supply your own dataset)")
			fmt.Fprintln(out, "  3. azd up")
			fmt.Fprintln(out, "  4. azd ai eval run")
			return nil
		},
	}

	cmd.Flags().StringVar(&target, "target", "", "Name of the agent to evaluate.")
	cmd.Flags().StringVar(&dataset, "dataset", "", "Path to a local .jsonl, or the name of a registered dataset.")
	cmd.Flags().StringArrayVar(&evaluators, "evaluator", nil,
		"Evaluator reference, repeatable. Use builtin.<name> for a built-in.")
	cmd.Flags().StringVar(&evalModel, "eval-model", "", "Model deployment used as the LLM judge.")
	cmd.Flags().StringVar(&outDir, "out-dir", project.DefaultEvalDir,
		"Directory to write the config into. Used verbatim, never re-rooted.")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite existing files.")
	return cmd
}

func buildGenerateScaffold(target, rubricName, evalModel string) *project.GenerateConfig {
	return &project.GenerateConfig{
		Agent: project.AgentSpec{
			Name: target,
			Context: project.AgentContext{
				// Scaffolded even though the file does not exist yet: writing
				// it overrides the agent's published instructions, which is the
				// usual way to narrow what gets generated. `tools` is left out
				// because nothing reads it yet.
				Instructions: "./agent/instructions.md",
			},
		},
		Generate: project.GenerateSpec{
			Rubric: &project.RubricSpec{
				Name:     rubricName,
				Model:    evalModel,
				LocalDir: "./" + project.DefaultEvaluatorsDir,
			},
			Dataset: &project.DatasetSpec{
				Name:       fmt.Sprintf("%s-golden", target),
				Strategy:   project.StrategySynthetic,
				SampleSize: project.DefaultSampleSize,
				LocalDir:   "./" + project.DefaultDatasetsDir,
			},
		},
	}
}

// relativeToConfig rewrites a path given relative to the working directory so
// it resolves from the directory holding the deploy spec.
//
// `--dataset ./tests/golden.jsonl` means "relative to where I am", but the
// deploy spec's `source:` is resolved relative to that file, so writing the
// path through unchanged sends the deploy looking inside evals/. An absolute
// path is left alone, and forward slashes are kept so the config reads the same
// on every platform.
func relativeToConfig(path, outDir string) string {
	if filepath.IsAbs(path) {
		return path
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	absOut, err := filepath.Abs(outDir)
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

func buildDeployScaffold(
	target, rubricName, dataset string,
	evaluators []string,
	evalModel string,
	outDir string,
) *project.EvalConfig {
	cfg := &project.EvalConfig{}

	datasetName := fmt.Sprintf("%s-golden", target)
	datasetSource := ""
	if dataset != "" {
		if looksLikeLocalDataset(dataset) {
			// --dataset is given relative to where the user is standing, but
			// source: is resolved relative to the deploy spec, so the path has
			// to be rebased or the deploy looks for it inside evals/.
			datasetSource = relativeToConfig(dataset, outDir)
			datasetName = strings.TrimSuffix(filepath.Base(dataset), filepath.Ext(dataset))
		} else {
			// A bare name references an already-registered dataset.
			datasetName = dataset
		}
	} else {
		datasetSource = fmt.Sprintf("./%s/%s.jsonl", project.DefaultDatasetsDir, datasetName)
	}
	cfg.Datasets = append(cfg.Datasets, project.DatasetDecl{
		Name:   datasetName,
		Source: datasetSource,
	})

	// Evaluators supplied on the command line win; otherwise scaffold the
	// generated rubric so `generate` has somewhere to write its reference.
	refs := evalcore.EvaluatorList{}
	if len(evaluators) == 0 {
		cfg.Evaluators = append(cfg.Evaluators, project.EvaluatorDecl{
			Name:   rubricName,
			Source: fmt.Sprintf("./%s/%s.json", project.DefaultEvaluatorsDir, rubricName),
		})
		refs = append(refs, evalcore.EvaluatorRef{Name: rubricName})
	} else {
		for _, e := range evaluators {
			ref := evalcore.EvaluatorRef{Name: e}
			if !ref.IsBuiltin() {
				cfg.Evaluators = append(cfg.Evaluators, project.EvaluatorDecl{
					Name:   e,
					Source: fmt.Sprintf("./%s/%s.json", project.DefaultEvaluatorsDir, e),
				})
			}
			refs = append(refs, ref)
		}
	}

	group := project.EvalGroup{
		Name:        fmt.Sprintf("%s-quality", target),
		Description: fmt.Sprintf("Quality gate for %s", target),
		Dataset:     datasetName,
		Evaluators:  refs,
		Target: &project.Target{
			Type: project.TargetTypeAgent,
			Name: target,
		},
	}
	if evalModel != "" {
		group.Options = &project.Options{EvalModel: evalModel}
	}
	cfg.EvalGroups = append(cfg.EvalGroups, group)

	return cfg
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
