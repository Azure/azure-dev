// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
)

// Package-level compiled regexes for output and env-var reference extraction.
var (
	bicepOutputRe   = regexp.MustCompile(`(?m)^\s*output\s+(\w+)\s+`)
	paramEnvSubstRe = regexp.MustCompile(`\$\{(\w+)\}`)
	paramReadEnvRe  = regexp.MustCompile(
		`readEnvironmentVariable\s*\(\s*'([^']+)'`,
	)
)

// LayerDependencies holds the results of static dependency analysis on
// infrastructure layers.
type LayerDependencies struct {
	// Levels groups layer indices into topological tiers where layers within
	// the same level have no mutual dependencies and can run concurrently.
	Levels [][]int
	// Edges maps each layer index to the indices of layers it directly
	// depends on. A nil or empty slice means the layer has no dependencies.
	Edges map[int][]int
}

// layerDependencyGraph holds the dependency relationships between layers.
type layerDependencyGraph struct {
	// layerCount is the total number of layers.
	layerCount int
	// edges[i] contains the indices of layers that layer i depends on.
	edges map[int][]int
	// outputProviders maps an output variable name to the layer index that
	// produces it.
	outputProviders map[string]int
}

// AnalyzeLayerDependencies performs static analysis on infrastructure layers
// to determine execution levels. Layers that share no dependencies run in the
// same level (concurrently); layers that consume outputs from earlier layers
// are placed in later levels.
//
// It returns an error when a dependency cycle exists.
func AnalyzeLayerDependencies(
	layers []provisioning.Options,
	projectPath string,
	env *environment.Environment,
) (*LayerDependencies, error) {
	if len(layers) == 0 {
		return nil, nil
	}
	if len(layers) == 1 {
		return &LayerDependencies{Levels: [][]int{{0}}}, nil
	}

	// Resolve defaults for each layer up front.
	resolved := make([]provisioning.Options, len(layers))
	for i, layer := range layers {
		r, err := layer.GetWithDefaults()
		if err != nil {
			return nil, fmt.Errorf(
				"resolving defaults for layer %q: %w",
				layer.Name, err,
			)
		}
		resolved[i] = r
	}

	g := &layerDependencyGraph{
		layerCount:      len(resolved),
		edges:           make(map[int][]int),
		outputProviders: make(map[string]int),
	}

	// Phase 1 — Discover outputs from each layer's Bicep file.
	// Iterate layers (not resolved) so the loop index i is clearly bounded by
	// len(layers) for static analyzers; resolved has the same length by
	// construction above.
	for i, layer := range layers {
		opts := resolved[i]
		bicepPath := resolveBicepPath(opts, projectPath)
		outputs, err := extractBicepOutputs(bicepPath)
		if err != nil {
			return nil, fmt.Errorf(
				"extracting outputs for layer %q: %w",
				layer.Name, err,
			)
		}
		for _, name := range outputs {
			if prev, exists := g.outputProviders[name]; exists && prev != i {
				// prev comes from outputProviders, which we populate only with
				// loop indices below. Guard defensively so static analyzers can
				// see the bounded access.
				if prev < 0 || prev >= len(layers) {
					return nil, fmt.Errorf(
						"internal error: invalid layer index %d recorded for output %q",
						prev, name,
					)
				}
				return nil, fmt.Errorf(
					"duplicate output %q: produced by both layer %q and layer %q",
					name, layers[prev].Name, layer.Name,
				)
			}
			g.outputProviders[name] = i
		}
	}

	// Phase 2 — Discover input env-var references and build edges.
	for i, opts := range resolved {
		refs := discoverParamEnvRefs(opts, projectPath)
		for _, ref := range refs {
			// Skip variables already present in the environment. This is an
			// intentional optimization: on re-runs where outputs from a previous
			// deployment already exist in .env, we allow layers to run in parallel
			// using the cached values instead of forcing them to wait for a fresh
			// deployment. If the producing layer's template has changed, it will
			// re-deploy and update the value; if unchanged, it's deployment-state-
			// skipped and the cached value is correct.
			if _, found := env.LookupEnv(ref); found {
				continue
			}
			if provider, ok := g.outputProviders[ref]; ok &&
				provider != i {
				g.edges[i] = append(g.edges[i], provider)
			}
		}
	}

	levels, err := topoSortLevels(g)
	if err != nil {
		return nil, err
	}

	// Deduplicate edges before returning.
	deduped := make(map[int][]int, len(g.edges))
	for node, deps := range g.edges {
		seen := make(map[int]bool, len(deps))
		for _, dep := range deps {
			if !seen[dep] {
				seen[dep] = true
				deduped[node] = append(deduped[node], dep)
			}
		}
	}

	return &LayerDependencies{Levels: levels, Edges: deduped}, nil
}

// resolveBicepPath returns the absolute path to the layer's main Bicep file.
func resolveBicepPath(
	opts provisioning.Options, projectPath string,
) string {
	infraPath := opts.Path
	if !filepath.IsAbs(infraPath) {
		infraPath = filepath.Join(projectPath, infraPath)
	}
	return filepath.Join(infraPath, opts.Module+".bicep")
}

// resolveParamPaths returns the absolute paths for the .bicepparam and
// .parameters.json files associated with a layer.
func resolveParamPaths(
	opts provisioning.Options, projectPath string,
) (bicepparam, parametersJSON string) {
	infraPath := opts.Path
	if !filepath.IsAbs(infraPath) {
		infraPath = filepath.Join(projectPath, infraPath)
	}
	return filepath.Join(infraPath, opts.Module+".bicepparam"),
		filepath.Join(infraPath, opts.Module+".parameters.json")
}

// extractBicepOutputs reads a Bicep file and returns the declared output
// names.
func extractBicepOutputs(bicepFilePath string) ([]string, error) {
	content, err := os.ReadFile(bicepFilePath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", bicepFilePath, err)
	}
	return extractBicepOutputsFromContent(content), nil
}

// extractBicepOutputsFromContent parses Bicep source bytes for output
// declarations and returns their names.
func extractBicepOutputsFromContent(content []byte) []string {
	matches := bicepOutputRe.FindAllSubmatch(content, -1)
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		names = append(names, string(m[1]))
	}
	return names
}

// extractParamEnvRefs extracts environment variable references from a
// parameter file's content. The file extension determines the regex used:
// .bicepparam files use readEnvironmentVariable, all others use ${VAR}.
func extractParamEnvRefs(
	paramFilePath string, content []byte,
) []string {
	var re *regexp.Regexp
	switch filepath.Ext(paramFilePath) {
	case ".bicepparam":
		re = paramReadEnvRe
	default:
		re = paramEnvSubstRe
	}

	matches := re.FindAllSubmatch(content, -1)
	seen := make(map[string]bool)
	var refs []string
	for _, m := range matches {
		name := string(m[1])
		if !seen[name] {
			seen[name] = true
			refs = append(refs, name)
		}
	}
	return refs
}

// discoverParamEnvRefs reads the parameter file for a layer, preferring
// .bicepparam over .parameters.json, and returns env-var references found.
func discoverParamEnvRefs(
	opts provisioning.Options, projectPath string,
) []string {
	bp, pj := resolveParamPaths(opts, projectPath)

	if content, err := os.ReadFile(bp); err == nil {
		return extractParamEnvRefs(bp, content)
	}

	if content, err := os.ReadFile(pj); err == nil {
		return extractParamEnvRefs(pj, content)
	}

	return nil
}

// topoSortLevels performs a topological sort using Kahn's algorithm and
// groups nodes into levels where each level contains layers that can run
// concurrently. Returns an error if a dependency cycle is detected.
func topoSortLevels(g *layerDependencyGraph) ([][]int, error) {
	if g.layerCount == 0 {
		return nil, nil
	}

	// Deduplicate edges, compute in-degree, and build successor map.
	inDeg := make([]int, g.layerCount)
	// successors[a] lists the layers that depend on layer a.
	successors := make(map[int][]int)

	for node := range g.layerCount {
		seen := make(map[int]bool)
		for _, dep := range g.edges[node] {
			if !seen[dep] {
				seen[dep] = true
				inDeg[node]++
				successors[dep] = append(successors[dep], node)
			}
		}
	}

	// Seed the first level with zero in-degree nodes.
	ready := make([]int, 0, g.layerCount)
	for i := range g.layerCount {
		if inDeg[i] == 0 {
			ready = append(ready, i)
		}
	}

	var levels [][]int
	processed := 0

	for len(ready) > 0 {
		levels = append(levels, slices.Clone(ready))
		var next []int
		for _, node := range ready {
			processed++
			for _, succ := range successors[node] {
				inDeg[succ]--
				if inDeg[succ] == 0 {
					next = append(next, succ)
				}
			}
		}
		ready = next
	}

	if processed != g.layerCount {
		return nil, fmt.Errorf(
			"cycle detected in layer dependencies",
		)
	}

	return levels, nil
}
