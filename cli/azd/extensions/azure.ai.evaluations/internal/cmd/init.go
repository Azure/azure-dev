// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
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

			// Scaffolding a config azd cannot see is half a step: the eval
			// service has to be referenced from the root config before any of
			// `azd up`, `azd deploy` or `azd ai eval run` will act on it.
			// Printing the block and leaving the edit to the reader was enough
			// to make the documented flow stop working between `init` and
			// `azd up`.
			rootWiring, err := ensureRootEvalService(rootConfigName, depPath)
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

			fmt.Fprintln(out, "\nNext:")
			step := 1
			if rootWiring == wiringManual {
				// Only reached when the root config could not be read or has a
				// shape this cannot safely edit, so the wiring is the caller's
				// to do.
				fmt.Fprintf(out, "  %d. Reference %s from your root azure.yaml:\n", step, depPath)
				fmt.Fprintln(out, "       services:")
				fmt.Fprintln(out, "         evals:")
				fmt.Fprintln(out, "           host: azure.ai.eval")
				fmt.Fprintln(out, "           uses: [ai-project]")
				fmt.Fprintf(out, "           $ref: ./%s\n", filepath.ToSlash(depPath))
				step++
			} else {
				fmt.Fprintf(out, "  (%s references %s)\n", rootConfigName, depPath)
			}
			fmt.Fprintf(out, "  %d. azd ai eval generate    (or supply your own dataset)\n", step)
			step++
			// azd up provisions before it deploys, which needs a bicep template.
			// An eval-only project has none, and the failure names a missing
			// infra/main.bicep rather than the reason, so it is only suggested
			// where it can work.
			if hasInfra() {
				fmt.Fprintf(out, "  %d. azd up\n", step)
			} else {
				fmt.Fprintf(out, "  %d. azd deploy evals    (azd up once the project has infra to provision)\n", step)
			}
			step++
			fmt.Fprintf(out, "  %d. azd ai eval run\n", step)
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

// rootConfigName is azd's project file, which the eval service is declared in.
const rootConfigName = "azure.yaml"

// How the root config ended up referencing the eval service.
const (
	wiringCreated = "created" // there was no root config, so one was written
	wiringAdded   = "added"   // the service was added to an existing config
	wiringPresent = "present" // an eval service was already declared
	wiringManual  = "manual"  // the caller has to do it; the block is printed
)

// ensureRootEvalService declares the eval service in azd's project file.
//
// A config azd cannot see does nothing, and the reference is mechanical, so it
// is written rather than described. An existing project file is edited in place
// through the YAML node tree, which keeps its comments and key order; anything
// that cannot be edited safely falls back to printing the block.
func ensureRootEvalService(rootPath, depPath string) (string, error) {
	ref := "./" + filepath.ToSlash(depPath)

	raw, err := os.ReadFile(rootPath)
	if errors.Is(err, os.ErrNotExist) {
		name := filepath.Base(mustAbs(filepath.Dir(rootPath)))
		body := fmt.Sprintf(""+
			"name: %s\n"+
			"services:\n"+
			"  evals:\n"+
			"    host: azure.ai.eval\n"+
			"    $ref: %s\n", name, ref)
		if err := os.WriteFile(rootPath, []byte(body), 0o600); err != nil {
			return "", fmt.Errorf("writing %s: %w", rootPath, err)
		}
		return wiringCreated, nil
	}
	if err != nil {
		return wiringManual, nil
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil || len(doc.Content) == 0 {
		return wiringManual, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return wiringManual, nil
	}

	services := mappingValue(root, "services")
	if services != nil && services.Kind == yaml.MappingNode {
		// A service already pointing at an eval config is left alone, whatever
		// it is called: adding a second would deploy the same evals twice.
		for i := 0; i+1 < len(services.Content); i += 2 {
			if host := mappingValue(services.Content[i+1], "host"); host != nil &&
				host.Value == project.EvalHost {
				return wiringPresent, nil
			}
		}
	}
	if services == nil {
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "services"},
			&yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"})
		services = root.Content[len(root.Content)-1]
	}
	if services.Kind != yaml.MappingNode {
		return wiringManual, nil
	}

	entry := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	entry.Content = append(entry.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "host"},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: project.EvalHost},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "$ref"},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: ref})
	services.Content = append(services.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: uniqueServiceName(services)},
		entry)

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return wiringManual, nil
	}
	if err := os.WriteFile(rootPath, out, 0o600); err != nil {
		return "", fmt.Errorf("updating %s: %w", rootPath, err)
	}
	return wiringAdded, nil
}

// mappingValue returns the value node for key, or nil.
func mappingValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// uniqueServiceName avoids colliding with a service the project already has.
func uniqueServiceName(services *yaml.Node) string {
	taken := map[string]bool{}
	for i := 0; i+1 < len(services.Content); i += 2 {
		taken[services.Content[i].Value] = true
	}
	if !taken["evals"] {
		return "evals"
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("evals%d", i)
		if !taken[candidate] {
			return candidate
		}
	}
}

func mustAbs(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

// hasInfra reports whether azd has a template to provision, which decides
// whether `azd up` can work here.
func hasInfra() bool {
	_, err := os.Stat(filepath.Join("infra", "main.bicep"))
	return err == nil
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
