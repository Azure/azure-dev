// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"

	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
)

// Package-level compiled regexes for output and env-var reference extraction.
var (
	bicepOutputRe   = regexp.MustCompile(`(?m)^\s*output\s+(\w+)\s+`)
	paramEnvSubstRe = regexp.MustCompile(`\$\{(\w+)\}`)
	paramReadEnvRe  = regexp.MustCompile(
		`readEnvironmentVariable\s*\(\s*'([^']+)'`,
	)
	// paramReadEnvAnyRe matches every readEnvironmentVariable(...) call
	// regardless of argument shape. Used to detect non-literal arguments
	// (e.g. readEnvironmentVariable(varName)) that the strict regex above
	// silently drops.
	paramReadEnvAnyRe = regexp.MustCompile(`readEnvironmentVariable\s*\(`)
	// armExpressionRe matches ARM-style template expressions inside
	// .parameters.json values (e.g. "[parameters('foo')]"). When present,
	// a literal env-var scan cannot prove the absence of cross-layer
	// references, so the consuming layer must fall back to depending on
	// all earlier layers.
	//
	// The body uses [^\]] (not [^"]) so that ARM expressions containing
	// escaped quotes inside string literals — e.g.
	// "[json('{\"a\":\"b\"}')]" — are still detected. This is a
	// heuristic, not a parser: an ARM expression whose payload contains
	// a literal "]" inside a single-quoted string (e.g.
	// "[json('[1,2]')]") will tokenize wrong. The bias is acceptable
	// because the only failure mode is a missed match → no detected
	// edge → potentially missing parallel-safety guarantee, which is
	// the same risk the rest of the analyzer already carries. Any
	// downstream silent-miss would still surface as a parameter-time
	// error from Bicep itself.
	armExpressionRe = regexp.MustCompile(`"\s*\[[^\]]+\]\s*"`)
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
	// SafeFallbackLayers holds the indices of layers that triggered the
	// safe-by-default detector fallback — i.e., the parser encountered a
	// syntax pattern it could not resolve to a literal env-var name and
	// the layer was forced to depend on all earlier layers. This is
	// surfaced for telemetry and diagnostics; the fallback edges are
	// already reflected in [LayerDependencies.Edges].
	SafeFallbackLayers []int
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
	var safeFallback []int
	for i, opts := range resolved {
		refs, hasUnknown := discoverParamEnvRefs(opts, projectPath)
		for _, ref := range refs {
			if provider, ok := g.outputProviders[ref]; ok && provider != i {
				// Always keep intra-graph edges, even when the ref is
				// already in the environment from a previous run. The
				// cached value may be stale if the producer's template
				// has changed; dropping the edge lets the consumer fan
				// out in parallel with the producer and consume stale
				// data. Unchanged producers still go through the fast
				// deployment-state-skipped path, so the serial cost is
				// near-zero.
				g.edges[i] = append(g.edges[i], provider)
				continue
			}
			// Refs with no in-graph producer are externally supplied
			// (from the environment) or genuinely unresolved. Either
			// way they don't add a DAG edge.
		}
		// Safe-by-default fallback: if the parser encountered a syntax
		// pattern it could not resolve to a literal env-var name (e.g.
		// readEnvironmentVariable(myVar) in .bicepparam, or an ARM
		// template expression like [parameters('foo')] in
		// .parameters.json), assume this layer may consume any earlier
		// layer's outputs and add edges to all earlier layers. This
		// trades parallelism for correctness on under-analyzed inputs.
		if hasUnknown {
			safeFallback = append(safeFallback, i)
			for j := range i {
				g.edges[i] = append(g.edges[i], j)
			}
		}
	}

	// Phase 3 — Apply explicit dependsOn declarations from azure.yaml.
	// These act as an author-controlled override for hook-mediated edges
	// (e.g. a postprovision hook in layer A writes an env var that layer
	// B's bicepparam reads at provision time) that no static analyzer
	// can discover. Explicit edges union with detected edges; cycles are
	// caught downstream by the topological sort.
	nameToIndex := make(map[string]int, len(layers))
	for i, layer := range layers {
		if layer.Name != "" {
			nameToIndex[layer.Name] = i
		}
	}
	for i, layer := range layers {
		for _, depName := range layer.DependsOn {
			depIdx, ok := nameToIndex[depName]
			if !ok {
				return nil, fmt.Errorf(
					"layer %q dependsOn unknown layer %q",
					layer.Name, depName,
				)
			}
			if depIdx == i {
				return nil, fmt.Errorf(
					"layer %q cannot depend on itself", layer.Name,
				)
			}
			g.edges[i] = append(g.edges[i], depIdx)
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

	return &LayerDependencies{Levels: levels, Edges: deduped, SafeFallbackLayers: safeFallback}, nil
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
//
// hasUnknown is true when the file contains a syntax pattern the parser
// cannot resolve to a literal env-var name (e.g. a non-literal
// readEnvironmentVariable argument or an ARM template expression). When
// hasUnknown is true the caller must apply a safe-by-default fallback
// (depend on all earlier layers) because the returned refs may be
// incomplete.
func extractParamEnvRefs(
	paramFilePath string, content []byte,
) (refs []string, hasUnknown bool) {
	var re *regexp.Regexp
	switch filepath.Ext(paramFilePath) {
	case ".bicepparam":
		re = paramReadEnvRe
		// A literal-only regex would silently drop calls like
		// readEnvironmentVariable(myVar). Compare the count of any
		// readEnvironmentVariable( call against the count of literal
		// matches: a mismatch means at least one non-literal call exists.
		anyCount := len(paramReadEnvAnyRe.FindAllIndex(content, -1))
		litCount := len(paramReadEnvRe.FindAllSubmatchIndex(content, -1))
		if anyCount > litCount {
			hasUnknown = true
		}
	default:
		re = paramEnvSubstRe
		// ARM expressions like "[parameters('foo')]" can reference
		// arbitrary deployment-time values that bypass the ${VAR}
		// substitution syntax. Their presence means the literal scan
		// is incomplete.
		if armExpressionRe.Match(content) {
			hasUnknown = true
		}
	}

	matches := re.FindAllSubmatch(content, -1)
	seen := make(map[string]bool)
	for _, m := range matches {
		name := string(m[1])
		if !seen[name] {
			seen[name] = true
			refs = append(refs, name)
		}
	}
	return refs, hasUnknown
}

// extractBicepParamReadEnvRefs scans a .bicep file for env-var references
// inside param defaults like `param x string = readEnvironmentVariable('Y')`.
// It mirrors the .bicepparam parser: it returns literal refs and a
// hasUnknown flag for non-literal calls.
func extractBicepParamReadEnvRefs(
	content []byte,
) (refs []string, hasUnknown bool) {
	matches := paramReadEnvRe.FindAllSubmatch(content, -1)
	seen := make(map[string]bool)
	for _, m := range matches {
		name := string(m[1])
		if !seen[name] {
			seen[name] = true
			refs = append(refs, name)
		}
	}
	anyCount := len(paramReadEnvAnyRe.FindAllIndex(content, -1))
	litCount := len(paramReadEnvRe.FindAllSubmatchIndex(content, -1))
	if anyCount > litCount {
		hasUnknown = true
	}
	return refs, hasUnknown
}

// discoverParamEnvRefs reads the parameter file for a layer, preferring
// .bicepparam over .parameters.json, and returns env-var references found.
// It also scans the layer's main .bicep file for readEnvironmentVariable
// calls in param defaults — those are silently missed when only parameter
// files are inspected. hasUnknown is true if any source contains a
// non-literal env-var reference.
func discoverParamEnvRefs(
	opts provisioning.Options, projectPath string,
) (refs []string, hasUnknown bool) {
	bp, pj := resolveParamPaths(opts, projectPath)

	merge := func(more []string, moreUnknown bool) {
		seen := make(map[string]bool, len(refs))
		for _, r := range refs {
			seen[r] = true
		}
		for _, r := range more {
			if !seen[r] {
				seen[r] = true
				refs = append(refs, r)
			}
		}
		if moreUnknown {
			hasUnknown = true
		}
	}

	if content, err := os.ReadFile(bp); err == nil {
		r, u := extractParamEnvRefs(bp, content)
		merge(r, u)
	} else if !os.IsNotExist(err) {
		// A read error (permission, I/O) is NOT the same as "file does
		// not exist". We can't prove what refs the file would have had,
		// so escalate to the safe-by-default fallback.
		hasUnknown = true
	} else if content, err := os.ReadFile(pj); err == nil {
		r, u := extractParamEnvRefs(pj, content)
		merge(r, u)
	} else if !os.IsNotExist(err) {
		hasUnknown = true
	}

	// Always scan the .bicep file for param defaults that call
	// readEnvironmentVariable. These are independent of the param file
	// and silently dropped when only parameter files are inspected.
	if bicepContent, err := os.ReadFile(
		resolveBicepPath(opts, projectPath),
	); err == nil {
		r, u := extractBicepParamReadEnvRefs(bicepContent)
		merge(r, u)
	} else if !os.IsNotExist(err) {
		hasUnknown = true
	}

	return refs, hasUnknown
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
