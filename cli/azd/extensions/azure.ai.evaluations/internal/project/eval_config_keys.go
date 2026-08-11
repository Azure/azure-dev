// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
)

// goTypeInField matches what yaml.KnownFields reports for an unrecognised key:
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

	lines := make([]string, 0, 4)
	for _, line := range strings.Split(text, "\n") {
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
	default:
		return nil
	}

	t := reflect.TypeOf(v)
	keys := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		tag, _, _ := strings.Cut(t.Field(i).Tag.Get("yaml"), ",")
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
