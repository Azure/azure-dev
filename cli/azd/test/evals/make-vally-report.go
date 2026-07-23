// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Generates some simple reports to drag in the typical things you need to troubleshoot, like
// the tool calls made, the skills that were loaded, and the response from the LLM.

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

type detail struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
}

type gradeResult struct {
	Passed  bool     `json:"passed"`
	Details []detail `json:"details"`
}

type trajectory struct {
	Output      string   `json:"output"`
	SessionPath *string  `json:"sessionPath"`
	Metadata    metadata `json:"metadata"`
	Events      []event  `json:"events"`
}

type metadata struct {
	SkillsLoaded []string `json:"skillsLoaded"`
}

type event struct {
	Type string         `json:"type"`
	Data map[string]any `json:"data"`
}

type vallyRecord struct {
	Stimulus string `json:"stimulus"`
	Model    string `json:"model"`

	// ExperimentVariant identifies which experiment cell (from vally's experiment) produced
	// the record. It is "<axis>=<value>" for an experiment run (e.g. "skills=with-skills",
	// "skills=without-skills", "model=claude-sonnet-4.6") and "main" for a plain
	// eval run that has no experiment axes.
	ExperimentVariant string `json:"variant"`

	TrialIndex  int         `json:"trialIndex"`
	TotalTrials int         `json:"totalTrials"`
	GradeResult gradeResult `json:"gradeResult"`
	Trajectory  trajectory  `json:"trajectory"`
}

type indexedRecord struct {
	lineNumber int
	sourcePath string
	record     vallyRecord
}

type reportEntry struct {
	lineNumber        int
	sourcePath        string
	passed            bool
	stimulus          string
	model             string
	experimentVariant string
	trialIndex        int
	totalTrials       int
	checks            []detail
	skillsLoaded      []string
	toolCalls         []string
	output            string
	sessionPath       string
}

func toWorkspaceRelative(p string) string {
	if p == "" {
		return p
	}

	if !filepath.IsAbs(p) {
		return filepath.ToSlash(p)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return filepath.ToSlash(p)
	}

	rel, err := filepath.Rel(cwd, p)
	if err != nil {
		return filepath.ToSlash(p)
	}

	if rel != "" && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}

	return filepath.ToSlash(p)
}

func loadRecords(resultsPath string) ([]indexedRecord, error) {
	file, err := os.Open(resultsPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), 10*1024*1024)

	records := []indexedRecord{}
	lineNumber := 0

	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var rec vallyRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			return nil, fmt.Errorf("failed to unmarshal JSON line %q from file %q: %w", line, resultsPath, err)
		}

		records = append(records, indexedRecord{lineNumber: lineNumber, sourcePath: resultsPath, record: rec})
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return records, nil
}

// loadResults loads and concatenates records from multiple results.jsonl
// files, preserving each record's source path for accurate deep links.
func loadResults(resultsJSONLFiles []string) ([]indexedRecord, error) {
	var all []indexedRecord

	for _, path := range resultsJSONLFiles {
		recs, err := loadRecords(path)
		if err != nil {
			return nil, err
		}
		all = append(all, recs...)
	}

	return all, nil
}

func filteredEntries(records []indexedRecord) (passed []reportEntry, failed []reportEntry) {
	for _, indexed := range records {
		rec := indexed.record

		var checks []detail

		for _, d := range rec.GradeResult.Details {
			name := d.Name
			if name == "" {
				name = "unknown"
			}
			checks = append(checks, detail{Name: name, Passed: d.Passed})
		}

		sessionPath := ""

		if rec.Trajectory.SessionPath != nil {
			sessionPath = *rec.Trajectory.SessionPath
		}

		re := reportEntry{
			lineNumber:        indexed.lineNumber,
			sourcePath:        indexed.sourcePath,
			passed:            rec.GradeResult.Passed,
			stimulus:          nonEmpty(rec.Stimulus, "unknown-stimulus"),
			model:             nonEmpty(rec.Model, "unknown-model"),
			experimentVariant: rec.ExperimentVariant,
			trialIndex:        max(rec.TrialIndex, 0),
			totalTrials:       max(rec.TotalTrials, 0),
			checks:            checks,
			skillsLoaded:      normalizeSkills(rec.Trajectory.Metadata.SkillsLoaded),
			toolCalls:         extractToolCalls(rec.Trajectory.Events),
			output:            rec.Trajectory.Output,
			sessionPath:       sessionPath,
		}

		if rec.GradeResult.Passed {
			passed = append(passed, re)
		} else {
			failed = append(failed, re)
		}
	}

	return
}

func buildReport(entries []reportEntry, totalEntries int, headerPath, sessionLogsPath string, passed bool) string {
	runDir := headerPath
	resultsLabel := "Results file"
	if strings.HasSuffix(headerPath, ".jsonl") {
		runDir = filepath.Dir(headerPath)
	} else {
		resultsLabel = "Results folder"
	}
	relResults := toWorkspaceRelative(headerPath)
	relRunDir := toWorkspaceRelative(nonEmpty(sessionLogsPath, runDir))
	title := "# Vally Failed Responses"
	countLabel := "Failed trials"
	noEntriesMessage := "No failed trials in this run."
	entryPrefix := "FAIL"
	detailsHeading := "### Failure Details"

	if passed {
		title = "# Vally Success Responses"
		countLabel = "Successful trials"
		noEntriesMessage = "No successful trials in this run."
		entryPrefix = "PASS"
		detailsHeading = "### Success Details"
	}

	var b strings.Builder
	b.WriteString(title)
	b.WriteString("\n\n")
	b.WriteString("- Generated: ")
	b.WriteString(time.Now().UTC().Format(time.RFC3339))
	b.WriteString("\n")
	b.WriteString("- ")
	b.WriteString(countLabel)
	b.WriteString(": ")
	b.WriteString(strconv.Itoa(len(entries)))
	b.WriteString("/")
	b.WriteString(strconv.Itoa(totalEntries))
	b.WriteString("\n")
	b.WriteString("- ")
	b.WriteString(resultsLabel)
	b.WriteString(": [")
	b.WriteString(relResults)
	b.WriteString("](")
	b.WriteString(relResults)
	b.WriteString(")\n")
	b.WriteString("- Session logs folder: [")
	b.WriteString(relRunDir)
	b.WriteString("](")
	b.WriteString(relRunDir)
	b.WriteString(")\n\n")

	if len(entries) == 0 {
		b.WriteString(noEntriesMessage)
		b.WriteString("\n")
		return b.String()
	}

	for _, entry := range entries {
		b.WriteString("## ")
		b.WriteString(entryPrefix)
		b.WriteString(": ")
		b.WriteString(entry.stimulus)
		b.WriteString(" (trial ")
		b.WriteString(strconv.Itoa(entry.trialIndex + 1))
		b.WriteString("/")
		b.WriteString(strconv.Itoa(entry.totalTrials))
		b.WriteString(", ")
		b.WriteString(entry.model)
		// Experiment runs carry a variant (e.g. skills=with-skills vs
		// skills=without-skills). Surface it so a no-skill run is obvious even
		// when the skills-loaded list below is empty. Plain eval runs use the
		// default "main" variant, which carries no signal, so skip it.
		if entry.experimentVariant != "" && entry.experimentVariant != "main" {
			b.WriteString(", ")
			b.WriteString(entry.experimentVariant)
		}
		b.WriteString(")")
		b.WriteString("\n\n")

		sessionTarget := nonEmpty(entry.sessionPath, nonEmpty(sessionLogsPath, runDir))
		relSessionTarget := toWorkspaceRelative(sessionTarget)
		relEntryResults := toWorkspaceRelative(nonEmpty(entry.sourcePath, headerPath))

		b.WriteString(detailsHeading)
		b.WriteString("\n\n")
		b.WriteString("- Checks:\n")
		if len(entry.checks) == 0 {
			b.WriteString("  - (none listed)\n")
		} else {
			for _, c := range entry.checks {
				mark := " "
				if c.Passed {
					mark = "x"
				}
				b.WriteString("  - [")
				b.WriteString(mark)
				b.WriteString("] ")
				b.WriteString(c.Name)
				b.WriteString("\n")
			}
		}
		b.WriteString("- Session: [")
		b.WriteString(relSessionTarget)
		b.WriteString("](")
		b.WriteString(relSessionTarget)
		b.WriteString(")\n")
		b.WriteString("- JSON record: [")
		b.WriteString(relEntryResults)
		b.WriteString("#L")
		b.WriteString(strconv.Itoa(entry.lineNumber))
		b.WriteString("](")
		b.WriteString(relEntryResults)
		b.WriteString("#L")
		b.WriteString(strconv.Itoa(entry.lineNumber))
		b.WriteString(")\n\n")

		b.WriteString("### Skills Loaded\n\n")
		if len(entry.skillsLoaded) == 0 {
			b.WriteString("- None reported\n\n")
		} else {
			for _, skill := range entry.skillsLoaded {
				b.WriteString("- ")
				b.WriteString(skill)
				b.WriteString("\n")
			}
			b.WriteString("\n")
		}

		b.WriteString("### Tool Calls\n\n")
		if len(entry.toolCalls) == 0 {
			b.WriteString("- None\n\n")
		} else {
			for _, call := range entry.toolCalls {
				b.WriteString("- ")
				b.WriteString(call)
				b.WriteString("\n")
			}
			b.WriteString("\n")
		}

		b.WriteString("### Response\n\n")
		b.WriteString(indentForMarkdown(entry.output))
		b.WriteString("\n")
	}

	return b.String()
}

func nonEmpty(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func indentForMarkdown(value string) string {
	lines := strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")

	var b strings.Builder
	for _, line := range lines {
		b.WriteString("    ")
		b.WriteString(line)
		b.WriteString("\n")
	}

	return b.String()
}

func extractToolCalls(events []event) []string {
	if len(events) == 0 {
		return nil
	}

	toolCalls := []string{}
	counts := map[string]int{}

	for _, ev := range events {
		if ev.Type != "tool_call" {
			continue
		}

		toolName := "unknown"
		if name, ok := ev.Data["toolName"].(string); ok && name != "" {
			toolName = name
		}

		detail := ""
		if args, ok := ev.Data["arguments"].(map[string]any); ok {
			if command, ok := args["command"].(string); ok && strings.TrimSpace(command) != "" {
				detail = truncateSingleLine(command, 90)
			} else if description, ok := args["description"].(string); ok && strings.TrimSpace(description) != "" {
				detail = truncateSingleLine(description, 90)
			}
		}

		counts[toolName]++
		if detail != "" {
			toolCalls = append(toolCalls, fmt.Sprintf("%s: %s", toolName, detail))
		} else {
			toolCalls = append(toolCalls, toolName)
		}
	}

	if len(toolCalls) == 0 {
		return nil
	}

	summary := []string{}
	toolNames := slices.Sorted(maps.Keys(counts))
	for _, name := range toolNames {
		summary = append(summary, fmt.Sprintf("%s x%d", name, counts[name]))
	}

	result := []string{fmt.Sprintf("Summary: %s", strings.Join(summary, ", "))}
	for i, call := range toolCalls {
		if i >= 8 {
			result = append(result, fmt.Sprintf("... and %d more", len(toolCalls)-i))
			break
		}
		result = append(result, call)
	}

	return result
}

func truncateSingleLine(value string, maxLen int) string {
	oneLine := strings.Join(strings.Fields(value), " ")
	if len(oneLine) <= maxLen {
		return oneLine
	}

	if maxLen <= 3 {
		return oneLine[:maxLen]
	}

	return oneLine[:maxLen-3] + "..."
}

func normalizeSkills(skills []string) []string {
	if len(skills) == 0 {
		return nil
	}

	seen := map[string]bool{}
	for _, skill := range skills {
		s := strings.TrimSpace(skill)
		if s == "" {
			continue
		}
		seen[s] = true
	}

	if len(seen) == 0 {
		return nil
	}

	return slices.Sorted(maps.Keys(seen))
}

func latestRunFromVallyResults(baseDir string) (string, error) {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return "", err
	}

	latestName := ""
	latestPath := ""
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		runDir := filepath.Join(baseDir, entry.Name())
		resultsPath := filepath.Join(runDir, "results.jsonl")
		if _, err := os.Stat(resultsPath); err != nil {
			continue
		}

		if latestName == "" || entry.Name() > latestName {
			latestName = entry.Name()
			latestPath = runDir
		}
	}

	if latestPath == "" {
		return "", fmt.Errorf("no runs with results.jsonl found in %s", baseDir)
	}

	return latestPath, nil
}

// collectResultsFiles returns the results.jsonl files for a run directory.
// A plain `vally eval` run has a single results.jsonl at the root. An
// experiment run (`vally experiment run`) shards output into one subdirectory
// per variant (e.g. model=<name>), each with its own results.jsonl.
func collectResultsFiles(runDir string) ([]string, error) {
	direct := filepath.Join(runDir, "results.jsonl")
	if _, err := os.Stat(direct); err == nil {
		return []string{direct}, nil
	}

	entries, err := os.ReadDir(runDir)
	if err != nil {
		return nil, err
	}

	files := []string{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		shard := filepath.Join(runDir, entry.Name(), "results.jsonl")
		if _, err := os.Stat(shard); err == nil {
			files = append(files, shard)
		}
	}

	slices.Sort(files)
	if len(files) == 0 {
		return nil, fmt.Errorf("no results.jsonl found in %s", runDir)
	}

	return files, nil
}

// latestExperimentRun returns the most recent experiment run directory (by name)
// that contains at least one variant shard with a results.jsonl.
func latestExperimentRun(baseDir string) (string, error) {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return "", err
	}

	latestName := ""
	latestPath := ""
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		runDir := filepath.Join(baseDir, entry.Name())
		if files, err := collectResultsFiles(runDir); err != nil || len(files) == 0 {
			continue
		}

		if latestName == "" || entry.Name() > latestName {
			latestName = entry.Name()
			latestPath = runDir
		}
	}

	if latestPath == "" {
		return "", fmt.Errorf("no experiment runs with results.jsonl found in %s", baseDir)
	}

	return latestPath, nil
}

func main() {
	// report loads every results.jsonl under runDir (a single eval run has one
	// at the root; an experiment run shards one per variant), then writes a
	// failed and a passed markdown report using the given filename prefix.
	report := func(runDir, prefix string) {
		resultsFiles, err := collectResultsFiles(runDir)
		if err != nil {
			fmt.Fprintln(os.Stderr, "failed collecting results:", err)
			os.Exit(1)
		}

		records, err := loadResults(resultsFiles)
		if err != nil {
			fmt.Fprintln(os.Stderr, "failed reading results.jsonl:", err)
			os.Exit(1)
		}

		successEntries, failedEntries := filteredEntries(records)

		failedMarkdownPath := fmt.Sprintf("%s-failed.md", prefix)
		passedMarkdownPath := fmt.Sprintf("%s-passed.md", prefix)

		failedReport := buildReport(failedEntries, len(records), runDir, runDir, false)
		if err := os.WriteFile(failedMarkdownPath, []byte(failedReport), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, "failed writing failed report:", err)
			os.Exit(1)
		}

		successReport := buildReport(successEntries, len(records), runDir, runDir, true)
		if err := os.WriteFile(passedMarkdownPath, []byte(successReport), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, "failed writing success report:", err)
			os.Exit(1)
		}

		fmt.Printf("%s: %d failed, %d passed -> %s | %s\n",
			prefix, len(failedEntries), len(successEntries), failedMarkdownPath, passedMarkdownPath)
	}

	// Report on the latest plain eval run and the latest experiment run. Either
	// may be absent, so skip whichever has no results rather than failing.
	if runDir, err := latestRunFromVallyResults("vally-results"); err == nil {
		report(runDir, "eval-results")
	} else {
		fmt.Fprintln(os.Stderr, "skipping vally-results:", err)
	}

	if runDir, err := latestExperimentRun("vally-experiment-results"); err == nil {
		report(runDir, "eval-experiments")
	} else {
		fmt.Fprintln(os.Stderr, "skipping vally-experiment-results:", err)
	}
}
