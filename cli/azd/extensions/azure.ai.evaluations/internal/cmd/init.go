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

			// Scaffolding a config azd cannot see is half a step: the eval
			// service has to be referenced from the root config before any of
			// `azd up`, `azd deploy` or `azd ai eval run` will act on it.
			// Printing the block and leaving the edit to the reader was enough
			// to make the documented flow stop working between `init` and
			// `azd up`.
			rootWiring, err := ensureRootEvalService(cmd.Context(), depPath)
			if err != nil {
				return err
			}

			if isJSON(cmd) {
				return emitJSON(out, map[string]any{
					"generateConfig": genPath,
					"deployConfig":   depPath,
					"datasetsDir":    filepath.Join(outDir, project.DefaultDatasetsDir),
					"evaluatorsDir":  filepath.Join(outDir, project.DefaultEvaluatorsDir),
					"rootConfig":     rootWiring,
				})
			}

			fmt.Fprintf(out, "Wrote %s\n", genPath)
			fmt.Fprintf(out, "Wrote %s\n", depPath)
			switch rootWiring {
			case wiringAdded:
				fmt.Fprintf(out, "Added the evals service to %s\n", rootConfigName)
			case wiringPresent:
				fmt.Fprintf(out, "%s already declares an eval service\n", rootConfigName)
			}

			fmt.Fprintln(out, "\nNext:")
			fmt.Fprintln(out, "  1. azd ai eval generate    (or supply your own dataset)")
			fmt.Fprintln(out, "  2. azd up")
			fmt.Fprintln(out, "  3. azd ai eval run")
			return nil
		},
	}

	cmd.Flags().StringVar(&target, "target", "", "Name of the agent to evaluate.")
	cmd.Flags().StringVar(&dataset, "dataset", "", "Path to a local .jsonl, or the name of a registered dataset.")
	cmd.Flags().StringArrayVar(&evaluators, "evaluator", nil,
		"Evaluator reference, repeatable. Use builtin.<name> for a built-in.")
	cmd.Flags().StringVar(&evalModel, "judge-model", "", "Model deployment that scores the results.")
	cmd.Flags().StringVar(&outDir, "out-dir", project.DefaultEvalDir,
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

// ensureRootEvalService declares the eval service in azd's project file.
//
// azd acts on nothing until the service exists, so the reference is made rather
// than described. It goes through azd's own Project().AddService, the same call
// the agents extension uses, so azd owns the edit and the project file keeps
// whatever shape azd gives it.
//
// The eval config itself stays in evals/azure.yaml and is referenced with
// `$ref`. azd carries unknown keys through AdditionalProperties untouched,
// which is how the extension gets it back at deploy time.
func ensureRootEvalService(ctx context.Context, depPath string) (string, error) {
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

	// A service already pointing at an eval config is left alone, whatever it
	// is called: a second one would deploy the same evals twice.
	for _, svc := range resp.GetProject().GetServices() {
		if svc.GetHost() == project.EvalHost {
			return wiringPresent, nil
		}
	}

	props, err := structpb.NewStruct(map[string]any{
		"$ref": "./" + filepath.ToSlash(depPath),
	})
	if err != nil {
		return "", fmt.Errorf("building the eval service entry: %w", err)
	}

	_, err = azdClient.Project().AddService(ctx, &azdext.AddServiceRequest{
		Service: &azdext.ServiceConfig{
			Name:                 evalServiceName(resp.GetProject()),
			Host:                 project.EvalHost,
			Uses:                 projectServiceUses(resp.GetProject()),
			AdditionalProperties: props,
		},
	})
	if err != nil {
		return "", fmt.Errorf("adding the eval service to %s: %w", rootConfigName, err)
	}
	return wiringAdded, nil
}

// projectServiceUses points the eval service at the Foundry project service
// when the repo declares one, so azd provisions it first.
//
// It is conditional for the same reason the agents extension makes it
// conditional: naming a service the project does not declare is a broken
// reference, and an eval config can perfectly well sit in a repo that reaches
// an existing Foundry project by endpoint instead.
func projectServiceUses(proj *azdext.ProjectConfig) []string {
	for name, svc := range proj.GetServices() {
		if svc.GetHost() == aiProjectHost {
			return []string{name}
		}
	}
	return nil
}

// evalServiceName avoids colliding with a service the project already has.
// azd keys services by name, so the map key is the name to avoid.
func evalServiceName(proj *azdext.ProjectConfig) string {
	taken := proj.GetServices()
	if _, exists := taken["evals"]; !exists {
		return "evals"
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("evals%d", i)
		if _, exists := taken[candidate]; !exists {
			return candidate
		}
	}
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

	group := project.Eval{
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
	cfg.Evals = append(cfg.Evals, group)

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
