// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// changelog-audit retroactively applies the updated changelog generation rules
// to the last N releases and produces a side-by-side comparison report showing
// the live changelog vs issues that the new rules would have caught.
//
// Usage:
//
//	go run . [-n 20] [-changelog ../../cli/azd/CHANGELOG.md] [-tag-prefix azure-dev-cli_]
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// --- Data model ---

type Release struct {
	Version    string
	Date       string
	HeaderLine int
	Categories []Category
	RawLines   []string // original text
}

type Category struct {
	Name    string
	Entries []Entry
}

type Entry struct {
	PRNumber    int    // 0 if missing
	IsIssueRef bool   // true if link points to /issues/ not /pull/
	RawText     string // full bullet text
	HasLink     bool   // has [[#N]] reference
	LineNumber  int
}

type Commit struct {
	SHA       string
	Subject   string
	PRNumbers []int  // all PR numbers found in subject
	Canonical int    // last PR number (per new dual-PR rule)
	IsRevert  bool
	RevertsPR int // PR number being reverted
}

type Finding struct {
	Rule        string // e.g., "F1", "F2", ...
	Severity    string // "error", "warning", "info"
	Description string
	EntryText   string // optional context
	LineNumber  int    // changelog line number affected (0 if N/A)
	OldPR       int    // for F1: wrong PR number used
	NewPR       int    // for F1: correct (canonical) PR number
}

type ReleaseAudit struct {
	Release  Release
	Tag      string
	PrevTag  string
	Commits  []Commit
	Findings []Finding
}

// --- Regex patterns ---

var (
	releaseHeaderRe = regexp.MustCompile(`^## (\d+\.\d+\.\d+(?:-[a-zA-Z0-9.]+)?) \((\d{4}-\d{2}-\d{2})\)`)
	unreleasedRe    = regexp.MustCompile(`^## .+\(Unreleased\)`)
	categoryRe      = regexp.MustCompile(`^### (.+)$`)
	bulletStartRe   = regexp.MustCompile(`^- `)
	prLinkRe        = regexp.MustCompile(`\[\[#(\d+)\]\]\(https://github\.com/Azure/azure-dev/(pull|issues)/(\d+)\)`)
	commitPRRe      = regexp.MustCompile(`\(#(\d+)\)`)
	mergeCommitRe   = regexp.MustCompile(`Merge pull request #(\d+)`)
	revertRe        = regexp.MustCompile(`^Revert\b`)
	revertPRRe      = regexp.MustCompile(`\(#(\d+)\)`)
)

func main() {
	var (
		numReleases   int
		changelogPath string
		tagPrefix     string
		repoRoot      string
		outputPath    string
	)

	flag.IntVar(&numReleases, "n", 20, "number of releases to audit")
	flag.StringVar(&changelogPath, "changelog", "", "path to CHANGELOG.md (auto-detected if empty)")
	flag.StringVar(&tagPrefix, "tag-prefix", "azure-dev-cli_", "git tag prefix for releases")
	flag.StringVar(&repoRoot, "repo-root", "", "git repository root (auto-detected if empty)")
	flag.StringVar(&outputPath, "output", "findings.md", "output findings report path")
	flag.Parse()

	if repoRoot == "" {
		out, err := gitCmd("rev-parse", "--show-toplevel")
		if err != nil {
			log.Fatalf("cannot determine repo root: %v", err)
		}
		repoRoot = strings.TrimSpace(out)
	}
	gitWorkDir = repoRoot

	if changelogPath == "" {
		changelogPath = filepath.Join(repoRoot, "cli", "azd", "CHANGELOG.md")
	}

	// Step 1: Parse changelog into releases
	releases, err := parseChangelog(changelogPath)
	if err != nil {
		log.Fatalf("parse changelog: %v", err)
	}

	// Keep only dated releases (skip Unreleased), take last N
	var dated []Release
	for _, r := range releases {
		if r.Date != "" {
			dated = append(dated, r)
		}
	}
	if len(dated) > numReleases {
		dated = dated[:numReleases]
	}

	fmt.Fprintf(os.Stderr, "Auditing %d releases...\n", len(dated))

	// Step 2: Map releases to tags and compute commit ranges
	audits := make([]ReleaseAudit, len(dated))
	// Build global PR→release map for cross-release dedup
	prReleaseMap := map[int]string{} // PR# → first release version that references it

	for i, r := range dated {
		tag := tagPrefix + r.Version
		prevTag := ""
		if i+1 < len(dated) {
			prevTag = tagPrefix + dated[i+1].Version
		}

		audits[i] = ReleaseAudit{
			Release: r,
			Tag:     tag,
			PrevTag: prevTag,
		}

		// Track PR numbers per release for cross-release dedup (process in reverse chronological order)
		for _, cat := range r.Categories {
			for _, e := range cat.Entries {
				if e.PRNumber > 0 {
					if _, exists := prReleaseMap[e.PRNumber]; !exists {
						prReleaseMap[e.PRNumber] = r.Version
					}
				}
			}
		}
	}

	// Rebuild prReleaseMap in chronological order (oldest first gets precedence)
	prReleaseMap = map[int]string{}
	for i := len(dated) - 1; i >= 0; i-- {
		for _, cat := range dated[i].Categories {
			for _, e := range cat.Entries {
				if e.PRNumber > 0 {
					if _, exists := prReleaseMap[e.PRNumber]; !exists {
						prReleaseMap[e.PRNumber] = dated[i].Version
					}
				}
			}
		}
	}

	// Step 3: Get commits and run findings for each release
	for i := range audits {
		a := &audits[i]

		if a.PrevTag != "" {
			commits, err := getCommitsInRange(a.PrevTag, a.Tag)
			if err != nil {
				fmt.Fprintf(os.Stderr, "WARNING: cannot get commits for %s..%s: %v\n", a.PrevTag, a.Tag, err)
			}
			a.Commits = commits
		}

		a.Findings = runFindings(a, prReleaseMap)
	}

	// Step 4: Generate outputs
	// - findings.md: clean findings report (no embedded diffs)
	// - published/<version>.md: verbatim changelog section per release
	// - corrected/<version>.md: deterministic corrections only
	outDir := filepath.Dir(outputPath)
	if outDir == "" || outDir == "." {
		outDir = "."
	}

	pubDir := filepath.Join(outDir, "published")
	corDir := filepath.Join(outDir, "corrected")
	if err := os.MkdirAll(pubDir, 0755); err != nil {
		log.Fatalf("create published dir: %v", err)
	}
	if err := os.MkdirAll(corDir, 0755); err != nil {
		log.Fatalf("create corrected dir: %v", err)
	}

	// Write per-release files
	changedReleases := []string{}
	for _, a := range audits {
		fname := a.Release.Version + ".md"

		// Published: verbatim from CHANGELOG.md
		pubContent := strings.Join(a.Release.RawLines, "\n") + "\n"
		if err := os.WriteFile(filepath.Join(pubDir, fname), []byte(pubContent), 0644); err != nil {
			log.Fatalf("write published/%s: %v", fname, err)
		}

		// Corrected: apply only deterministic fixes
		corrected, _ := correctRelease(&a)
		corContent := strings.Join(corrected, "\n") + "\n"
		if err := os.WriteFile(filepath.Join(corDir, fname), []byte(corContent), 0644); err != nil {
			log.Fatalf("write corrected/%s: %v", fname, err)
		}

		if pubContent != corContent {
			changedReleases = append(changedReleases, a.Release.Version)
		}
	}

	// Write findings report
	report := generateReport(audits, changedReleases)
	reportPath := outputPath
	if reportPath == "" {
		reportPath = filepath.Join(outDir, "findings.md")
	}
	if err := os.WriteFile(reportPath, []byte(report), 0644); err != nil {
		log.Fatalf("write findings: %v", err)
	}

	fmt.Fprintf(os.Stderr, "Output written:\n")
	fmt.Fprintf(os.Stderr, "  %s (findings report)\n", reportPath)
	fmt.Fprintf(os.Stderr, "  %s/ (%d release files)\n", pubDir, len(audits))
	fmt.Fprintf(os.Stderr, "  %s/ (%d release files, %d with corrections)\n", corDir, len(audits), len(changedReleases))
}

// --- Changelog parser (stateful, line-oriented) ---

func parseChangelog(path string) ([]Release, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var (
		releases   []Release
		current    *Release
		currentCat *Category
		inBullet   bool
		scanner    = bufio.NewScanner(f)
		lineNum    int
	)

	// Increase buffer size for long lines
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		// Check for release header
		if m := releaseHeaderRe.FindStringSubmatch(line); m != nil {
			finishRelease(&releases, current, currentCat)
			r := Release{Version: m[1], Date: m[2], HeaderLine: lineNum}
			current = &r
			currentCat = nil
			inBullet = false
			current.RawLines = append(current.RawLines, line)
			continue
		}

		// Check for unreleased header
		if unreleasedRe.MatchString(line) {
			finishRelease(&releases, current, currentCat)
			r := Release{Version: extractUnreleasedVersion(line), HeaderLine: lineNum}
			current = &r
			currentCat = nil
			inBullet = false
			current.RawLines = append(current.RawLines, line)
			continue
		}

		if current == nil {
			continue
		}

		current.RawLines = append(current.RawLines, line)

		// Check for category header
		if m := categoryRe.FindStringSubmatch(line); m != nil {
			if currentCat != nil {
				current.Categories = append(current.Categories, *currentCat)
			}
			currentCat = &Category{Name: m[1]}
			inBullet = false
			continue
		}

		// Check for bullet start
		if bulletStartRe.MatchString(line) {
			entry := parseEntry(line, lineNum)
			if currentCat == nil {
				currentCat = &Category{Name: "(uncategorized)"}
			}
			currentCat.Entries = append(currentCat.Entries, entry)
			inBullet = true
			continue
		}

		// Continuation line (indented text after a bullet)
		if inBullet && len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			if currentCat != nil && len(currentCat.Entries) > 0 {
				last := &currentCat.Entries[len(currentCat.Entries)-1]
				last.RawText += "\n" + line
			}
			continue
		}

		// Blank line or other content
		if strings.TrimSpace(line) == "" {
			inBullet = false
		}
	}

	finishRelease(&releases, current, currentCat)
	return releases, scanner.Err()
}

func finishRelease(releases *[]Release, current *Release, currentCat *Category) {
	if current != nil {
		if currentCat != nil {
			current.Categories = append(current.Categories, *currentCat)
		}
		*releases = append(*releases, *current)
	}
}

func extractUnreleasedVersion(line string) string {
	re := regexp.MustCompile(`## (\S+)`)
	if m := re.FindStringSubmatch(line); m != nil {
		return m[1]
	}
	return "unreleased"
}

func parseEntry(line string, lineNum int) Entry {
	e := Entry{
		RawText:    line,
		LineNumber: lineNum,
	}

	matches := prLinkRe.FindAllStringSubmatch(line, -1)
	if len(matches) > 0 {
		e.HasLink = true
		e.PRNumber = atoi(matches[0][1])
		e.IsIssueRef = matches[0][2] == "issues"

		if len(matches) > 1 {
			log.Printf("warning: changelog entry on line %d contains %d PR/issue links; only the first (%d) is tracked: %s",
				lineNum, len(matches), e.PRNumber, truncate(line, 120))
		}
	}

	return e
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		}
	}
	return n
}

// --- Git operations ---

var gitWorkDir string // set in main() to repo root

func gitCmd(args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"--no-pager"}, args...)...)
	if gitWorkDir != "" {
		cmd.Dir = gitWorkDir
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func getCommitsInRange(fromTag, toTag string) ([]Commit, error) {
	rangeSpec := fromTag + ".." + toTag
	out, err := gitCmd("log", "--oneline", "--pretty=format:%H %s", rangeSpec, "--", "cli/azd/", ":!cli/azd/extensions/")
	if err != nil {
		return nil, fmt.Errorf("git log %s: %w\n%s", rangeSpec, err, out)
	}

	var commits []Commit
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) < 2 {
			continue
		}
		sha := parts[0]
		subject := parts[1]

		c := Commit{SHA: sha, Subject: subject}

		// Extract all PR numbers
		if matches := commitPRRe.FindAllStringSubmatch(subject, -1); matches != nil {
			for _, m := range matches {
				c.PRNumbers = append(c.PRNumbers, atoi(m[1]))
			}
			// Canonical = last PR number (Finding 1: dual PR rule)
			c.Canonical = c.PRNumbers[len(c.PRNumbers)-1]
		} else if m := mergeCommitRe.FindStringSubmatch(subject); m != nil {
			c.PRNumbers = []int{atoi(m[1])}
			c.Canonical = c.PRNumbers[0]
		}

		// Revert detection
		if revertRe.MatchString(subject) {
			c.IsRevert = true
			if matches := revertPRRe.FindAllStringSubmatch(subject, -1); matches != nil {
				c.RevertsPR = atoi(matches[0][1])
			}
		}

		commits = append(commits, c)
	}

	return commits, nil
}

// --- Findings engine ---

func runFindings(a *ReleaseAudit, prReleaseMap map[int]string) []Finding {
	var findings []Finding

	// Collect all changelog PR numbers and entry data
	allEntries := allEntries(a.Release)
	changelogPRs := map[int]bool{}
	for _, e := range allEntries {
		if e.PRNumber > 0 {
			changelogPRs[e.PRNumber] = true
		}
	}

	// Collect commit canonical PRs and reverted PRs
	commitPRs := map[int]bool{}
	revertedPRs := map[int]bool{}
	for _, c := range a.Commits {
		if c.IsRevert {
			revertedPRs[c.RevertsPR] = true
		}
		if c.Canonical > 0 {
			commitPRs[c.Canonical] = true
		}
	}

	// --- Finding 1: Dual PR numbers ---
	for _, c := range a.Commits {
		if len(c.PRNumbers) >= 2 {
			first := c.PRNumbers[0]
			last := c.PRNumbers[len(c.PRNumbers)-1]
			if changelogPRs[first] && !changelogPRs[last] {
				// Find the entry line that uses the wrong PR
				ln := 0
				for _, e := range allEntries {
					if e.PRNumber == first {
						ln = e.LineNumber
						break
					}
				}
				findings = append(findings, Finding{
					Rule:        "F1",
					Severity:    "warning",
					Description: fmt.Sprintf("Dual PR numbers detected: commit references #%d and #%d. Changelog uses #%d but should use #%d (last = canonical).", first, last, first, last),
					EntryText:   c.Subject,
					LineNumber:  ln,
					OldPR:       first,
					NewPR:       last,
				})
			} else if changelogPRs[first] && changelogPRs[last] {
				findings = append(findings, Finding{
					Rule:        "F1",
					Severity:    "info",
					Description: fmt.Sprintf("Dual PR numbers: #%d and #%d. Changelog correctly uses #%d.", first, last, last),
					EntryText:   c.Subject,
				})
			}
		}
	}

	// --- Finding 2: Missing PR links ---
	for _, e := range allEntries {
		if !e.HasLink {
			findings = append(findings, Finding{
				Rule:        "F2",
				Severity:    "error",
				Description: "Entry is missing a [[#PR]] link.",
				EntryText:   truncate(e.RawText, 120),
				LineNumber:  e.LineNumber,
			})
		}
	}

	// --- Finding 2b: Issue link instead of PR link ---
	for _, e := range allEntries {
		if e.IsIssueRef {
			findings = append(findings, Finding{
				Rule:        "F2b",
				Severity:    "warning",
				Description: fmt.Sprintf("Entry uses issue link (#%d) instead of PR link. Use /pull/ not /issues/.", e.PRNumber),
				EntryText:   truncate(e.RawText, 120),
				LineNumber:  e.LineNumber,
			})
		}
	}

	// --- Finding 3: Cross-release duplicate ---
	for _, e := range allEntries {
		if e.PRNumber > 0 {
			if firstVer, exists := prReleaseMap[e.PRNumber]; exists && firstVer != a.Release.Version {
				findings = append(findings, Finding{
					Rule:        "F3",
					Severity:    "warning",
					Description: fmt.Sprintf("PR #%d also appears in release %s (cross-release duplicate).", e.PRNumber, firstVer),
					EntryText:   truncate(e.RawText, 120),
					LineNumber:  e.LineNumber,
				})
			}
		}
	}

	// --- Finding 3b: Intra-release duplicate ---
	seen := map[int]int{} // PR# → count
	for _, e := range allEntries {
		if e.PRNumber > 0 {
			seen[e.PRNumber]++
		}
	}
	for pr, count := range seen {
		if count > 1 {
			findings = append(findings, Finding{
				Rule:        "F3b",
				Severity:    "warning",
				Description: fmt.Sprintf("PR #%d appears %d times within this release.", pr, count),
			})
		}
	}

	// --- Finding 4: Alpha/beta feature gating ---
	// Check for PRs with alpha-related keywords. Full label check would require GitHub API.
	for _, c := range a.Commits {
		subjectLower := strings.ToLower(c.Subject)
		if (strings.Contains(subjectLower, "alpha") || strings.Contains(subjectLower, "feature flag")) &&
			!c.IsRevert && c.Canonical > 0 {
			if changelogPRs[c.Canonical] {
				findings = append(findings, Finding{
					Rule:        "F4",
					Severity:    "info",
					Description: fmt.Sprintf("PR #%d mentions alpha/feature-flag in subject. Verify gating decision.", c.Canonical),
					EntryText:   c.Subject,
				})
			}
		}
	}

	// --- Finding 5: Borderline excluded commits ---
	borderlineKeywords := []string{
		"help text", "error message", "output", "flag pars", "flag prop",
		"stderr", "ux", "prompt", "display", "format",
	}
	for _, c := range a.Commits {
		if c.IsRevert || c.Canonical == 0 {
			continue
		}
		if changelogPRs[c.Canonical] || revertedPRs[c.Canonical] {
			continue
		}
		subjectLower := strings.ToLower(c.Subject)
		// Skip known exclusion categories
		if isDefinitelyExcluded(subjectLower) {
			continue
		}
		for _, kw := range borderlineKeywords {
			if strings.Contains(subjectLower, kw) {
				findings = append(findings, Finding{
					Rule:        "F5",
					Severity:    "warning",
					Description: fmt.Sprintf("Excluded commit #%d matches borderline keyword %q. New rules recommend including it.", c.Canonical, kw),
					EntryText:   c.Subject,
				})
				break
			}
		}
	}

	// --- Finding 6: Phantom entries (PR not in commit range) ---
	if len(a.Commits) > 0 {
		for _, e := range allEntries {
			if e.PRNumber > 0 && !commitPRs[e.PRNumber] {
				// Check if it matches any commit's non-canonical PR number
				aliased := false
				for _, c := range a.Commits {
					for _, pr := range c.PRNumbers {
						if pr == e.PRNumber {
							aliased = true
							break
						}
					}
					if aliased {
						break
					}
				}
				if !aliased {
					findings = append(findings, Finding{
						Rule:        "F6",
						Severity:    "warning",
						Description: fmt.Sprintf("PR #%d in changelog but not found in commit range %s..%s (phantom entry).", e.PRNumber, a.PrevTag, a.Tag),
						EntryText:   truncate(e.RawText, 120),
						LineNumber:  e.LineNumber,
					})
				}
			}
		}
	}

	// --- Finding 6b: Link integrity (text PR# vs URL PR#) ---
	linkIntegrityRe := regexp.MustCompile(`\[\[#(\d+)\]\]\(https://github\.com/Azure/azure-dev/(?:pull|issues)/(\d+)\)`)
	for _, e := range allEntries {
		if m := linkIntegrityRe.FindStringSubmatch(e.RawText); m != nil {
			textNum := m[1]
			urlNum := m[2]
			if textNum != urlNum {
				findings = append(findings, Finding{
					Rule:        "F6b",
					Severity:    "error",
					Description: fmt.Sprintf("Link text [[#%s]] does not match URL number %s.", textNum, urlNum),
					EntryText:   truncate(e.RawText, 120),
				})
			}
		}
	}

	return findings
}

func allEntries(r Release) []Entry {
	var entries []Entry
	for _, cat := range r.Categories {
		entries = append(entries, cat.Entries...)
	}
	return entries
}

func isDefinitelyExcluded(subject string) bool {
	excludePatterns := []string{
		"changelog", "version bump", "increment cli version",
		"extension registry", "chore:", "refactor:", "test:",
		"docs:", "ci:", "build:", "revert",
	}
	for _, p := range excludePatterns {
		if strings.Contains(subject, p) {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	// Take first line only, truncate if needed
	if idx := strings.Index(s, "\n"); idx >= 0 {
		s = s[:idx]
	}
	if len(s) > n {
		return s[:n-3] + "..."
	}
	return s
}

// --- Correction engine ---

// correctRelease applies mechanical fixes to a release's raw lines and returns
// the corrected version along with per-line annotations explaining each change.
func correctRelease(a *ReleaseAudit) (corrected []string, annotations map[int]string) {
	annotations = map[int]string{}
	original := a.Release.RawLines

	// Build lookup maps: line number → finding(s)
	// Line numbers in findings are absolute; convert to relative (0-based index into RawLines)
	baseLineNum := a.Release.HeaderLine // line 1 of RawLines = this absolute line number

	removedLines := map[int]bool{}   // relative line indices to remove
	replacedLines := map[int]string{} // relative line index → replacement text

	for _, f := range a.Findings {
		if f.LineNumber == 0 {
			continue
		}
		relIdx := f.LineNumber - baseLineNum

		if relIdx < 0 || relIdx >= len(original) {
			continue
		}

		switch f.Rule {
		case "F1":
			// Replace old PR number with new canonical one
			if f.OldPR > 0 && f.NewPR > 0 {
				line := original[relIdx]
				oldRef := fmt.Sprintf("[[#%d]](https://github.com/Azure/azure-dev/pull/%d)", f.OldPR, f.OldPR)
				newRef := fmt.Sprintf("[[#%d]](https://github.com/Azure/azure-dev/pull/%d)", f.NewPR, f.NewPR)
				if strings.Contains(line, oldRef) {
					replacedLines[relIdx] = strings.Replace(line, oldRef, newRef, 1)
					annotations[relIdx] = fmt.Sprintf("F1: use canonical PR #%d", f.NewPR)
				}
			}

		case "F2":
			// Missing PR link — cannot determine correct PR number mechanically.
			// Report in findings only; do NOT generate placeholder corrections.

		case "F2b":
			// Change /issues/ to /pull/
			line := original[relIdx]
			if strings.Contains(line, "/issues/") {
				replacedLines[relIdx] = strings.Replace(line, "/issues/", "/pull/", 1)
				annotations[relIdx] = "F2b: use /pull/ not /issues/"
			}

		case "F3":
			// Mark cross-release duplicate for removal
			removedLines[relIdx] = true
			annotations[relIdx] = "F3: remove cross-release duplicate"

		case "F6":
			// Mark phantom entry for removal (only if it's an actionable warning)
			if f.Severity == "warning" {
				removedLines[relIdx] = true
				annotations[relIdx] = "F6: remove phantom entry"
			}

		case "F6b":
			// Fix link text/URL mismatch
			line := original[relIdx]
			linkRe := regexp.MustCompile(`\[\[#(\d+)\]\]\(https://github\.com/Azure/azure-dev/(pull|issues)/(\d+)\)`)
			if m := linkRe.FindStringSubmatch(line); m != nil {
				urlNum := m[3]
				oldLink := m[0]
				newLink := fmt.Sprintf("[[#%s]](https://github.com/Azure/azure-dev/%s/%s)", urlNum, m[2], urlNum)
				replacedLines[relIdx] = strings.Replace(line, oldLink, newLink, 1)
				annotations[relIdx] = "F6b: fix link text/URL mismatch"
			}
		}
	}

	// Build corrected lines
	for i, line := range original {
		if removedLines[i] {
			continue
		}
		if replacement, ok := replacedLines[i]; ok {
			corrected = append(corrected, replacement)
		} else {
			corrected = append(corrected, line)
		}
	}

	// F5 (borderline excluded commits) — cannot determine correct placement or
	// description mechanically. Report in findings only; do NOT generate entries.

	return corrected, annotations
}


// --- Report generation ---

func generateReport(audits []ReleaseAudit, changedReleases []string) string {
	var sb strings.Builder

	// Header
	headSHA, _ := gitCmd("rev-parse", "--short", "HEAD")
	headSHA = strings.TrimSpace(headSHA)

	sb.WriteString("# Changelog Audit Report\n\n")
	sb.WriteString(fmt.Sprintf("**Generated**: %s\n", time.Now().UTC().Format("2006-01-02 15:04 UTC")))
	sb.WriteString(fmt.Sprintf("**Repo SHA**: %s\n", headSHA))
	sb.WriteString(fmt.Sprintf("**Releases audited**: %d\n", len(audits)))
	sb.WriteString("**Rules applied**: F1 (dual PR numbers), F2 (PR link validation), F3 (cross-release dedup), F4 (alpha/beta gating), F5 (borderline inclusion), F6 (phantom entries)\n\n")

	// How to review
	sb.WriteString("## How to Review\n\n")
	sb.WriteString("Each audited release has two files:\n\n")
	sb.WriteString("- `published/<version>.md` — the changelog section exactly as published\n")
	sb.WriteString("- `corrected/<version>.md` — the same section with deterministic corrections applied\n\n")
	sb.WriteString("**Compare them** to see the impact of the new rules:\n\n")
	sb.WriteString("```bash\n")
	sb.WriteString("# Diff a single release\n")
	sb.WriteString("diff -u published/1.23.12.md corrected/1.23.12.md\n\n")
	sb.WriteString("# Diff all releases at once\n")
	sb.WriteString("diff -ru published/ corrected/\n")
	sb.WriteString("```\n\n")

	changedSet := map[string]bool{}
	for _, v := range changedReleases {
		changedSet[v] = true
	}
	if len(changedReleases) > 0 {
		sb.WriteString(fmt.Sprintf("**Releases with corrections** (%d of %d): ", len(changedReleases), len(audits)))
		sb.WriteString(strings.Join(changedReleases, ", "))
		sb.WriteString("\n\n")
	} else {
		sb.WriteString("**No releases required corrections.** All published entries match the new rules.\n\n")
	}

	// Summary table
	sb.WriteString("## Summary\n\n")
	sb.WriteString("| Release | Entries | Commits | Errors | Warnings | Info |\n")
	sb.WriteString("|---------|---------|---------|--------|----------|------|\n")

	totalErrors, totalWarnings, totalInfo := 0, 0, 0
	for _, a := range audits {
		entries := allEntries(a.Release)
		errs, warns, infos := countSeverities(a.Findings)
		totalErrors += errs
		totalWarnings += warns
		totalInfo += infos
		sb.WriteString(fmt.Sprintf("| %s | %d | %d | %d | %d | %d |\n",
			a.Release.Version, len(entries), len(a.Commits), errs, warns, infos))
	}
	sb.WriteString(fmt.Sprintf("| **Total** | | | **%d** | **%d** | **%d** |\n\n", totalErrors, totalWarnings, totalInfo))

	// Findings by rule
	sb.WriteString("## Findings by Rule\n\n")
	ruleCount := map[string]int{}
	for _, a := range audits {
		for _, f := range a.Findings {
			ruleCount[f.Rule]++
		}
	}
	rules := []string{"F1", "F2", "F2b", "F3", "F3b", "F4", "F5", "F6", "F6b"}
	ruleNames := map[string]string{
		"F1":  "Dual PR number extraction",
		"F2":  "Missing PR link on entry",
		"F2b": "Issue link instead of PR link",
		"F3":  "Cross-release duplicate",
		"F3b": "Intra-release duplicate",
		"F4":  "Alpha/beta feature gating",
		"F5":  "Borderline excluded commit",
		"F6":  "Phantom entry (PR not in range)",
		"F6b": "Link text/URL mismatch",
	}
	sb.WriteString("| Rule | Description | Count |\n")
	sb.WriteString("|------|-------------|-------|\n")
	for _, rule := range rules {
		if c, ok := ruleCount[rule]; ok && c > 0 {
			sb.WriteString(fmt.Sprintf("| %s | %s | %d |\n", rule, ruleNames[rule], c))
		}
	}
	sb.WriteString("\n")

	// Per-release detail
	sb.WriteString("## Per-Release Detail\n\n")
	for _, a := range audits {
		entries := allEntries(a.Release)
		errs, warns, infos := countSeverities(a.Findings)

		sb.WriteString(fmt.Sprintf("### %s (%s)\n\n", a.Release.Version, a.Release.Date))

		commitRangeDisplay := "(no previous tag in scope)"
		if a.PrevTag != "" {
			commitRangeDisplay = fmt.Sprintf("`%s..%s`", a.PrevTag, a.Tag)
		}
		sb.WriteString(fmt.Sprintf("**Commit range**: %s (%d commits, %d changelog entries)\n",
			commitRangeDisplay, len(a.Commits), len(entries)))

		if changedSet[a.Release.Version] {
			sb.WriteString(fmt.Sprintf("**Diff**: `diff -u published/%s.md corrected/%s.md`\n", a.Release.Version, a.Release.Version))
		}

		if len(a.Findings) == 0 {
			sb.WriteString("\n> No findings — this release is clean under the new rules.\n\n")
			continue
		}

		sb.WriteString(fmt.Sprintf("**Findings**: %d errors, %d warnings, %d info\n\n", errs, warns, infos))

		// Group findings by rule
		byRule := map[string][]Finding{}
		for _, f := range a.Findings {
			byRule[f.Rule] = append(byRule[f.Rule], f)
		}

		sortedRules := sortedKeys(byRule)
		for _, rule := range sortedRules {
			findings := byRule[rule]
			sb.WriteString(fmt.Sprintf("#### %s: %s\n\n", rule, ruleNames[rule]))
			for _, f := range findings {
				icon := severityIcon(f.Severity)
				sb.WriteString(fmt.Sprintf("- %s %s\n", icon, f.Description))
				if f.EntryText != "" {
					// Use indented code block to avoid backtick nesting issues
					sb.WriteString("  >\n")
					for _, line := range strings.Split(f.EntryText, "\n") {
						sb.WriteString(fmt.Sprintf("  >     %s\n", line))
					}
				}
			}
			sb.WriteString("\n")
		}
	}

	return sb.String()
}

func countSeverities(findings []Finding) (errors, warnings, infos int) {
	for _, f := range findings {
		switch f.Severity {
		case "error":
			errors++
		case "warning":
			warnings++
		case "info":
			infos++
		}
	}
	return
}

func severityIcon(s string) string {
	switch s {
	case "error":
		return "[ERROR]"
	case "warning":
		return "[WARN]"
	case "info":
		return "[INFO]"
	default:
		return "[?]"
	}
}

func sortedKeys(m map[string][]Finding) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}


// --- JSON output for machine consumption ---

type AuditJSON struct {
	Generated string            `json:"generated"`
	SHA       string            `json:"sha"`
	Releases  []ReleaseAuditJSON `json:"releases"`
}

type ReleaseAuditJSON struct {
	Version  string        `json:"version"`
	Date     string        `json:"date"`
	Tag      string        `json:"tag"`
	PrevTag  string        `json:"prevTag"`
	Commits  int           `json:"commits"`
	Entries  int           `json:"entries"`
	Findings []FindingJSON `json:"findings"`
}

type FindingJSON struct {
	Rule        string `json:"rule"`
	Severity    string `json:"severity"`
	Description string `json:"description"`
	EntryText   string `json:"entryText,omitempty"`
}

func toJSON(audits []ReleaseAudit) string {
	headSHA, _ := gitCmd("rev-parse", "--short", "HEAD")
	headSHA = strings.TrimSpace(headSHA)

	result := AuditJSON{
		Generated: time.Now().UTC().Format(time.RFC3339),
		SHA:       headSHA,
	}

	for _, a := range audits {
		entries := allEntries(a.Release)
		r := ReleaseAuditJSON{
			Version: a.Release.Version,
			Date:    a.Release.Date,
			Tag:     a.Tag,
			PrevTag: a.PrevTag,
			Commits: len(a.Commits),
			Entries: len(entries),
		}
		for _, f := range a.Findings {
			r.Findings = append(r.Findings, FindingJSON{
				Rule:        f.Rule,
				Severity:    f.Severity,
				Description: f.Description,
				EntryText:   f.EntryText,
			})
		}
		result.Releases = append(result.Releases, r)
	}

	b, _ := json.MarshalIndent(result, "", "  ")
	return string(b)
}
