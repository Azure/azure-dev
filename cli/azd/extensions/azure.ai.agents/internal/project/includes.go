// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"azureaiagent/internal/exterrors"
)

// refKey is the include directive key. Any object that contains it is replaced by the loaded
// file, with the object's remaining keys overlaid on top (a shallow, top-level merge).
const refKey = "$ref"

// maxRefDepth bounds nested $ref resolution. Cyclic includes are already rejected by the
// per-chain cycle check; this guards against pathologically deep (but acyclic) chains as a
// defense-in-depth measure.
const maxRefDepth = 32

// remoteRefPattern matches a URL scheme prefix (e.g. https://, file://). Remote $ref targets
// are rejected for now; only local YAML/JSON files are supported.
var remoteRefPattern = regexp.MustCompile(`(?i)^[a-z][a-z0-9+.-]*://`)

// ResolveFileRefs resolves $ref file includes within a Foundry resource service configuration.
//
// In the separate-services azure.yaml shape every Foundry resource is its own service entry, so
// each owning extension calls ResolveFileRefs on its resource's inline map: the entry keys that
// reach the extension over gRPC, where they arrive as a structpb.Struct decoded to
// map[string]any. The core ServiceConfig fields (host, the service key, uses) are stripped by
// core and never appear here. cfg therefore takes any of these shapes:
//
//   - A service-entry-level $ref. The $ref sits at the top level of the inline map, beside the
//     host and service key that core already removed (e.g. an agent or skill entry whose body
//     lives in ./agents/research-agent.yaml). The map itself is the $ref directive.
//   - A deployment array-item $ref. Deployments stay an array on the project service, so each
//     item in deployments may be its own $ref (e.g. ./deployments/gpt-4o.yaml).
//   - Any nested $ref reached while walking the entry (a $ref inside a loaded file, or a sibling
//     value), since resolution is recursive over every map and sequence node.
//
// projectRoot is the directory that holds azure.yaml; relative $ref targets at the top level
// resolve against it, and rebased project/instructions paths are anchored to it.
//
// Any object that contains a "$ref" string is replaced by the referenced YAML or JSON file,
// with the object's remaining keys overlaid on top. The overlay is a shallow, top-level merge:
// sibling scalars, arrays, and objects each replace the loaded value wholesale. Nested $ref
// directives inside a loaded file resolve relative to that file's own directory, and that
// file's relative project, instructions, and $ref paths are rebased so the returned config has
// every path anchored to projectRoot in clean, forward-slash form. Inline path values authored
// directly in azure.yaml are left exactly as written.
//
// $ref values that are URLs are rejected (remote includes are not supported yet); only local
// relative or absolute file paths are accepted. Cyclic and excessively deep include chains
// return an error rather than looping. $ref targets are treated as trusted input, the same
// trust level as azure.yaml itself.
func ResolveFileRefs(cfg map[string]any, projectRoot string) (map[string]any, error) {
	if cfg == nil {
		return nil, nil
	}

	root := filepath.Clean(projectRoot)
	resolved, err := resolveValue(cfg, root, root, nil)
	if err != nil {
		return nil, err
	}

	out, ok := resolved.(map[string]any)
	if !ok {
		// resolveValue always returns a map for a map input; this is unreachable in practice.
		return nil, exterrors.Internal(exterrors.CodeInvalidFileRef, "resolved Foundry config is not a mapping")
	}
	return out, nil
}

// resolveValue recursively resolves $ref includes in v. baseDir is the directory of the file
// that holds v (so relative $ref and path values resolve correctly); projectRoot is the
// azure.yaml directory used to re-anchor rebased paths; chain holds the absolute paths of the
// $ref files currently being resolved, for cycle detection.
func resolveValue(v any, baseDir, projectRoot string, chain []string) (any, error) {
	switch typed := v.(type) {
	case map[string]any:
		if _, isRef := typed[refKey]; isRef {
			return resolveRef(typed, baseDir, projectRoot, chain)
		}

		out := make(map[string]any, len(typed))
		for key, child := range typed {
			resolvedChild, err := resolveMapEntry(key, child, baseDir, projectRoot, chain)
			if err != nil {
				return nil, err
			}
			out[key] = resolvedChild
		}
		return out, nil
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			resolvedItem, err := resolveValue(item, baseDir, projectRoot, chain)
			if err != nil {
				return nil, err
			}
			out[i] = resolvedItem
		}
		return out, nil
	default:
		return v, nil
	}
}

// resolveMapEntry resolves a single key/value of a mapping and rebases it when the key is a
// path-bearing field (project, instructions) loaded from a $ref file. Inline values authored
// directly in azure.yaml (baseDir == projectRoot) are left untouched.
func resolveMapEntry(key string, child any, baseDir, projectRoot string, chain []string) (any, error) {
	resolvedChild, err := resolveValue(child, baseDir, projectRoot, chain)
	if err != nil {
		return nil, err
	}

	if value, ok := resolvedChild.(string); ok && baseDir != projectRoot && isPathKey(key, value) {
		return rebasePath(value, baseDir, projectRoot), nil
	}
	return resolvedChild, nil
}

// resolveRef loads the file named by directive[refKey], resolves it relative to its own
// directory, then overlays the directive's sibling keys on top of the loaded object.
func resolveRef(directive map[string]any, baseDir, projectRoot string, chain []string) (any, error) {
	ref, ok := directive[refKey].(string)
	if !ok {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidFileRef,
			fmt.Sprintf("%s value must be a string, got %T", refKey, directive[refKey]),
			fmt.Sprintf("Set %s to a relative or absolute path to a YAML or JSON file.", refKey),
		)
	}

	target, err := refTargetPath(ref, baseDir)
	if err != nil {
		return nil, err
	}

	if slices.Contains(chain, target) {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidFileRef,
			fmt.Sprintf("cyclic %s include detected at %q", refKey, target),
			"Remove the circular reference between the included files.",
		)
	}
	if len(chain) >= maxRefDepth {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidFileRef,
			fmt.Sprintf("%s include nesting exceeds %d levels at %q", refKey, maxRefDepth, target),
			"Reduce the depth of nested $ref includes.",
		)
	}

	loaded, err := loadRefFile(target)
	if err != nil {
		return nil, err
	}

	nextChain := append(slices.Clone(chain), target)
	resolvedLoaded, err := resolveValue(loaded, filepath.Dir(target), projectRoot, nextChain)
	if err != nil {
		return nil, err
	}

	out, ok := resolvedLoaded.(map[string]any)
	if !ok {
		// loadRefFile guarantees a mapping, so resolveValue returns a map; unreachable in practice.
		return nil, exterrors.Validation(
			exterrors.CodeInvalidFileRef,
			fmt.Sprintf("%s file %q must contain a YAML or JSON object", refKey, target),
			"The referenced file must define an object, not a list or scalar.",
		)
	}

	// Overlay sibling keys. They are authored in the file that holds the directive, so they
	// resolve and rebase against baseDir, not the loaded file's directory.
	for key, child := range directive {
		if key == refKey {
			continue
		}
		resolvedChild, err := resolveMapEntry(key, child, baseDir, projectRoot, chain)
		if err != nil {
			return nil, err
		}
		out[key] = resolvedChild
	}

	return out, nil
}

// refTargetPath turns a $ref value into an absolute, cleaned local file path. Relative paths
// resolve against baseDir. URLs are rejected.
func refTargetPath(ref, baseDir string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", exterrors.Validation(
			exterrors.CodeInvalidFileRef,
			fmt.Sprintf("%s value must not be empty", refKey),
			"Set $ref to a relative or absolute path to a YAML or JSON file.",
		)
	}
	if remoteRefPattern.MatchString(ref) {
		return "", exterrors.Validation(
			exterrors.CodeInvalidFileRef,
			fmt.Sprintf("%s %q is a URL; remote includes are not supported yet", refKey, ref),
			"Use a local file path. Download the file and reference it by a relative or absolute path.",
		)
	}
	if filepath.IsAbs(ref) {
		return filepath.Clean(ref), nil
	}
	return filepath.Join(baseDir, ref), nil
}

// loadRefFile reads and parses a referenced YAML or JSON file into a mapping. JSON parses as a
// subset of YAML, so a single decoder handles both.
func loadRefFile(path string) (map[string]any, error) {
	// #nosec G304 -- $ref targets are trusted config input, the same trust level as azure.yaml
	// itself (design spec §2.4 treats includes as trusted input).
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidFileRef,
			fmt.Sprintf("cannot read %s file %q: %v", refKey, path, err),
			"Check that the path is correct and the file exists and is readable.",
		)
	}

	var out map[string]any
	if err := yaml.Unmarshal(data, &out); err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidFileRef,
			fmt.Sprintf("%s file %q is not a valid YAML or JSON object: %v", refKey, path, err),
			"Fix the file so it parses as a YAML or JSON object.",
		)
	}
	if out == nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidFileRef,
			fmt.Sprintf("%s file %q is empty or not a mapping", refKey, path),
			"The referenced file must contain a YAML or JSON object.",
		)
	}
	return out, nil
}

// isPathKey reports whether a string value for key is a filesystem path that should be rebased.
// project is always a path. instructions is a path only when it looks like one (a single-line
// .md or .txt reference); otherwise it is inline prose and must be left untouched.
func isPathKey(key, value string) bool {
	switch key {
	case "project":
		return value != ""
	case "instructions":
		return looksLikeInstructionsPath(value)
	default:
		return false
	}
}

// looksLikeInstructionsPath reports whether an instructions value is a file path rather than
// inline prose: a single line ending in .md or .txt.
func looksLikeInstructionsPath(value string) bool {
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(value))
	return strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".txt")
}

// rebasePath re-anchors a relative path that was authored relative to baseDir so it becomes
// relative to projectRoot, in clean forward-slash form. Empty values, URLs, and absolute paths
// are returned unchanged.
func rebasePath(value, baseDir, projectRoot string) string {
	if value == "" || remoteRefPattern.MatchString(value) || filepath.IsAbs(value) {
		return value
	}

	abs := filepath.Join(baseDir, value)
	rel, err := filepath.Rel(projectRoot, abs)
	if err != nil {
		// Different Windows volumes (or otherwise unrelated paths): fall back to the absolute path.
		return filepath.ToSlash(abs)
	}
	return filepath.ToSlash(rel)
}
