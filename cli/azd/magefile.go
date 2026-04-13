// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build mage

package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/magefile/mage/mg"
)

// Dev contains developer tooling commands for building and installing azd from source.
type Dev mg.Namespace

// Install builds azd from source as 'azd-dev' and installs it to ~/.azd/bin.
// The binary is named azd-dev to avoid conflicting with a production azd install.
// Automatically adds ~/.azd/bin to PATH if not already present.
//
// Usage: mage dev:install
func (Dev) Install() error {
	repoRoot, err := findRepoRoot()
	if err != nil {
		return err
	}

	azdDir := filepath.Join(repoRoot, "cli", "azd")
	installDir, err := installDir()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(installDir, 0o755); err != nil {
		return fmt.Errorf("creating install dir: %w", err)
	}

	binaryName := "azd-dev"
	if runtime.GOOS == "windows" {
		binaryName = "azd-dev.exe"
	}
	outputPath := filepath.Join(installDir, binaryName)

	version, commit := devVersion(repoRoot)
	ldflags := fmt.Sprintf(
		"-X 'github.com/azure/azure-dev/cli/azd/internal.Version=%s (commit %s)'",
		version, commit,
	)

	fmt.Printf("Building azd (%s/%s)...\n", runtime.GOOS, runtime.GOARCH)
	fmt.Printf("  version: %s\n", version)
	fmt.Printf("  commit:  %s\n", commit)

	cmd := exec.Command("go", "build", "-ldflags", ldflags, "-o", outputPath, ".")
	cmd.Dir = azdDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go build failed: %w", err)
	}

	fmt.Printf("\nInstalled: %s\n", outputPath)

	if !dirOnPath(installDir) {
		if err := addToPath(installDir); err != nil {
			return err
		}
	} else {
		fmt.Println("✓ Install directory is already on PATH.")
	}

	return nil
}

// Uninstall removes the azd-dev binary from ~/.azd/bin.
// The PATH entry is left intact.
//
// Usage: mage dev:uninstall
func (Dev) Uninstall() error {
	dir, err := installDir()
	if err != nil {
		return err
	}

	binaryName := "azd-dev"
	if runtime.GOOS == "windows" {
		binaryName = "azd-dev.exe"
	}
	path := filepath.Join(dir, binaryName)

	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			fmt.Println("azd-dev is not installed.")
			return nil
		}
		return fmt.Errorf("removing %s: %w", path, err)
	}

	fmt.Printf("Removed %s\n", path)
	return nil
}

// Preflight runs all pre-commit quality checks: formatting, copyright headers, linting,
// spell checking, compilation, unit tests, and playback functional tests.
// Reports a summary of all results at the end.
//
// Usage: mage preflight
func Preflight() error {
	// Disable Go workspace mode so preflight mirrors CI, which has no go.work file.
	// Without this, a local go.work can silently resolve different module versions
	// than go.mod alone, masking build failures that only appear in CI.
	defer setEnvScoped("GOWORK", "off")()

	repoRoot, err := findRepoRoot()
	if err != nil {
		return err
	}
	azdDir := filepath.Join(repoRoot, "cli", "azd")

	// Pin GOTOOLCHAIN to the version declared in go.mod when it isn't already
	// set. When the system Go is older (e.g. 1.25) and go.mod says 1.26,
	// parallel compilations can race the auto-download, producing "compile:
	// version X does not match go tool version Y" errors. Pinning upfront
	// avoids this. We skip the override when GOTOOLCHAIN is already set so
	// that a user's explicit choice (or a newer Go) is respected.
	if _, hasToolchain := os.LookupEnv("GOTOOLCHAIN"); !hasToolchain {
		if ver, err := goModVersion(azdDir); err == nil && ver != "" {
			defer setEnvScoped("GOTOOLCHAIN", "go"+ver)()
		}
	}

	type result struct {
		name   string
		status string // "pass" or "fail"
		detail string
	}
	var results []result
	failed := false

	record := func(name, status, detail string) {
		results = append(results, result{name, status, detail})
		if status == "fail" {
			failed = true
		}
	}

	// Check required tools are installed before running anything.
	if err := requireTool("golangci-lint",
		"go install github.com/golangci/golangci-lint/cmd/golangci-lint@v2.11.4"); err != nil {
		return err
	}
	if err := requireTool("cspell", "npm install -g cspell@8.13.1"); err != nil {
		return err
	}
	shell := "sh"
	if runtime.GOOS == "windows" {
		if p, err := exec.LookPath("bash"); err == nil {
			shell = p
		} else if p, err := exec.LookPath("sh"); err == nil {
			shell = p
		} else {
			return fmt.Errorf("bash/sh not found — install Git for Windows: https://git-scm.com/downloads/win")
		}
	}

	// 1. gofmt — check for unformatted files
	fmt.Println("══ Formatting (gofmt) ══")
	if out, err := runCapture(azdDir, "gofmt", "-s", "-l", "."); err != nil {
		record("gofmt", "fail", err.Error())
	} else if len(strings.TrimSpace(out)) > 0 {
		record("gofmt", "fail", "unformatted files:\n"+out)
		fmt.Print(out)
	} else {
		record("gofmt", "pass", "")
	}

	// 2. go fix — check for code modernization opportunities
	fmt.Println("══ Code modernization (go fix) ══")
	if out, err := runCapture(azdDir, "go", "fix", "-diff", "./..."); err != nil {
		record("go fix", "fail", err.Error())
	} else if len(strings.TrimSpace(out)) > 0 {
		record("go fix", "fail", "code should be modernized — run 'go fix ./...' to apply:\n"+out)
		fmt.Print(out)
	} else {
		record("go fix", "pass", "")
	}

	// 3. Copyright headers
	fmt.Println("══ Copyright headers ══")
	script := filepath.Join(repoRoot, "eng", "scripts", "copyright-check.sh")
	if _, err := os.Stat(script); err != nil {
		record("copyright", "fail", "script not found: "+script)
	} else if err := runShellScript(azdDir, shell, script, "."); err != nil {
		record("copyright", "fail", err.Error())
	} else {
		record("copyright", "pass", "")
	}

	// 4. golangci-lint
	fmt.Println("══ Lint (golangci-lint) ══")
	if err := runStreaming(azdDir, "golangci-lint", "run", "./..."); err != nil {
		record("lint", "fail", err.Error())
	} else {
		record("lint", "pass", "")
	}

	// 5a. Spell check (cspell — Go source)
	fmt.Println("══ Spell check (cspell) ══")
	if err := runStreaming(azdDir, "cspell", "lint", "**/*.go",
		"--relative", "--config", "./.vscode/cspell.yaml", "--no-progress"); err != nil {
		record("cspell", "fail", err.Error())
	} else {
		record("cspell", "pass", "")
	}

	// 5b. Spell check (cspell — misc/docs files, mirrors CI cspell-misc.yml)
	fmt.Println("══ Spell check (cspell-misc) ══")
	if err := runStreaming(repoRoot, "cspell", "lint", "**/*",
		"--relative", "--config", "./.vscode/cspell.misc.yaml", "--no-progress"); err != nil {
		record("cspell-misc", "fail", err.Error())
	} else {
		record("cspell-misc", "pass", "")
	}

	// 6. Compile check
	fmt.Println("══ Build (go build) ══")
	if err := runStreaming(azdDir, "go", "build", "./..."); err != nil {
		record("build", "fail", err.Error())
	} else {
		record("build", "pass", "")
	}

	// 7. Unit tests (with -cover to match CI and catch os.Args leaks)
	fmt.Println("══ Unit tests (go test -short -cover) ══")
	if err := runStreaming(azdDir, "go", "test", "./...", "-short", "-cover", "-count=1"); err != nil {
		record("test", "fail", err.Error())
	} else {
		record("test", "pass", "")
	}

	// 8. Functional tests in playback mode (no Azure credentials needed).
	fmt.Println("══ Playback tests (functional) ══")
	if err := runPlaybackTests(azdDir); err != nil {
		record("playback tests", "fail", err.Error())
	} else {
		record("playback tests", "pass", "")
	}

	// Summary
	fmt.Println("\n══════════════════════════")
	fmt.Println("  Preflight Summary")
	fmt.Println("══════════════════════════")
	for _, r := range results {
		icon := "✓"
		if r.status == "fail" {
			icon = "✗"
		}
		fmt.Printf("  %s %s\n", icon, r.name)
	}
	fmt.Println("══════════════════════════")

	if failed {
		return fmt.Errorf("preflight failed")
	}
	fmt.Println("All checks passed!")
	return nil
}

// PlaybackTests runs functional tests that have recordings in playback mode.
// No Azure credentials are required — tests replay from recorded HTTP
// interactions stored in test/functional/testdata/recordings.
//
// Usage: mage playbackTests
func PlaybackTests() error {
	defer setEnvScoped("GOWORK", "off")()

	repoRoot, err := findRepoRoot()
	if err != nil {
		return err
	}
	azdDir := filepath.Join(repoRoot, "cli", "azd")

	// Pin GOTOOLCHAIN (see Preflight for rationale).
	if _, hasToolchain := os.LookupEnv("GOTOOLCHAIN"); !hasToolchain {
		if ver, err := goModVersion(azdDir); err == nil && ver != "" {
			defer setEnvScoped("GOTOOLCHAIN", "go"+ver)()
		}
	}

	return runPlaybackTests(azdDir)
}

// runPlaybackTests discovers test recordings and runs matching functional
// tests in playback mode (AZURE_RECORD_MODE=playback).
func runPlaybackTests(azdDir string) error {
	recordingsDir := filepath.Join(
		azdDir, "test", "functional", "testdata", "recordings",
	)
	names, err := discoverPlaybackTests(recordingsDir)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		fmt.Println("No recording files found — skipping playback tests.")
		return nil
	}

	escaped := make([]string, len(names))
	for i, name := range names {
		escaped[i] = regexp.QuoteMeta(name)
	}
	pattern := "^(" + strings.Join(escaped, "|") + ")(/|$)"
	fmt.Printf("Running %d tests in playback mode...\n", len(names))

	return runStreamingWithEnv(
		azdDir,
		[]string{"AZURE_RECORD_MODE=playback"},
		"go", "test", "-run", pattern,
		"./test/functional", "-timeout", "30m", "-count=1",
	)
}

// excludedPlaybackTests lists tests whose recordings are known to be stale.
// These are excluded from automatic playback so they don't block preflight.
// Re-record the test to remove it from this list.
var excludedPlaybackTests = map[string]string{}

// discoverPlaybackTests scans the recordings directory for .yaml files and
// subdirectories, returning unique top-level Go test function names.
func discoverPlaybackTests(recordingsDir string) ([]string, error) {
	entries, err := os.ReadDir(recordingsDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading recordings directory: %w", err)
	}

	seen := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			// Only include directories named like Go test functions.
			if strings.HasPrefix(name, "Test") {
				seen[name] = true
			}
			continue
		}
		if !strings.HasSuffix(name, ".yaml") {
			continue
		}
		// Strip .yaml, then take everything before the first "."
		// to get the top-level test function name.
		// Example: Test_CLI_Aspire_Deploy.dotnet.yaml
		//        → Test_CLI_Aspire_Deploy
		cassette := strings.TrimSuffix(name, ".yaml")
		if idx := strings.Index(cassette, "."); idx >= 0 {
			cassette = cassette[:idx]
		}
		seen[cassette] = true
	}

	// Remove tests with known stale recordings.
	for name := range excludedPlaybackTests {
		delete(seen, name)
	}

	if len(seen) == 0 {
		return nil, nil
	}

	return slices.Sorted(maps.Keys(seen)), nil
}

// goModVersion reads the "go X.Y.Z" directive from go.mod in the given dir.
func goModVersion(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "go ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "go ")), nil
		}
	}
	return "", nil
}

// runCapture runs a command and returns its combined stdout/stderr.
func runCapture(dir string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

// runStreaming runs a command with stdout/stderr connected to the terminal.
func runStreaming(dir string, name string, args ...string) error {
	return runStreamingWithEnv(dir, nil, name, args...)
}

// runStreamingWithEnv runs a command with stdout/stderr connected to the
// terminal and additional environment variables set.
func runStreamingWithEnv(
	dir string, env []string, name string, args ...string,
) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// runShellScript runs a shell script using the given shell binary.
// On Windows, converts the script path to the form expected by the detected shell (WSL or Git-for-Windows bash)
// and handles CRLF line endings that WSL bash cannot process.
func runShellScript(dir string, shell string, script string, args ...string) error {
	if runtime.GOOS == "windows" {
		shellScript := toShellPath(shell, script)

		// Build: cd '<dir>' && tr -d '\r' < '<script>' | bash -s -- '<arg1>' ...
		// This strips CRLF line endings that WSL bash chokes on.
		shellDir := toShellPath(shell, dir)
		quotedArgs := make([]string, len(args))
		for i, a := range args {
			quotedArgs[i] = shellQuote(a)
		}
		inner := fmt.Sprintf(`cd %s && tr -d '\r' < %s | bash -s -- %s`,
			shellQuote(shellDir), shellQuote(shellScript), strings.Join(quotedArgs, " "))
		cmd := exec.Command(shell, "-c", inner)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	cmdArgs := append([]string{script}, args...)
	cmd := exec.Command(shell, cmdArgs...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// shellKind caches the result of detecting whether the shell is WSL or Git-for-Windows bash.
var shellKind struct {
	once  sync.Once
	isWSL bool
}

// toShellPath converts a Windows path to a unix-style path for the given shell.
// WSL bash expects /mnt/c/..., Git-for-Windows bash expects /c/...
func toShellPath(shell, winPath string) string {
	p := filepath.ToSlash(winPath)
	if len(p) >= 2 && p[1] == ':' {
		drive := strings.ToLower(string(p[0]))
		rest := p[2:]

		// Cache WSL detection so we only shell out once.
		shellKind.once.Do(func() {
			out, err := exec.Command(shell, "-c", "test -d /mnt/c && echo wsl").Output()
			shellKind.isWSL = err == nil && strings.TrimSpace(string(out)) == "wsl"
		})
		if shellKind.isWSL {
			return "/mnt/" + drive + rest
		}
		return "/" + drive + rest
	}
	return p
}

// requireTool checks that a CLI tool is on PATH, returning a helpful install message if not.
func requireTool(name, installCmd string) error {
	if _, err := exec.LookPath(name); err != nil {
		return fmt.Errorf("%s is required but not installed.\n  Install: %s", name, installCmd)
	}
	return nil
}

// setEnvScoped sets an environment variable and returns a function that restores
// the original value. Use with defer: defer setEnvScoped("KEY", "value")()
// NOTE: os.Setenv is process-global and not goroutine-safe. This is safe
// because mage targets run sequentially (no parallel deps).
func setEnvScoped(key, value string) func() {
	orig, had := os.LookupEnv(key)
	os.Setenv(key, value)
	return func() {
		if had {
			os.Setenv(key, orig)
		} else {
			os.Unsetenv(key)
		}
	}
}

// shellQuote wraps s in single quotes and escapes embedded single quotes for POSIX shells.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func installDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("getting home directory: %w", err)
	}
	return filepath.Join(home, ".azd", "bin"), nil
}

func devVersion(repoRoot string) (string, string) {
	version := "0.0.0-dev.0"

	if data, err := os.ReadFile(filepath.Join(repoRoot, "cli", "version.txt")); err == nil {
		if v := strings.TrimSpace(string(data)); v != "" {
			version = v + "-dev"
		}
	}

	commit := strings.Repeat("0", 40)
	if out, err := exec.Command("git", "-C", repoRoot, "rev-parse", "HEAD").Output(); err == nil {
		if h := strings.TrimSpace(string(out)); len(h) == 40 {
			commit = h
		}
	}

	return version, commit
}

func findRepoRoot() (string, error) {
	if out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output(); err == nil {
		return filepath.FromSlash(strings.TrimSpace(string(out))), nil
	}

	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "cli", "azd", "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find azure-dev repository root (looking for cli/azd/go.mod)")
		}
		dir = parent
	}
}

func dirOnPath(dir string) bool {
	for _, entry := range filepath.SplitList(os.Getenv("PATH")) {
		if strings.EqualFold(filepath.Clean(entry), filepath.Clean(dir)) {
			return true
		}
	}
	return false
}

// addToPath persistently adds dir to the user's PATH.
//   - Windows: updates the User environment variable via PowerShell.
//   - Unix: appends an export line to the user's shell rc file.
//
// The current process PATH is also updated so subsequent steps see the change.
func addToPath(dir string) error {
	switch runtime.GOOS {
	case "windows":
		return addToPathWindows(dir)
	default:
		return addToPathUnix(dir)
	}
}

func addToPathWindows(dir string) error {
	// Read the persisted user PATH to check for duplicates (process PATH may be stale).
	out, err := exec.Command(
		"powershell", "-NoProfile", "-Command",
		"[Environment]::GetEnvironmentVariable('PATH', 'User')",
	).Output()
	if err == nil {
		for _, entry := range filepath.SplitList(strings.TrimSpace(string(out))) {
			if strings.EqualFold(filepath.Clean(entry), filepath.Clean(dir)) {
				fmt.Printf("✓ User PATH already contains %s.\n", dir)
				os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
				return nil
			}
		}
	}

	// Persistently prepend to user-level PATH via PowerShell.
	// Pass dir as a parameter to avoid shell-injection from special characters in the path.
	cmd := exec.Command(
		"powershell", "-NoProfile", "-Command",
		"param([string]$dir) "+
			"$current = [Environment]::GetEnvironmentVariable('PATH', 'User'); "+
			`[Environment]::SetEnvironmentVariable('PATH', "$dir;" + $current, 'User')`,
		dir,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to update user PATH: %w", err)
	}

	// Update current process so the caller sees it immediately.
	os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	fmt.Printf("✓ Added %s to user PATH (persistent). Restart your terminal for other sessions.\n", dir)
	return nil
}

func addToPathUnix(dir string) error {
	shell := filepath.Base(os.Getenv("SHELL"))
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home directory: %w", err)
	}

	var rcFile string
	var exportLine string

	switch shell {
	case "zsh":
		rcFile = filepath.Join(home, ".zshrc")
		exportLine = fmt.Sprintf(`export PATH=%s:$PATH`, shellQuote(dir))
	case "fish":
		rcFile = filepath.Join(home, ".config", "fish", "config.fish")
		exportLine = fmt.Sprintf("fish_add_path %s", shellQuote(dir))
	default: // bash and others
		rcFile = filepath.Join(home, ".bashrc")
		exportLine = fmt.Sprintf(`export PATH=%s:$PATH`, shellQuote(dir))
	}

	// Check if already present in rc file to avoid duplicates on re-runs.
	if data, err := os.ReadFile(rcFile); err == nil {
		if strings.Contains(string(data), exportLine) {
			fmt.Printf("✓ %s already references %s.\n", rcFile, dir)
			os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			return nil
		}
	}

	// Ensure parent dir exists (for fish config path).
	if err := os.MkdirAll(filepath.Dir(rcFile), 0o755); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}

	f, err := os.OpenFile(rcFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("opening %s: %w", rcFile, err)
	}
	defer f.Close()

	if _, err := fmt.Fprintf(f, "\n# Added by azd dev:install\n%s\n", exportLine); err != nil {
		return fmt.Errorf("writing to %s: %w", rcFile, err)
	}

	os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	fmt.Printf("✓ Added %s to %s. Restart your terminal or run: source %s\n", dir, rcFile, rcFile)
	return nil
}

// Coverage contains commands for measuring and reporting code coverage.
// Four modes mirror the developer workflow in docs/code-coverage-guide.md:
//
//	mage coverage:unit    — unit tests only (fast, no Azure resources)
//	mage coverage:full    — unit + integration tests locally (needs Azure)
//	mage coverage:hybrid  — unit locally + CI integration coverage
//	mage coverage:ci      — download latest CI combined coverage
//	mage coverage:html    — generate and open an HTML report
//	mage coverage:check   — enforce minimum coverage threshold
//	mage coverage:diff    — compare current branch vs main baseline
//	mage coverage:pr      — diff + post as PR comment
//
// See cli/azd/docs/code-coverage-guide.md for prerequisites and details.
type Coverage mg.Namespace

// Unit runs unit tests with coverage and shows a per-package report.
// This is the fastest mode — no Azure resources or login required.
//
// Usage: mage coverage:unit
func (Coverage) Unit() error {
	return runLocalCoverage("-UnitOnly", "-ShowReport")
}

// Full runs unit and integration/functional tests locally with coverage.
// Requires Azure resources configured per CONTRIBUTING.md prerequisites.
//
// Usage: mage coverage:full
func (Coverage) Full() error {
	return runLocalCoverage("-ShowReport")
}

// Hybrid runs unit tests locally and merges CI integration coverage.
// Requires 'az login' for Azure DevOps artifact access.
//
// Environment variables (optional):
//
//	COVERAGE_PULL_REQUEST_ID — target a specific PR's CI run
//	COVERAGE_BUILD_ID        — target a specific build ID
//
// Usage: mage coverage:hybrid
func (Coverage) Hybrid() error {
	args := []string{"-MergeWithCI", "-ShowReport"}
	if id := os.Getenv("COVERAGE_PULL_REQUEST_ID"); id != "" {
		args = append(args, "-PullRequestId", id)
	}
	if id := os.Getenv("COVERAGE_BUILD_ID"); id != "" {
		args = append(args, "-BuildId", id)
	}
	return runLocalCoverage(args...)
}

// CI downloads and displays the latest combined coverage from Azure DevOps CI.
// Requires 'az login' for Azure DevOps access.
//
// Environment variables (optional):
//
//	COVERAGE_BUILD_ID        — target a specific build ID
//	COVERAGE_PULL_REQUEST_ID — target a specific PR's CI run
//
// Usage: mage coverage:ci
func (Coverage) CI() error {
	args := []string{"-ShowReport"}
	if id := os.Getenv("COVERAGE_BUILD_ID"); id != "" {
		args = append(args, "-BuildId", id)
	}
	if id := os.Getenv("COVERAGE_PULL_REQUEST_ID"); id != "" {
		args = append(args, "-PullRequestId", id)
	}
	return runCICoverage(args...)
}

// Html generates and opens an HTML coverage report.
// Uses unit-only mode by default for speed.
//
// Environment variables (optional):
//
//	COVERAGE_MODE — "unit" (default), "full", or "hybrid"
//
// Usage: mage coverage:html
func (Coverage) Html() error {
	args := []string{"-Html", "-ShowReport"}
	switch os.Getenv("COVERAGE_MODE") {
	case "full":
		// default full mode — no extra flags
	case "hybrid":
		args = append(args, "-MergeWithCI")
	default:
		args = append(args, "-UnitOnly")
	}
	return runLocalCoverage(args...)
}

// Check enforces the minimum coverage threshold (default 50%).
// Runs unit-only coverage for speed, then validates against the threshold.
// The CI gate (55%) uses combined unit+integration coverage which is higher
// than unit-only; this target uses a lower default appropriate for unit-only.
//
// Environment variables (optional):
//
//	COVERAGE_MIN — minimum percentage (default "50")
//
// Usage: mage coverage:check
func (Coverage) Check() error {
	min := "50"
	if v := os.Getenv("COVERAGE_MIN"); v != "" {
		min = v
	}
	return runLocalCoverage("-UnitOnly", "-MinCoverage", min)
}

// Diff generates a coverage diff between the current branch and the main branch baseline.
// Uses cover-local.out as the current profile (run coverage:unit first) and downloads
// the main baseline from CI when needed.
//
// To avoid the CI download (which requires 'az login'), run coverage on main first
// and point to it:
//
//	COVERAGE_BASELINE=path/to/main-cover.out mage coverage:diff
//
// Environment variables (optional):
//
//	COVERAGE_BASELINE — path to baseline coverage profile (default: cover-ci-combined.out or download from CI)
//	COVERAGE_CURRENT  — path to current coverage profile (default: cover-local.out)
//
// Usage: mage coverage:diff
func (Coverage) Diff() error {
	repoRoot, err := findRepoRoot()
	if err != nil {
		return err
	}
	azdDir := filepath.Join(repoRoot, "cli", "azd")

	currentFile, err := resolveCoverageFile(
		os.Getenv("COVERAGE_CURRENT"),
		filepath.Join(azdDir, "cover-local.out"),
	)
	if err != nil {
		return fmt.Errorf("no current coverage profile: %w\nRun 'mage coverage:unit' first", err)
	}

	baselineFile, err := resolveBaselineFile(azdDir)
	if err != nil {
		return err
	}

	diffScript := filepath.Join(repoRoot, "eng", "scripts", "Get-CoverageDiff.ps1")
	return runPwshScript(azdDir, diffScript,
		"-BaselineFile", baselineFile,
		"-CurrentFile", currentFile,
	)
}

// PR generates a coverage diff and posts it as a comment on the current pull request.
// Requires: gh CLI authenticated, current branch must have an open PR.
//
// Re-running replaces the previous coverage comment (uses a tag for replacement).
//
// Environment variables (optional):
//
//	COVERAGE_BASELINE — path to baseline coverage profile (default: cover-ci-combined.out or download from CI)
//	COVERAGE_CURRENT  — path to current coverage profile (default: cover-local.out)
//
// Usage: mage coverage:pr
func (Coverage) PR() error {
	repoRoot, err := findRepoRoot()
	if err != nil {
		return err
	}
	azdDir := filepath.Join(repoRoot, "cli", "azd")

	currentFile, err := resolveCoverageFile(
		os.Getenv("COVERAGE_CURRENT"),
		filepath.Join(azdDir, "cover-local.out"),
	)
	if err != nil {
		return fmt.Errorf("no current coverage profile: %w\nRun 'mage coverage:unit' first", err)
	}

	baselineFile, err := resolveBaselineFile(azdDir)
	if err != nil {
		return err
	}

	// Generate diff markdown to a file
	diffFile := filepath.Join(azdDir, "coverage-diff.md")
	diffScript := filepath.Join(repoRoot, "eng", "scripts", "Get-CoverageDiff.ps1")
	if err := runPwshScript(azdDir, diffScript,
		"-BaselineFile", baselineFile,
		"-CurrentFile", currentFile,
		"-OutputFile", diffFile,
	); err != nil {
		return err
	}

	// Determine PR number from current branch
	prNumRaw, err := runCapture(azdDir, "gh", "pr", "view", "--json", "number", "--jq", ".number")
	if err != nil {
		return fmt.Errorf("no open PR for current branch (is 'gh' authenticated?): %w", err)
	}
	prNum := strings.TrimSpace(prNumRaw)

	// Determine repository slug (owner/repo)
	repoRaw, err := runCapture(azdDir, "gh", "repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner")
	if err != nil {
		return fmt.Errorf("cannot determine repository: %w", err)
	}
	repo := strings.TrimSpace(repoRaw)

	// Post coverage diff as a PR comment (replaces previous tagged comment)
	fmt.Printf("Posting coverage diff to %s#%s...\n", repo, prNum)
	updateScript := filepath.Join(repoRoot, "eng", "scripts", "Update-PRComment.ps1")
	return runPwshScript(azdDir, updateScript,
		"-Repo", repo,
		"-PRNumber", prNum,
		"-BodyFile", diffFile,
		"-Tag", "<!-- coverage-diff -->",
	)
}

// resolveCoverageFile returns envOverride if non-empty and existing,
// otherwise returns defaultPath if it exists.
func resolveCoverageFile(envOverride, defaultPath string) (string, error) {
	if envOverride != "" {
		if _, err := os.Stat(envOverride); err != nil {
			return "", fmt.Errorf("file not found: %s", envOverride)
		}
		return envOverride, nil
	}
	if _, err := os.Stat(defaultPath); err != nil {
		return "", fmt.Errorf("file not found: %s", defaultPath)
	}
	return defaultPath, nil
}

// resolveBaselineFile returns the baseline coverage profile path.
// Checks COVERAGE_BASELINE env var first, then cover-ci-combined.out,
// and downloads from CI as a last resort.
func resolveBaselineFile(azdDir string) (string, error) {
	if env := os.Getenv("COVERAGE_BASELINE"); env != "" {
		if _, err := os.Stat(env); err != nil {
			return "", fmt.Errorf("baseline file not found: %s", env)
		}
		return env, nil
	}

	defaultBaseline := filepath.Join(azdDir, "cover-ci-combined.out")
	if _, err := os.Stat(defaultBaseline); err == nil {
		return defaultBaseline, nil
	}

	fmt.Println("No baseline profile found. Downloading from CI main branch...")
	fmt.Println("(requires 'az login'; or set COVERAGE_BASELINE to skip download)")
	if err := runCICoverage(); err != nil {
		return "", fmt.Errorf("failed to download baseline: %w\nRun 'az login' or set COVERAGE_BASELINE env var", err)
	}
	if _, err := os.Stat(defaultBaseline); err != nil {
		return "", fmt.Errorf("CI download succeeded but baseline file not found at %s", defaultBaseline)
	}
	return defaultBaseline, nil
}

// findPwsh locates PowerShell (pwsh or powershell) on the system PATH.
func findPwsh() (string, error) {
	if p, err := exec.LookPath("pwsh"); err == nil {
		return p, nil
	}
	if runtime.GOOS == "windows" {
		if p, err := exec.LookPath("powershell"); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf(
		"PowerShell is required but not installed.\n  Install: https://aka.ms/install-powershell")
}

// runPwshScript runs a PowerShell script with GOWORK=off (mirroring CI).
func runPwshScript(dir, script string, args ...string) error {
	pwsh, err := findPwsh()
	if err != nil {
		return err
	}

	defer setEnvScoped("GOWORK", "off")()

	cmdArgs := append(
		[]string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", script},
		args...,
	)
	cmd := exec.Command(pwsh, cmdArgs...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("script %s failed: %w", filepath.Base(script), err)
	}
	return nil
}

// runLocalCoverage runs Get-LocalCoverageReport.ps1 with the given flags.
func runLocalCoverage(args ...string) error {
	repoRoot, err := findRepoRoot()
	if err != nil {
		return err
	}
	script := filepath.Join(repoRoot, "eng", "scripts", "Get-LocalCoverageReport.ps1")
	return runPwshScript(filepath.Join(repoRoot, "cli", "azd"), script, args...)
}

// runCICoverage runs Get-CICoverageReport.ps1 with the given flags.
func runCICoverage(args ...string) error {
	repoRoot, err := findRepoRoot()
	if err != nil {
		return err
	}
	script := filepath.Join(repoRoot, "eng", "scripts", "Get-CICoverageReport.ps1")
	return runPwshScript(filepath.Join(repoRoot, "cli", "azd"), script, args...)
}
