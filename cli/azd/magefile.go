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
//
// Checks are organized into two waves for faster execution:
//   - Wave 1 runs formatting, code modernization, copyright, lint, spell-check, and build
//     in parallel. Results are printed as each check completes.
//   - Wave 2 runs unit tests followed by playback tests sequentially (they share an
//     auto-built test binary and cannot safely overlap).
//
// Usage: mage preflight
func Preflight() error {
	azdDir, cleanup, err := mageInit()
	if err != nil {
		return err
	}
	defer cleanup()

	repoRoot := filepath.Dir(filepath.Dir(azdDir))

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
			return fmt.Errorf(
				"bash/sh not found — install Git for Windows: https://git-scm.com/downloads/win",
			)
		}
	}

	// Preallocated result slots — one per check, indexed by constant.
	// This avoids append races and keeps summary order deterministic.
	const (
		checkGofmt = iota
		checkGoFix
		checkCopyright
		checkLint
		checkCspell
		checkCspellMisc
		checkBuild
		checkTest
		checkPlayback
		numChecks
	)
	checkNames := [numChecks]string{
		"gofmt", "go fix", "copyright", "lint",
		"cspell", "cspell-misc", "build", "test", "playback tests",
	}

	type checkResult struct {
		status string // "pass", "fail", or "skip"
		detail string
		output string // captured stdout/stderr
	}
	results := make([]checkResult, numChecks)

	// printResult writes a completed check's output to the terminal.
	// Called under printMu so output from different checks doesn't interleave.
	var printMu sync.Mutex
	printResult := func(idx int) {
		printMu.Lock()
		defer printMu.Unlock()
		r := results[idx]
		icon := "✓"
		switch r.status {
		case "fail":
			icon = "✗"
		case "skip":
			icon = "−"
		}
		fmt.Printf("══ %s %s ══\n", icon, checkNames[idx])
		if r.output != "" {
			fmt.Print(r.output)
			if !strings.HasSuffix(r.output, "\n") {
				fmt.Println()
			}
		}
		if (r.status == "fail" || r.status == "skip") && r.detail != "" && r.detail != r.output {
			fmt.Println(r.detail)
		}
	}

	// ── Wave 1: parallel formatting, lint, spell-check, build ──────────
	fmt.Println("══ Wave 1: checks (parallel) ══")

	var wg sync.WaitGroup

	// 1. gofmt
	wg.Go(func() {
		out, err := runCapture(azdDir, "gofmt", "-s", "-l", ".")
		if err != nil {
			results[checkGofmt] = checkResult{"fail", err.Error(), out}
		} else if len(strings.TrimSpace(out)) > 0 {
			results[checkGofmt] = checkResult{
				"fail", "unformatted files:", out,
			}
		} else {
			results[checkGofmt] = checkResult{"pass", "", ""}
		}
		printResult(checkGofmt)
	})

	// 2. go fix
	wg.Go(func() {
		out, err := runCapture(azdDir, "go", "fix", "-diff", "./...")
		if err != nil {
			results[checkGoFix] = checkResult{"fail", err.Error(), out}
		} else if len(strings.TrimSpace(out)) > 0 {
			results[checkGoFix] = checkResult{
				"fail",
				"code should be modernized — run 'go fix ./...' to apply:",
				out,
			}
		} else {
			results[checkGoFix] = checkResult{"pass", "", ""}
		}
		printResult(checkGoFix)
	})

	// 3. Copyright headers
	wg.Go(func() {
		script := filepath.Join(repoRoot, "eng", "scripts", "copyright-check.sh")
		if _, err := os.Stat(script); err != nil {
			results[checkCopyright] = checkResult{
				"fail", "script not found: " + script, "",
			}
		} else if out, err := runCaptureShellScript(
			azdDir, shell, script, ".",
		); err != nil {
			results[checkCopyright] = checkResult{"fail", err.Error(), out}
		} else {
			results[checkCopyright] = checkResult{"pass", "", ""}
		}
		printResult(checkCopyright)
	})

	// 4. golangci-lint
	wg.Go(func() {
		out, err := runCaptureAll(azdDir, nil, "golangci-lint", "run", "./...")
		if err != nil {
			results[checkLint] = checkResult{"fail", err.Error(), out}
		} else {
			results[checkLint] = checkResult{"pass", "", ""}
		}
		printResult(checkLint)
	})

	// 5a. cspell (Go source)
	wg.Go(func() {
		out, err := runCaptureAll(azdDir, nil,
			"cspell", "lint", "**/*.go",
			"--relative", "--config", "./.vscode/cspell.yaml", "--no-progress")
		if err != nil {
			results[checkCspell] = checkResult{"fail", err.Error(), out}
		} else {
			results[checkCspell] = checkResult{"pass", "", ""}
		}
		printResult(checkCspell)
	})

	// 5b. cspell (misc/docs)
	wg.Go(func() {
		out, err := runCaptureAll(repoRoot, nil,
			"cspell", "lint", "**/*",
			"--relative", "--config", "./.vscode/cspell.misc.yaml", "--no-progress")
		if err != nil {
			results[checkCspellMisc] = checkResult{"fail", err.Error(), out}
		} else {
			results[checkCspellMisc] = checkResult{"pass", "", ""}
		}
		printResult(checkCspellMisc)
	})

	// 6. go build — compile all packages AND pre-build the azd + azd-record
	// binaries so that Wave 2 tests can skip auto-building. This lets unit
	// tests and playback tests run in parallel safely.
	wg.Go(func() {
		// Compile all packages first (catches errors everywhere).
		out, err := runCaptureAll(azdDir, nil, "go", "build", "./...")
		if err != nil {
			results[checkBuild] = checkResult{"fail", err.Error(), out}
			printResult(checkBuild)
			return
		}
		// Build the azd binary that unit tests need.
		azdBin := "azd"
		if runtime.GOOS == "windows" {
			azdBin = "azd.exe"
		}
		out, err = runCaptureAll(azdDir, nil, "go", "build", "-o", azdBin, ".")
		if err != nil {
			results[checkBuild] = checkResult{"fail", err.Error(), out}
			printResult(checkBuild)
			return
		}
		// Build the azd-record binary that playback tests need.
		recordBin := "azd-record"
		if runtime.GOOS == "windows" {
			recordBin = "azd-record.exe"
		}
		out, err = runCaptureAll(
			azdDir, nil, "go", "build", "-tags=record", "-o", recordBin, ".",
		)
		if err != nil {
			results[checkBuild] = checkResult{"fail", err.Error(), out}
			printResult(checkBuild)
			return
		}
		results[checkBuild] = checkResult{"pass", "", ""}
		printResult(checkBuild)
	})

	wg.Wait()

	// ── Wave 2: tests (parallel — binaries pre-built in Wave 1) ───────
	// Both test suites use CLI_TEST_SKIP_BUILD=true so they don't attempt
	// to rebuild the azd binary, eliminating the file-level race that
	// previously forced sequential execution.
	fmt.Println("\n══ Wave 2: tests (parallel) ══")

	if results[checkBuild].status == "fail" {
		results[checkTest] = checkResult{"skip", "skipped (build failed)", ""}
		printResult(checkTest)
		results[checkPlayback] = checkResult{"skip", "skipped (build failed)", ""}
		printResult(checkPlayback)
	} else {
		skipBuildEnv := []string{"CLI_TEST_SKIP_BUILD=true"}
		var wg2 sync.WaitGroup

		// 7. Unit tests
		wg2.Go(func() {
			if err := runStreamingWithEnv(
				azdDir, skipBuildEnv,
				"go", "test", "./...", "-short", "-cover", "-count=1",
			); err != nil {
				results[checkTest] = checkResult{"fail", err.Error(), ""}
			} else {
				results[checkTest] = checkResult{"pass", "", ""}
			}
			printResult(checkTest)
		})

		// 8. Playback tests
		wg2.Go(func() {
			if err := runFunctionalTests(azdDir, testRunOpts{
				mode: "playback",
				env:  skipBuildEnv,
			}); err != nil {
				results[checkPlayback] = checkResult{"fail", err.Error(), ""}
			} else {
				results[checkPlayback] = checkResult{"pass", "", ""}
			}
			printResult(checkPlayback)
		})

		wg2.Wait()
	}

	// ── Summary ────────────────────────────────────────────────────────
	failed := false
	fmt.Println("\n══════════════════════════")
	fmt.Println("  Preflight Summary")
	fmt.Println("══════════════════════════")
	for i, r := range results {
		icon := "✓"
		switch r.status {
		case "fail":
			icon = "✗"
			failed = true
		case "skip":
			icon = "−"
		}
		fmt.Printf("  %s %s\n", icon, checkNames[i])
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
	azdDir, cleanup, err := mageInit()
	if err != nil {
		return err
	}
	defer cleanup()

	return runFunctionalTests(azdDir, testRunOpts{mode: "playback"})
}

// Record re-records functional test playback cassettes against live Azure.
// Usage:
//
//	mage record                              # re-record ALL playback tests
//	mage record -filter=TestName             # re-record tests matching filter
//
// Requires:
//   - Azure authentication (azd auth login)
//   - Test subscription config (azd config set defaults.test.subscription/tenant/location)
//     OR equivalent AZD_TEST_* environment variables
//
// The azd-record binary (built with -tags=record) is built on demand by
// azdcli.NewCLI via buildRecordOnce when tests run in record mode. To use a
// custom pre-built binary, set CLI_TEST_AZD_PATH before invoking mage record.
func Record(filter *string) error {
	azdDir, cleanup, err := mageInit()
	if err != nil {
		return err
	}
	defer cleanup()

	return runFunctionalTests(azdDir, testRunOpts{
		mode:    "record",
		filter:  filter,
		verbose: true,
	})
}

// mageInit sets up the standard mage environment (GOWORK=off, GOTOOLCHAIN pin)
// and returns the cli/azd directory path. Callers must defer the returned
// cleanup function to restore environment variables.
func mageInit() (azdDir string, cleanup func(), err error) {
	restoreGowork := setEnvScoped("GOWORK", "off")

	repoRoot, err := findRepoRoot()
	if err != nil {
		restoreGowork()
		return "", nil, err
	}
	azdDir = filepath.Join(repoRoot, "cli", "azd")

	// Pin GOTOOLCHAIN to the version declared in go.mod when it isn't already
	// set. Prevents parallel compilation races when the system Go version
	// differs from go.mod (see Preflight for full rationale).
	var restoreToolchain func()
	if _, hasToolchain := os.LookupEnv("GOTOOLCHAIN"); !hasToolchain {
		if ver, err := goModVersion(azdDir); err == nil && ver != "" {
			restoreToolchain = setEnvScoped("GOTOOLCHAIN", "go"+ver)
		}
	}

	cleanup = func() {
		if restoreToolchain != nil {
			restoreToolchain()
		}
		restoreGowork()
	}
	return azdDir, cleanup, nil
}

// testRunOpts configures how runFunctionalTests discovers and executes
// functional tests with recorded HTTP interactions.
type testRunOpts struct {
	mode    string   // "record" or "playback"
	filter  *string  // optional test name filter (substring match)
	verbose bool     // add -v flag
	env     []string // additional env vars beyond AZURE_RECORD_MODE
}

// runFunctionalTests discovers playback tests from recordings, applies an
// optional name filter, and runs them via "go test" with the given mode
// and environment variables.
func runFunctionalTests(azdDir string, opts testRunOpts) error {
	recordingsDir := filepath.Join(
		azdDir, "test", "functional", "testdata", "recordings",
	)
	names, err := discoverPlaybackTests(recordingsDir)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		fmt.Printf("No recording files found — skipping %s tests.\n", opts.mode)
		return nil
	}

	// Apply optional test filter.
	if opts.filter != nil {
		var filtered []string
		for _, name := range names {
			if strings.Contains(name, *opts.filter) || name == *opts.filter {
				filtered = append(filtered, name)
			}
		}
		if len(filtered) == 0 {
			fmt.Printf("No playback tests match filter %q. Available tests:\n", *opts.filter)
			for _, name := range names {
				fmt.Printf("  • %s\n", name)
			}
			return fmt.Errorf("no tests match filter %q", *opts.filter)
		}
		names = filtered
	}

	// Build test -run pattern from discovered names.
	escaped := make([]string, len(names))
	for i, name := range names {
		escaped[i] = regexp.QuoteMeta(name)
	}
	pattern := "^(" + strings.Join(escaped, "|") + ")(/|$)"

	fmt.Printf("Running %d test(s) in %s mode...\n", len(names), opts.mode)
	if opts.verbose {
		for _, name := range names {
			fmt.Printf("  • %s\n", name)
		}
	}

	env := []string{"AZURE_RECORD_MODE=" + opts.mode}
	env = append(env, opts.env...)

	args := []string{"test", "-run", pattern, "./test/functional", "-timeout", "30m", "-count=1"}
	if opts.verbose {
		args = append(args, "-v")
	}

	return runStreamingWithEnv(azdDir, env, "go", args...)
}

// excludedPlaybackTests lists tests whose recordings are known to be stale.
// These are excluded from automatic playback so they don't block preflight.
// Re-record the test (mage record -filter=<name>) to remove it from this list.
// Re-recording requires access to the TME subscription — see CONTRIBUTING.md.
var excludedPlaybackTests = map[string]string{
	"Test_CLI_VsServer":                "stale recording; re-record requires TME access (#7780)",
	"Test_CLI_Deploy_SlotDeployment":   "stale recording; re-record requires TME access (#7780)",
	"Test_CLI_Up_Down_ContainerAppJob": "stale recording; re-record requires TME access (#7014)",
}

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

// runCaptureAll runs a command and returns its combined stdout/stderr as a
// string. Unlike runCapture it also accepts extra environment variables.
// This is used by parallel checks that cannot stream directly to the terminal.
func runCaptureAll(
	dir string, env []string, name string, args ...string,
) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

// runCaptureShellScript is like runShellScript but captures output instead of
// streaming. Used during parallel checks.
func runCaptureShellScript(
	dir string, shell string, script string, args ...string,
) (string, error) {
	if runtime.GOOS == "windows" {
		shellScript := toShellPath(shell, script)
		shellDir := toShellPath(shell, dir)
		quotedArgs := make([]string, len(args))
		for i, a := range args {
			quotedArgs[i] = shellQuote(a)
		}
		inner := fmt.Sprintf(`cd %s && tr -d '\r' < %s | bash -s -- %s`,
			shellQuote(shellDir), shellQuote(shellScript),
			strings.Join(quotedArgs, " "))
		cmd := exec.Command(shell, "-c", inner)
		var buf bytes.Buffer
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		err := cmd.Run()
		return buf.String(), err
	}

	cmdArgs := append([]string{script}, args...)
	cmd := exec.Command(shell, cmdArgs...)
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
