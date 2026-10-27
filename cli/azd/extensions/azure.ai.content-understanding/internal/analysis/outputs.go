// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package analysis

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"unicode/utf8"

	"azure.ai.content-understanding/internal/localio"
)

type OutputOptions struct {
	File, Directory, Report, Existing string
	LLMInput                          bool
}

type Output struct {
	Input   Input
	Path    *string
	Exists  bool
	Skipped bool
}

type Plan struct {
	Outputs  []Output
	Existing string
	Report   string
	LLMInput bool
}

type DryRunOutput struct {
	Input   string  `json:"input"`
	Output  *string `json:"output"`
	Exists  bool    `json:"exists"`
	Skipped bool    `json:"skipped"`
}

type DryRun struct {
	DryRun   bool           `json:"dryRun"`
	Analyzer string         `json:"analyzerId"`
	Outputs  []DryRunOutput `json:"outputs"`
}

func (p Plan) Preview(analyzer string) DryRun {
	result := DryRun{DryRun: true, Analyzer: analyzer, Outputs: make([]DryRunOutput, len(p.Outputs))}
	for index, output := range p.Outputs {
		result.Outputs[index] = DryRunOutput{
			Input: output.Input.Reference, Output: output.Path, Exists: output.Exists, Skipped: output.Skipped,
		}
	}
	return result
}

func PlanOutputs(inputs []Input, options OutputOptions) (Plan, error) {
	plan := Plan{Existing: options.Existing, Report: options.Report, LLMInput: options.LLMInput}
	if len(inputs) == 0 {
		return Plan{}, invalid("empty_inputs", "Select at least one analysis input.")
	}
	if !slices.Contains([]string{"error", "skip", "reanalyze"}, options.Existing) {
		return Plan{}, invalid("invalid_existing_policy", "--on-existing must be error, skip, or reanalyze.")
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		return Plan{}, invalid("invalid_working_directory", "The current working directory could not be resolved.")
	}
	keyFor := func(path string) string { return pathKey(path, workingDirectory) }
	if options.File != "" && (options.Directory != "" || len(inputs) != 1) {
		return Plan{}, invalid("invalid_output_file",
			"--output-file requires one input and cannot be combined with --output-dir.")
	}
	suffix := ".result.json"
	if options.LLMInput {
		suffix = ".result.md"
	}
	destinations := make(map[string][]int)
	reserved := make(map[string]bool)
	for index, input := range inputs {
		var destination *string
		switch {
		case options.File != "":
			destination = new(filepath.Clean(options.File))
		case options.Directory != "":
			if !filepath.IsLocal(input.Relative) {
				return Plan{}, invalid("invalid_relative_output",
					"Source-relative result paths must stay within the output directory.")
			}
			path := filepath.Join(options.Directory, input.Relative) + suffix
			if input.URL != "" && len(filepath.Base(path)) > 240 {
				return Plan{}, invalid("output_name_too_long",
					"Remote result names must fit in 240 UTF-8 bytes; use --output-file.")
			}
			destination = new(path)
		case len(inputs) == 1:
			destination = nil
		case input.Path != "":
			destination = new(input.Path + suffix)
		default:
			return Plan{}, invalid("missing_output_directory", "Multiple inputs containing a URL require --output-dir.")
		}
		plan.Outputs = append(plan.Outputs, Output{Input: input, Path: destination})
		if destination != nil {
			key := keyFor(*destination)
			destinations[key] = append(destinations[key], index)
			reserved[key] = true
		}
	}
	// Resolve every local collision, including collisions with an otherwise unique generated name.
	var collisions []int
	for _, indexes := range destinations {
		if len(indexes) < 2 {
			continue
		}
		for _, index := range indexes {
			if plan.Outputs[index].Input.URL != "" {
				return Plan{}, invalid("remote_output_collision",
					"Remote inputs share a result name; use separate --output-file paths.")
			}
			collisions = append(collisions, index)
		}
	}
	slices.SortFunc(collisions, func(left, right int) int {
		return strings.Compare(plan.Outputs[left].Input.Reference, plan.Outputs[right].Input.Reference)
	})
	for _, index := range collisions {
		output := &plan.Outputs[index]
		digest := sha256.Sum256([]byte(output.Input.Reference))
		hash := hex.EncodeToString(digest[:])
		original := *output.Path
		resolved := false
		for length := 8; length <= len(hash); length += 8 {
			tail := "." + hash[:length] + suffix
			name := strings.TrimSuffix(filepath.Base(original), suffix)
			if len(name) > 240-len(tail) {
				name = name[:240-len(tail)]
				for !utf8.ValidString(name) {
					name = name[:len(name)-1]
				}
			}
			candidate := filepath.Join(filepath.Dir(original), name+tail)
			key := keyFor(candidate)
			if !reserved[key] {
				reserved[key] = true
				output.Path = new(candidate)
				resolved = true
				break
			}
		}
		if !resolved {
			return Plan{}, invalid("output_collision", "Result filename collisions could not be resolved.")
		}
	}
	for index := range plan.Outputs {
		output := &plan.Outputs[index]
		if output.Path == nil {
			continue
		}
		exists, err := localio.Check(*output.Path)
		if err != nil {
			return Plan{}, invalid("unsafe_output_path", "A result path is inaccessible, a symlink, or not a regular file.")
		}
		if AliasesInput(*output.Path, inputs, workingDirectory) {
			return Plan{}, invalid("output_overwrites_input", "A result path must not overwrite any analysis input.")
		}
		output.Exists = exists
		output.Skipped = exists && options.Existing == "skip"
		if exists && options.Existing == "error" {
			return Plan{}, invalid("result_exists", "A result file already exists; use --on-existing skip or reanalyze.")
		}
	}
	if options.Report != "" {
		exists, err := localio.Check(options.Report)
		if err != nil || exists || AliasesInput(options.Report, inputs, workingDirectory) {
			return Plan{}, invalid("invalid_report_path",
				"The report requires a new regular file path that is not an input.")
		}
		for _, output := range plan.Outputs {
			if output.Path != nil && keyFor(*output.Path) == keyFor(options.Report) {
				return Plan{}, invalid("report_output_collision", "The report path must differ from every result path.")
			}
		}
	}
	// A planned file may not be an ancestor of another output or of the report.
	paths := []string{}
	for _, output := range plan.Outputs {
		if output.Path != nil {
			paths = append(paths, keyFor(*output.Path))
		}
	}
	if plan.Report != "" {
		paths = append(paths, keyFor(plan.Report))
	}
	slices.Sort(paths)
	for index, path := range paths {
		for _, other := range paths[index+1:] {
			if strings.HasPrefix(other, path+string(filepath.Separator)) {
				return Plan{}, invalid("output_parent_collision",
					"A planned output file cannot be another output's parent directory.")
			}
		}
	}
	return plan, nil
}

func pathKey(path, workingDirectory string) string {
	absolute := filepath.Clean(path)
	if !filepath.IsAbs(absolute) {
		absolute = filepath.Join(workingDirectory, absolute)
	}
	if runtime.GOOS == "windows" {
		return strings.ToLower(absolute)
	}
	return absolute
}

// AliasesInput detects lexical and physical aliases of discovered local files.
func AliasesInput(path string, inputs []Input, workingDirectory string) bool {
	info, err := os.Stat(path)
	for _, input := range inputs {
		if input.Path != "" && (pathKey(path, workingDirectory) == pathKey(input.Path, workingDirectory) ||
			(err == nil && input.identity != nil && os.SameFile(info, input.identity))) {
			return true
		}
	}
	return false
}
