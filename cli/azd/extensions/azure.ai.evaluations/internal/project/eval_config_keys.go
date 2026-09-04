// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"azureaieval/internal/pkg/evalcore"
)

// goTypeInField matches what yaml.KnownFields reports for an unrecognized key:
// `line 7: field evaulators not found in type project.Eval`. The Go type is an
// implementation detail, and the reader is editing a YAML file by hand, which
// is the documented way to use one.
var goTypeInField = regexp.MustCompile(`field (\S+) not found in type (\S+)`)

// explainUnknownKeys rewrites a decode failure into the file's own vocabulary,
// naming the near-miss when there is one.
func explainUnknownKeys(err error) error {
	text := err.Error()
	if !goTypeInField.MatchString(text) {
		return err
	}

	// Nothing at the top level was recognized, so this is another tool's file
	// rather than a typo in one of ours. `azd ai agent eval` writes an eval.yaml
	// of its own, and suggesting a near-miss for each of its keys in turn would
	// walk the reader into rewriting it a line at a time.
	if topLevelKeysAllUnknown(text) {
		return errUnrecognizedEvalConfig
	}

	lines := make([]string, 0, 4)
	for line := range strings.SplitSeq(text, "\n") {
		m := goTypeInField.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		key, goType := m[1], m[2]
		rewritten := fmt.Sprintf("unknown key %q", key)
		if near := nearestKey(key, keysOfType(goType)); near != "" {
			rewritten += fmt.Sprintf(`; did you mean %q?`, near)
		}
		// The yaml prefix carries the line number, which is the useful half.
		if prefix, _, ok := strings.Cut(strings.TrimSpace(line), ": field "); ok {
			rewritten = prefix + ": " + rewritten
		}
		lines = append(lines, rewritten)
	}
	if len(lines) == 0 {
		return err
	}
	return fmt.Errorf("%s", strings.Join(lines, "\n"))
}

var errUnrecognizedEvalConfig = errors.New(
	"none of this file's top-level keys are ones an eval configuration declares, " +
		"so this is not one. `azd ai agent eval` writes an eval.yaml of its own with " +
		"a different shape, and runs it with `azd ai agent eval run`")

// topLevelKeysAllUnknown reports a file whose top-level shape is not this one.
//
// Only top-level rejections count. A nested one does not disqualify the check:
// another tool's file can still have a key named like one of ours, and the
// mismatch inside it then reports against the nested type rather than the
// config. The threshold is the number of keys a configuration declares, so one
// stray key beside recognized ones stays a typo.
func topLevelKeysAllUnknown(text string) bool {
	known := keysOfType("project.EvalConfig")
	rejected := 0
	for line := range strings.SplitSeq(text, "\n") {
		m := goTypeInField.FindStringSubmatch(line)
		if m == nil || m[2] != "project.EvalConfig" {
			continue
		}
		rejected++
	}
	return rejected > 0 && rejected >= len(known)
}

// keysOfType lists the YAML keys a declaration accepts.
func keysOfType(goType string) []string {
	var v any
	switch goType {
	case "project.EvalConfig":
		v = EvalConfig{}
	case "project.Eval":
		v = Eval{}
	case "project.DatasetDecl":
		v = DatasetDecl{}
	case "project.EvaluatorDecl":
		v = EvaluatorDecl{}
	case "project.Target":
		v = Target{}
	case "project.SourceDecl":
		v = SourceDecl{}
	case "evalcore.EvaluatorRef":
		v = evalcore.EvaluatorRef{}
	default:
		return nil
	}

	t := reflect.TypeOf(v)
	keys := make([]string, 0, t.NumField())
	for field := range t.Fields() {
		tag, _, _ := strings.Cut(field.Tag.Get("yaml"), ",")
		if tag != "" && tag != "-" {
			keys = append(keys, tag)
		}
	}
	return keys
}

// nearestKey returns the closest known key, or "" when nothing is close enough
// to be worth suggesting. A third of the length is the budget, so `evaulators`
// finds `evaluators` while `banana` suggests nothing.
func nearestKey(key string, known []string) string {
	best, bestDist := "", len(key)/3+1
	for _, k := range known {
		if d := editDistance(key, k); d < bestDist {
			best, bestDist = k, d
		}
	}
	return best
}

// editDistance is Levenshtein over two short identifiers.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(min(curr[j-1]+1, prev[j]+1), prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}
