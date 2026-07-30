// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package evalcore

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// pythonExt is the extension a code evaluator's source carries. It is also
// what tells a code evaluator apart from a rubric, which is `.json`.
const pythonExt = ".py"

// GradeFunctionName is the function the executor calls.
//
// A code evaluator runs as an OpenAI python grader, and the grader contract is
// a single string of source with one entry point: a top-level
// `grade(sample, item)` returning a float. There is no module, package, or
// import path, so nothing else in the file can be reached.
const GradeFunctionName = "grade"

// CodeEvaluatorScript is a validated evaluator script ready to publish.
type CodeEvaluatorScript struct {
	// Name is the evaluator name the script is published under.
	Name string
	// Path is where the script was read from, kept for error messages.
	Path string
	// Source is the whole file, which is what the service is sent. The grader
	// takes source, not a location: there is nowhere for a second file to go.
	Source string
}

// IsCodeEvaluatorSource reports whether a declared `source:` names a code
// evaluator rather than a rubric.
//
// The decision is made from the extension alone and never touches the
// filesystem, so it answers the same for a path that has not been created yet
// — a config can be validated before the file it names exists.
func IsCodeEvaluatorSource(path string) bool {
	return strings.EqualFold(filepath.Ext(path), pythonExt)
}

// gradeDeclaration matches a top-level `def grade(` — optionally async, and
// anchored at column zero.
//
// Indentation is what makes this specific rather than a substring search: a
// `grade` nested inside a class is a method, and the grader only ever calls a
// module-level function, so an indented match would pass validation here and
// then fail at run time with "top-level grade() function not found".
var gradeDeclaration = regexp.MustCompile(
	`(?m)^(?:async[ \t]+)?def[ \t]+` + regexp.QuoteMeta(GradeFunctionName) + `[ \t]*\(`)

// LoadCodeEvaluator reads an evaluator script and checks it against the grader
// contract.
//
// The check is done here rather than left to the service because the service
// only discovers a missing entry point when a run executes — long after a
// version has been published and an eval bound to it. The failure it reports
// then is "Invalid grader source: top-level grade() function not found in
// source", which names neither the file nor the evaluator.
func LoadCodeEvaluator(name, path string) (*CodeEvaluatorScript, error) {
	if name == "" {
		return nil, fmt.Errorf("an evaluator name is required to publish %q", path)
	}
	if path == "" {
		return nil, fmt.Errorf("evaluator %q has no source file to publish", name)
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("reading evaluator source %q: %w", path, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf(
			"evaluator source %q is a directory. A code evaluator is a single %s file: "+
				"it is published as the source of a python grader, which takes one script "+
				"and cannot import a helper module beside it", path, pythonExt)
	}
	if !IsCodeEvaluatorSource(path) {
		return nil, fmt.Errorf(
			"evaluator source %q must be a %s file", path, pythonExt)
	}

	source, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading evaluator source %q: %w", path, err)
	}
	if len(strings.TrimSpace(string(source))) == 0 {
		return nil, fmt.Errorf("evaluator source %q is empty", path)
	}
	if !gradeDeclaration.Match(source) {
		return nil, fmt.Errorf(
			"%s does not declare a top-level %s(sample, item) function. A code evaluator "+
				"runs as a python grader, which calls exactly that and nothing else — a "+
				"class, a differently named function, or one nested inside another will "+
				"not be found. For example:\n\ndef %s(sample, item) -> float:\n"+
				"    return float(len(item.get(\"response\", \"\")))\n\n"+
				"The script must also be self-contained: only the standard library and "+
				"whatever the image named by --image-tag provides are importable, so a "+
				"helper file next to it cannot be imported",
			path, GradeFunctionName, GradeFunctionName)
	}

	return &CodeEvaluatorScript{
		Name:   name,
		Path:   path,
		Source: string(source),
	}, nil
}
