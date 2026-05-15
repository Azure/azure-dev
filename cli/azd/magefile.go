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
	if opts.mode != "record" && opts.mode != "playback" {
		return fmt.Errorf("invalid test mode %q: must be %q or %q", opts.mode, "record", "playback")
	}

	recordingsDir := filepath.Join(
		azdDir, "test", "functional", "testdata", "recordings",
	)
	// In record mode, ignore the stale-recording exclusion list so users can
	// re-record those tests via `mage record -filter=<name>`. In playback
	// mode, skip them so they don't block preflight.
	names, err := discoverPlaybackTests(recordingsDir, opts.mode == "playback")
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
			fmt.Printf("No tests match filter %q in %s mode. Available tests:\n", *opts.filter, opts.mode)
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
	// Recordings affected by feat/exegraph: the graph-driven up/provision path
	// introduces legitimate new HTTP interactions (layer hash probes, resource-group
	// existence checks). Must be re-recorded with live Azure credentials before merge.
	"Test_DeploymentStacks":                         "needs re-record for feat/exegraph graph-driven provision",
	"Test_CLI_ProvisionState":                       "needs re-record for feat/exegraph graph-driven provision",
	"Test_CLI_InfraCreateAndDeleteUpperCase":        "needs re-record for feat/exegraph graph-driven provision",
	"Test_CLI_PreflightQuota_Sub_DefaultCapacity":   "stale recording; missing extension registry + resource group interactions",
	"Test_CLI_PreflightQuota_Sub_InvalidModelName":  "stale recording; missing extension registry + resource group interactions",
	"Test_CLI_PreflightQuota_Sub_DifferentLocation": "stale recording; missing extension registry + resource group interactions",
	"Test_CLI_PreflightQuota_RG_DefaultCapacity":    "stale recording; missing extension registry + resource group interactions",
	"Test_CLI_PreflightQuota_RG_InvalidVersion":     "stale recording; missing extension registry + resource group interactions",
	"Test_CLI_PreflightQuota_RG_InvalidModelName":   "stale recording; missing extension registry + resource group interactions",
}

// discoverPlaybackTests scans the recordings directory for .yaml files and
// subdirectories, returning unique top-level Go test function names. When
// applyExclusions is true, tests in excludedPlaybackTests are filtered out
// (used in playback mode to avoid blocking preflight on known-stale
// recordings). In record mode, callers pass false so excluded tests can
// be re-recorded via `mage record -filter=<name>`.
func discoverPlaybackTests(recordingsDir string, applyExclusions bool) ([]string, error) {
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

	// Remove tests with known stale recordings, but only when requested
	// (i.e. in playback mode). Record mode needs to see all tests so users
	// can re-record the excluded ones via `mage record -filter=<name>`.
	if applyExclusions {
		for name := range excludedPlaybackTests {
			delete(seen, name)
		}
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
//	mage coverage:pr      — preview the CI PR coverage gate (fail-loud)
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
// By default this is purely advisory: it prints a per-package report without enforcing
// any gates, so local invocations like `mage coverage:diff` don't show noisy
// "RESULT: FAIL" lines just because a single package drifted past CI's 0.5 pp tolerance
// during exploration. To preview the CI gate locally, either set
// COVERAGE_FAIL_ON_DECREASE=1 (which activates CI defaults: 0.5 pp per package +
// 69% floor) or use `mage coverage:pr`.
//
// On a feature branch (not main / detached HEAD), Diff resolves PR-touched .go files
// via `git fetch origin main` + `git diff origin/main...HEAD` and passes them to the
// underlying script so the per-package report is scoped to packages containing
// touched files — matching the CI gate run by code-coverage-upload.yml.
//
// To avoid the CI download (which requires 'az login'), run coverage on main first
// and point to it:
//
//	COVERAGE_BASELINE=path/to/main-cover.out mage coverage:diff
//
// Environment variables (optional):
//
//	COVERAGE_BASELINE                   — path to baseline coverage profile (default: cover-ci-combined.out or download from CI)
//	COVERAGE_CURRENT                    — path to current coverage profile (default: cover-local.out)
//	COVERAGE_MAX_PACKAGE_DECREASE       — per-package coverage decrease tolerance in percentage points
//	                                      (defaults: 0.5 when COVERAGE_FAIL_ON_DECREASE=1; gate disabled otherwise)
//	COVERAGE_MIN_OVERALL                — absolute floor for overall coverage in percent
//	                                      (defaults: 69.0 when COVERAGE_FAIL_ON_DECREASE=1; gate disabled otherwise)
//	COVERAGE_FAIL_ON_DECREASE           — "1" or "true" to exit non-zero when EITHER gate is breached
//	                                      (per-package decrease or absolute floor); also activates default thresholds
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

	args := []string{
		"-BaselineFile", baselineFile,
		"-CurrentFile", currentFile,
	}

	failOnDecrease := false
	if fail := os.Getenv("COVERAGE_FAIL_ON_DECREASE"); fail == "1" || strings.EqualFold(fail, "true") {
		failOnDecrease = true
	}

	changedFiles, err := resolveChangedFilesForDiff(azdDir, failOnDecrease)
	if err != nil {
		return err
	}
	if changedFiles != "" {
		args = append(args, "-ChangedFilesFromFile", changedFiles)
	}

	// Pass user-supplied thresholds explicitly. When the user opts into
	// fail-loud mode (COVERAGE_FAIL_ON_DECREASE=1) and hasn't set a
	// threshold, omit the flag entirely so the script's own defaults rule —
	// keeps a single source of truth (Get-CoverageDiff.ps1) and prevents
	// drift between mage and CI.
	// In advisory mode (default), neutralize the gates (max=100 / min=0) so
	// local exploration runs don't print "RESULT: FAIL" noise just because
	// a single package drifted past CI's defaults during exploration.
	if maxPkg := os.Getenv("COVERAGE_MAX_PACKAGE_DECREASE"); maxPkg != "" {
		args = append(args, "-MaxPackageDecrease", maxPkg)
	} else if !failOnDecrease {
		args = append(args, "-MaxPackageDecrease", "100")
	}

	if minOverall := os.Getenv("COVERAGE_MIN_OVERALL"); minOverall != "" {
		args = append(args, "-MinOverallCoverage", minOverall)
	} else if !failOnDecrease {
		args = append(args, "-MinOverallCoverage", "0")
	}

	if failOnDecrease {
		args = append(args, "-FailOnGate")
	}

	diffScript := filepath.Join(repoRoot, "eng", "scripts", "Get-CoverageDiff.ps1")
	return runPwshScript(azdDir, diffScript, args...)
}

// PR previews the CI PR coverage gate locally: same fail-loud per-package
// decrease and absolute-floor gates that code-coverage-upload.yml runs
// against the latest successful main build. No PR comment is posted (the CI
// gate surfaces breaches in the build log; this target lets contributors
// repro the gate locally before pushing).
//
// Resolves changed files via `git fetch origin main` + `git merge-base origin/main HEAD`
// + `git diff` so the per-package report is scoped to packages containing PR-touched
// files. Requires a feature branch with origin/main reachable; on main or detached
// HEAD, or when git resolution fails (e.g. no remote / shallow clone), the target
// returns an actionable error rather than silently passing — the "preview"
// guarantee depends on the same package set CI displays.
//
// Environment variables (optional):
//
//	COVERAGE_BASELINE             — path to baseline coverage profile (default: cover-ci-combined.out or download from CI)
//	COVERAGE_CURRENT              — path to current coverage profile (default: cover-local.out)
//	COVERAGE_MAX_PACKAGE_DECREASE — per-package coverage decrease tolerance in pp (default: from Get-CoverageDiff.ps1, currently 0.5)
//	COVERAGE_MIN_OVERALL          — absolute floor for overall coverage in percent (default: from Get-CoverageDiff.ps1, currently 69)
//
// Defaults intentionally come from the underlying script so both `mage coverage:pr`
// and the CI pipeline read from a single source of truth.
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

	changedFiles, err := resolveChangedFilesForDiff(azdDir, true)
	if err != nil {
		return err
	}
	if changedFiles == "" {
		return fmt.Errorf(
			"coverage:pr requires a feature branch with origin/main reachable; " +
				"run on a feature branch and ensure 'git fetch origin main' succeeds",
		)
	}

	args := []string{
		"-BaselineFile", baselineFile,
		"-CurrentFile", currentFile,
		"-FailOnGate",
		"-ChangedFilesFromFile", changedFiles,
	}

	// Only forward thresholds when the user explicitly set them. Otherwise
	// let Get-CoverageDiff.ps1's own defaults rule so there's a single
	// source of truth shared between mage and CI.
	if v := os.Getenv("COVERAGE_MAX_PACKAGE_DECREASE"); v != "" {
		args = append(args, "-MaxPackageDecrease", v)
	}
	if v := os.Getenv("COVERAGE_MIN_OVERALL"); v != "" {
		args = append(args, "-MinOverallCoverage", v)
	}

	diffScript := filepath.Join(repoRoot, "eng", "scripts", "Get-CoverageDiff.ps1")
	return runPwshScript(azdDir, diffScript, args...)
}

// Report merges Go cover-data inputs and writes a textfmt cover.out, mirroring
// the same `go tool covdata merge` + `go tool covdata textfmt` plumbing that
// `mage coverage:ci` uses internally. CI invokes this target so the upload
// stage and the local `mage coverage:*` targets share one coverage-reporting
// path — no second source of truth.
//
// Environment variables:
//
//	COVERAGE_REPORT_UNIT_INPUTS — comma-separated paths to per-platform unit covdata directories (required)
//	COVERAGE_REPORT_INT_INPUTS  — comma-separated paths to per-platform integration covdata directories (optional)
//	COVERAGE_REPORT_OUTPUT      — path to the textfmt output file (required, e.g. cover.out)
//	COVERAGE_REPORT_MERGED_DIR  — directory to write the merged covdata into (default: <azdDir>/cover-merged)
//
// Usage: mage coverage:report
func (Coverage) Report() error {
	unitInputs := os.Getenv("COVERAGE_REPORT_UNIT_INPUTS")
	if unitInputs == "" {
		return fmt.Errorf("COVERAGE_REPORT_UNIT_INPUTS is required (comma-separated covdata directories)")
	}
	output := os.Getenv("COVERAGE_REPORT_OUTPUT")
	if output == "" {
		return fmt.Errorf("COVERAGE_REPORT_OUTPUT is required (path to textfmt cover.out)")
	}

	repoRoot, err := findRepoRoot()
	if err != nil {
		return err
	}
	azdDir := filepath.Join(repoRoot, "cli", "azd")

	mergedDir := os.Getenv("COVERAGE_REPORT_MERGED_DIR")
	if mergedDir == "" {
		mergedDir = filepath.Join(azdDir, "cover-merged")
	}

	if err := os.MkdirAll(mergedDir, 0o755); err != nil {
		return fmt.Errorf("creating merged dir %q: %w", mergedDir, err)
	}

	intInputs := os.Getenv("COVERAGE_REPORT_INT_INPUTS")

	// Per-stream merge: when integration inputs are provided we merge each
	// stream individually first so the final merge can combine both streams.
	// Place intermediate dirs INSIDE mergedDir so concurrent invocations
	// with different mergedDir values (e.g. PR pipeline merging current and
	// baseline coverage in the same job) don't share intermediate paths and
	// contaminate each other's covdata.
	tmpUnit := filepath.Join(mergedDir, "unit-tmp")
	tmpInt := filepath.Join(mergedDir, "int-tmp")

	if intInputs != "" {
		// Clean any stale intermediate dir from a prior run before merging
		// to avoid mixing previous covcounters/covmeta into this run.
		if err := os.RemoveAll(tmpUnit); err != nil {
			return fmt.Errorf("cleaning stale unit merge dir %q: %w", tmpUnit, err)
		}
		if err := os.RemoveAll(tmpInt); err != nil {
			return fmt.Errorf("cleaning stale int merge dir %q: %w", tmpInt, err)
		}
		if err := os.MkdirAll(tmpUnit, 0o755); err != nil {
			return fmt.Errorf("creating unit merge dir: %w", err)
		}
		if err := os.MkdirAll(tmpInt, 0o755); err != nil {
			return fmt.Errorf("creating int merge dir: %w", err)
		}
		if err := runGoTool(azdDir, "covdata", "merge", "-i="+unitInputs, "-o="+tmpUnit); err != nil {
			return fmt.Errorf("merging unit covdata: %w", err)
		}
		if err := runGoTool(azdDir, "covdata", "merge", "-i="+intInputs, "-o="+tmpInt); err != nil {
			return fmt.Errorf("merging integration covdata: %w", err)
		}
		combined := tmpUnit + "," + tmpInt
		if err := runGoTool(azdDir, "covdata", "merge", "-i="+combined, "-o="+mergedDir); err != nil {
			return fmt.Errorf("merging combined covdata: %w", err)
		}
	} else {
		if err := runGoTool(azdDir, "covdata", "merge", "-i="+unitInputs, "-o="+mergedDir); err != nil {
			return fmt.Errorf("merging unit covdata: %w", err)
		}
	}

	if err := runGoTool(azdDir, "covdata", "textfmt", "-i="+mergedDir, "-o="+output); err != nil {
		return fmt.Errorf("converting covdata to textfmt: %w", err)
	}

	fmt.Printf("Wrote merged textfmt coverage profile to %s\n", output)
	return nil
}

// runGoTool runs `go tool <args...>` inside the given directory and streams
// stdout/stderr through the parent process. Used by Coverage.Report to wrap
// `go tool covdata` invocations so the same pipeline plumbing is exercised
// in CI and locally.
func runGoTool(dir string, args ...string) error {
	full := append([]string{"tool"}, args...)
	cmd := exec.Command("go", full...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// resolveChangedFilesForDiff returns the path to a file listing PR-touched
// files used to scope the per-package coverage gate, or an
// empty string when changed-file mode should be skipped (on main, detached
// HEAD, or when git resolution fails).
//
// When strict is false, git resolution failures degrade silently to advisory
// mode. When strict is true, failures return an actionable error so the caller
// (e.g. `mage coverage:pr`, which always enforces the floor) doesn't pass with
// a green-when-CI-is-red false positive.
//
// Mirrors the resolution done by code-coverage-upload.yml so local
// `mage coverage:diff` / `coverage:pr` runs against the same file set CI checks.
func resolveChangedFilesForDiff(azdDir string, strict bool) (string, error) {
	skipOrFail := func(reason string) (string, error) {
		if strict {
			return "", fmt.Errorf("cannot resolve changed files for coverage gate: %s", reason)
		}
		return "", nil
	}

	branch, err := runCapture(azdDir, "git", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return skipOrFail(fmt.Sprintf("git rev-parse failed: %v", err))
	}
	branch = strings.TrimSpace(branch)
	if branch == "" || branch == "HEAD" {
		return skipOrFail("detached HEAD or empty branch name")
	}
	if branch == "main" {
		return skipOrFail("on main branch (no PR diff to compute)")
	}

	// Best-effort fetch so origin/main is fresh; mirrors code-coverage-upload.yml.
	// Any error here is non-fatal — the next step will surface a usable error if
	// origin/main truly isn't reachable.
	_, _ = runCapture(azdDir, "git", "fetch", "--no-tags", "--depth=200", "origin", "main")

	base, err := runCapture(azdDir, "git", "merge-base", "origin/main", "HEAD")
	if err != nil {
		return skipOrFail(fmt.Sprintf("git merge-base origin/main HEAD failed: %v", err))
	}
	base = strings.TrimSpace(base)
	if base == "" {
		return skipOrFail("empty merge-base result")
	}

	diff, err := runCapture(azdDir, "git", "diff", "--name-only", "--no-renames", "--diff-filter=AMRD", base+"...HEAD")
	if err != nil {
		return skipOrFail(fmt.Sprintf("git diff failed: %v", err))
	}
	diff = strings.TrimSpace(diff)
	if diff == "" {
		return skipOrFail("no files changed vs origin/main")
	}

	// Use os.CreateTemp (not a fixed name in TempDir) so two concurrent
	// `mage coverage:diff` / `coverage:pr` runs on the same machine can't
	// clobber each other's file list and silently produce wrong gate results.
	f, err := os.CreateTemp("", "azd-coverage-changed-files-*.txt")
	if err != nil {
		return "", fmt.Errorf("creating changed-files temp file: %w", err)
	}
	out := f.Name()
	f.Close()
	if err := os.WriteFile(out, []byte(diff+"\n"), 0o644); err != nil {
		return "", fmt.Errorf("writing changed files list: %w", err)
	}
	return out, nil
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
