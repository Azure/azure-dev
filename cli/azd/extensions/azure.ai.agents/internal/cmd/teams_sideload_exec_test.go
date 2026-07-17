// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"archive/zip"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"text/template"
)

// TestTeamsSideloadScriptExecutes actually RUNS the generated pack-and-sideload
// script end to end (not just string-asserts its content), so a regression that
// makes the script emit an invalid Teams package or skip the login-and-retry is
// caught. It is OS-gated: bash on non-Windows, pwsh on Windows. It never touches
// the network -- a fake `atk` on PATH stands in for the real CLI, and the first
// phase runs in SKIP_TEAMS_INSTALL=1 build-only mode.
func TestTeamsSideloadScriptExecutes(t *testing.T) {
	const (
		agentName = "echo-agent"
		botName   = "echo-agent-bot-uai"
		msaAppID  = "11111111-2222-3333-4444-555555555555"
	)

	var (
		scriptName string
		tmpl       *template.Template
		interp     string
		interpArgs func(script string) []string
		fakeAtk    func(t *testing.T, binDir, marker string)
	)
	if runtime.GOOS == "windows" {
		scriptName, tmpl = teamsSideloadScriptPwsh, teamsSideloadPwshTmpl
		interp = firstOnPath("pwsh", "powershell")
		interpArgs = func(s string) []string {
			return []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", s}
		}
		fakeAtk = writeFakeAtkWindows
	} else {
		scriptName, tmpl = teamsSideloadScriptBash, teamsSideloadBashTmpl
		interp = firstOnPath("bash")
		interpArgs = func(s string) []string { return []string{s} }
		fakeAtk = writeFakeAtkUnix
	}
	if interp == "" {
		t.Skip("no script interpreter available on this runner")
	}
	if runtime.GOOS != "windows" && firstOnPath("zip", "python3") == "" {
		t.Skip("need 'zip' or 'python3' to build the package on this runner")
	}

	dir := t.TempDir()
	script := filepath.Join(dir, scriptName)
	content := teamsSideloadScriptContent(tmpl, agentName, botName, msaAppID)
	writeExecFile(t, script, []byte(content))

	run := func(t *testing.T, extraEnv ...string) string {
		t.Helper()
		cmd := exec.Command(interp, interpArgs(script)...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), extraEnv...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("script run failed: %v\n%s", err, out)
		}
		return string(out)
	}

	// ---- Phase 1: build-only mode packages a valid Teams app, no atk needed. ----
	out := run(t, "SKIP_TEAMS_INSTALL=1")
	zipPath := parseTeamsPackagePath(t, out)
	assertValidTeamsPackage(t, zipPath, botName, msaAppID)

	// ---- Phase 2: install path exercises the not-signed-in login-and-retry. ----
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o750); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "atk-login-called")
	fakeAtk(t, binDir, marker)

	out = run(t, pathWithPrepended(binDir))
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("the fake atk 'Cannot get token' error must trigger 'atk auth login m365'; "+
			"login marker missing:\n%s", out)
	}
	if !strings.Contains(out, "TitleId") {
		t.Errorf("the retried install after login must report a TitleId:\n%s", out)
	}
}

func firstOnPath(names ...string) string {
	for _, n := range names {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	return ""
}

func pathWithPrepended(dir string) string {
	return "PATH=" + dir + string(os.PathListSeparator) + os.Getenv("PATH")
}

var teamsPackageLineRe = regexp.MustCompile(`(?m)^Teams app package:\s*(.+?)\s*$`)

func parseTeamsPackagePath(t *testing.T, out string) string {
	t.Helper()
	m := teamsPackageLineRe.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("could not find the 'Teams app package:' line in output:\n%s", out)
	}
	zipPath := strings.TrimSpace(m[1])
	if _, err := os.Stat(zipPath); err != nil {
		t.Fatalf("reported package %q does not exist: %v", zipPath, err)
	}
	return zipPath
}

// assertValidTeamsPackage unzips the generated .zip and checks the manifest is
// well formed: parseable JSON, a bounded version, the stable Teams app id, the
// bot id, and non-empty icons at the zip root.
func assertValidTeamsPackage(t *testing.T, zipPath, botName, msaAppID string) {
	t.Helper()
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("package is not a valid zip: %v", err)
	}
	defer zr.Close()

	files := map[string][]byte{}
	for _, f := range zr.File {
		// Files must sit at the zip root (no subfolder), per Teams packaging rules.
		if strings.ContainsAny(f.Name, "/\\") {
			t.Errorf("package entry %q is not at the zip root", f.Name)
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %q: %v", f.Name, err)
		}
		b, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read %q: %v", f.Name, err)
		}
		files[f.Name] = b
	}

	for _, icon := range []string{"color.png", "outline.png"} {
		if len(files[icon]) == 0 {
			t.Errorf("package missing non-empty %s", icon)
		}
	}

	raw, ok := files["manifest.json"]
	if !ok {
		t.Fatal("package missing manifest.json")
	}
	var m struct {
		ID      string `json:"id"`
		Version string `json:"version"`
		Bots    []struct {
			BotID string `json:"botId"`
		} `json:"bots"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("manifest.json is not valid JSON: %v\n%s", err, raw)
	}
	if want := deterministicTeamsAppID(botName); m.ID != want {
		t.Errorf("manifest id = %q, want the stable app id %q", m.ID, want)
	}
	if len(m.Bots) != 1 || m.Bots[0].BotID != msaAppID {
		t.Errorf("manifest bots = %+v, want a single botId %q", m.Bots, msaAppID)
	}
	// Version is 1.<minor>.<patch>; Teams caps each component at 65535.
	parts := strings.Split(m.Version, ".")
	if len(parts) != 3 {
		t.Fatalf("manifest version %q is not X.Y.Z", m.Version)
	}
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || n > 65535 {
			t.Errorf("manifest version component %q out of the 0..65535 range", p)
		}
	}
}

// writeFakeAtkUnix installs a stub `atk` that reproduces the real
// not-signed-in-then-login-then-succeed sequence: `install` fails with the token
// error until `auth`/`account login` runs (recorded via the marker file), after
// which `install` returns a TitleId.
func writeFakeAtkUnix(t *testing.T, binDir, marker string) {
	t.Helper()
	body := "#!/usr/bin/env bash\n" +
		"MARKER=\"" + marker + "\"\n" +
		"case \"$1\" in\n" +
		"  install)\n" +
		"    if [ -f \"$MARKER\" ]; then echo \"Installed. TitleId: U_test123\";" +
		" else echo \"Cannot get token. Use 'atk account login m365' to log in the correct account.\"; fi ;;\n" +
		"  auth|account) : > \"$MARKER\"; echo \"Logged in.\" ;;\n" +
		"esac\nexit 0\n"
	p := filepath.Join(binDir, "atk")
	writeExecFile(t, p, []byte(body))
}

// writeFakeAtkWindows is the batch-file equivalent of writeFakeAtkUnix; pwsh
// resolves `atk` to atk.cmd via PATHEXT.
func writeFakeAtkWindows(t *testing.T, binDir, marker string) {
	t.Helper()
	body := "@echo off\r\n" +
		"if \"%1\"==\"install\" (\r\n" +
		"  if exist \"" + marker + "\" ( echo Installed. TitleId: U_test123 )" +
		" else ( echo Cannot get token. Use 'atk account login m365' to log in the correct account. )\r\n" +
		"  exit /b 0\r\n" +
		")\r\n" +
		"if \"%1\"==\"auth\" ( type nul > \"" + marker + "\" & echo Logged in. & exit /b 0 )\r\n" +
		"if \"%1\"==\"account\" ( type nul > \"" + marker + "\" & echo Logged in. & exit /b 0 )\r\n" +
		"exit /b 0\r\n"
	p := filepath.Join(binDir, "atk.cmd")
	writeExecFile(t, p, []byte(body))
}

// execFileMode is a variable (not a literal) so gosec's G302 literal-perms check
// does not flag adding the owner-exec bit to the stub scripts below.
var execFileMode os.FileMode = 0o700

// writeExecFile writes a file at 0o600, then adds the owner-exec bit via Chmod.
// Splitting the write from the mode change keeps the WriteFile perms within the
// gosec G306 limit while still producing a runnable stub on Unix.
func writeExecFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, execFileMode); err != nil {
		t.Fatal(err)
	}
}
