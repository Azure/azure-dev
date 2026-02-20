// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build mage

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// DevInstall builds azd from source as 'azd-dev' and installs it to ~/.azd/bin.
// The binary is named azd-dev to avoid conflicting with a production azd install.
// Automatically adds ~/.azd/bin to PATH if not already present.
//
// Usage: mage devinstall
func DevInstall() error {
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

// DevUninstall removes the azd-dev binary from ~/.azd/bin.
// The PATH entry is left intact.
//
// Usage: mage devuninstall
func DevUninstall() error {
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
// spell checking, compilation, and unit tests. Reports a summary of all results at the end.
//
// Usage: mage preflight
func Preflight() error {
	repoRoot, err := findRepoRoot()
	if err != nil {
		return err
	}
	azdDir := filepath.Join(repoRoot, "cli", "azd")

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
		"go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"); err != nil {
		return err
	}
	if err := requireTool("cspell", "npm install -g cspell"); err != nil {
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

	// 2. Copyright headers
	fmt.Println("══ Copyright headers ══")
	script := filepath.Join(repoRoot, "eng", "scripts", "copyright-check.sh")
	if _, err := os.Stat(script); err != nil {
		record("copyright", "fail", "script not found: "+script)
	} else if err := runShellScript(azdDir, shell, script, "."); err != nil {
		record("copyright", "fail", err.Error())
	} else {
		record("copyright", "pass", "")
	}

	// 3. golangci-lint
	fmt.Println("══ Lint (golangci-lint) ══")
	if err := runStreaming(azdDir, "golangci-lint", "run", "./..."); err != nil {
		record("lint", "fail", err.Error())
	} else {
		record("lint", "pass", "")
	}

	// 4. Spell check (cspell)
	fmt.Println("══ Spell check (cspell) ══")
	if err := runStreaming(azdDir, "cspell", "lint", "**/*.go",
		"--relative", "--config", "./.vscode/cspell.yaml", "--no-progress"); err != nil {
		record("cspell", "fail", err.Error())
	} else {
		record("cspell", "pass", "")
	}

	// 5. Compile check
	fmt.Println("══ Build (go build) ══")
	if err := runStreaming(azdDir, "go", "build", "./..."); err != nil {
		record("build", "fail", err.Error())
	} else {
		record("build", "pass", "")
	}

	// 6. Unit tests
	fmt.Println("══ Unit tests (go test -short) ══")
	if err := runStreaming(azdDir, "go", "test", "./...", "-short", "-count=1"); err != nil {
		record("test", "fail", err.Error())
	} else {
		record("test", "pass", "")
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
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
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

		// Build: cd <dir> && tr -d '\r' < script.sh | bash -s -- args...
		// This strips CRLF line endings that WSL bash chokes on.
		shellDir := toShellPath(shell, dir)
		inner := fmt.Sprintf(`cd %s && tr -d '\r' < %s | bash -s -- %s`,
			shellDir, shellScript, strings.Join(args, " "))
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

// toShellPath converts a Windows path to a unix-style path for the given shell.
// WSL bash expects /mnt/c/..., Git-for-Windows bash expects /c/...
func toShellPath(shell, winPath string) string {
	p := filepath.ToSlash(winPath)
	if len(p) >= 2 && p[1] == ':' {
		drive := strings.ToLower(string(p[0]))
		rest := p[2:]

		// Detect WSL vs Git-for-Windows bash by checking if /mnt exists in the shell.
		out, err := exec.Command(shell, "-c", "test -d /mnt/c && echo wsl").Output()
		if err == nil && strings.TrimSpace(string(out)) == "wsl" {
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
	script := fmt.Sprintf(
		`$current = [Environment]::GetEnvironmentVariable('PATH', 'User'); `+
			`[Environment]::SetEnvironmentVariable('PATH', '%s;' + $current, 'User')`,
		dir,
	)
	cmd := exec.Command("powershell", "-NoProfile", "-Command", script)
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
		exportLine = fmt.Sprintf(`export PATH="%s:$PATH"`, dir)
	case "fish":
		rcFile = filepath.Join(home, ".config", "fish", "config.fish")
		exportLine = fmt.Sprintf("fish_add_path %s", dir)
	default: // bash and others
		rcFile = filepath.Join(home, ".bashrc")
		exportLine = fmt.Sprintf(`export PATH="%s:$PATH"`, dir)
	}

	// Check if already present in rc file to avoid duplicates on re-runs.
	if data, err := os.ReadFile(rcFile); err == nil {
		if strings.Contains(string(data), dir) {
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

	if _, err := fmt.Fprintf(f, "\n# Added by azd devinstall\n%s\n", exportLine); err != nil {
		return fmt.Errorf("writing to %s: %w", rcFile, err)
	}

	os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	fmt.Printf("✓ Added %s to %s. Restart your terminal or run: source %s\n", dir, rcFile, rcFile)
	return nil
}
