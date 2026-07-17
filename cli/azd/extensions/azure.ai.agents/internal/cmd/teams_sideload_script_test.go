// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"text/template"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/google/uuid"
)

func TestDeterministicTeamsAppID(t *testing.T) {
	const msaAppID = "11111111-2222-3333-4444-555555555555"

	got := deterministicTeamsAppID(msaAppID)
	if _, err := uuid.Parse(got); err != nil {
		t.Fatalf("teams app id %q is not a valid UUID: %v", got, err)
	}
	// Stable across calls so re-runs/re-deploys update the same Teams app.
	if again := deterministicTeamsAppID(msaAppID); again != got {
		t.Errorf("teams app id not stable: %q vs %q", got, again)
	}
	// Distinct from the bot id it is derived from.
	if got == msaAppID {
		t.Errorf("teams app id must differ from the bot id")
	}
	// Different bots get different ids.
	if other := deterministicTeamsAppID("99999999-8888-7777-6666-555555555555"); other == got {
		t.Errorf("distinct bot ids must yield distinct teams app ids")
	}
}

func TestTeamsSideloadScriptContent(t *testing.T) {
	const (
		agentName = "echo-agent"
		botName   = "echo-agent-bot-uai"
		msaAppID  = "11111111-2222-3333-4444-555555555555"
	)
	teamsAppID := deterministicTeamsAppID(msaAppID)

	for name, tmpl := range map[string]struct {
		content string
	}{
		"pwsh": {teamsSideloadScriptContent(teamsSideloadPwshTmpl, agentName, botName, msaAppID)},
		"bash": {teamsSideloadScriptContent(teamsSideloadBashTmpl, agentName, botName, msaAppID)},
	} {
		content := tmpl.content

		// No unresolved template placeholders may remain.
		if strings.Contains(content, "{{") || strings.Contains(content, "}}") {
			t.Errorf("[%s] script has unresolved template placeholders:\n%s", name, content)
		}
		if !strings.Contains(content, msaAppID) {
			t.Errorf("[%s] script missing the bot id", name)
		}
		if !strings.Contains(content, "28:"+"$BotId") && !strings.Contains(content, "28:"+"$BOT_ID") {
			t.Errorf("[%s] script missing the Teams 1:1 chat deep link", name)
		}
		// The stable Teams app id (distinct from the bot id) must be embedded.
		if !strings.Contains(content, teamsAppID) {
			t.Errorf("[%s] script missing the deterministic Teams app id", name)
		}
		// The per-user, no-admin install command must be present.
		if !strings.Contains(content, "--scope Personal") {
			t.Errorf("[%s] script missing 'atk install --scope Personal'", name)
		}
		// Icons must be embedded so the script needs no image tooling.
		if !strings.Contains(content, teamsColorIconB64) || !strings.Contains(content, teamsOutlineIconB64) {
			t.Errorf("[%s] script missing embedded icon data", name)
		}
		// The opt-out must be honored.
		if !strings.Contains(content, "SKIP_TEAMS_INSTALL") {
			t.Errorf("[%s] script missing the SKIP_TEAMS_INSTALL opt-out", name)
		}
	}
}

func TestWriteTeamsSideloadScripts(t *testing.T) {
	root := t.TempDir()
	proj := &azdext.ProjectConfig{Path: root}
	svc := &azdext.ServiceConfig{Name: "echo-agent", RelativePath: "src"}
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o750); err != nil {
		t.Fatal(err)
	}

	paths := writeTeamsSideloadScripts(proj, svc, "echo-agent", "echo-agent-bot-uai", "app-id")
	if len(paths) != 2 {
		t.Fatalf("expected 2 scripts written, got %d: %v", len(paths), paths)
	}

	wantFiles := map[string]bool{
		filepath.Join(root, "src", teamsSideloadScriptPwsh): false,
		filepath.Join(root, "src", teamsSideloadScriptBash): false,
	}
	for _, p := range paths {
		if _, ok := wantFiles[p]; !ok {
			t.Errorf("unexpected script path %q", p)
		}
		wantFiles[p] = true
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("script not written: %v", err)
		}
		if !strings.Contains(string(data), "app-id") {
			t.Errorf("written script %q missing the bot id", p)
		}
	}
	for p, seen := range wantFiles {
		if !seen {
			t.Errorf("expected script %q was not written", p)
		}
	}
}

func TestPreferredSideloadScript(t *testing.T) {
	pwsh := filepath.Join("x", teamsSideloadScriptPwsh)
	bash := filepath.Join("x", teamsSideloadScriptBash)

	if got := preferredSideloadScript(nil); got != "" {
		t.Errorf("empty input should yield empty result, got %q", got)
	}
	// Whatever the OS, the result must be one of the two written scripts.
	got := preferredSideloadScript([]string{pwsh, bash})
	if got != pwsh && got != bash {
		t.Errorf("preferred script %q is neither candidate", got)
	}
	// The current-OS script must be chosen so the emitted command matches the
	// user's shell.
	wantOSScript := bash
	otherOSScript := pwsh
	if runtime.GOOS == "windows" {
		wantOSScript, otherOSScript = pwsh, bash
	}
	if got := preferredSideloadScript([]string{wantOSScript}); got != wantOSScript {
		t.Errorf("expected the current-OS script %q, got %q", wantOSScript, got)
	}
	// If only the wrong-OS script was written, return "" (no cross-shell hint)
	// so the guide/manual fallback is shown instead.
	if got := preferredSideloadScript([]string{otherOSScript}); got != "" {
		t.Errorf("wrong-OS-only input should yield empty result, got %q", got)
	}
}

// TestTeamsSideloadScriptBuildOnly asserts the SKIP_TEAMS_INSTALL opt-out is a
// build-only mode: the package (zip) is produced first and only the atk install
// is skipped. It verifies this via source ordering rather than executing the
// scripts (which would need a real atk/npm/pwsh|bash on both CI OSes).
func TestTeamsSideloadScriptBuildOnly(t *testing.T) {
	const (
		agentName = "echo-agent"
		botName   = "echo-agent-bot-uai"
		msaAppID  = "11111111-2222-3333-4444-555555555555"
	)
	for name, tmpl := range map[string]*template.Template{
		"pwsh": teamsSideloadPwshTmpl,
		"bash": teamsSideloadBashTmpl,
	} {
		content := teamsSideloadScriptContent(tmpl, agentName, botName, msaAppID)

		idxPkg := strings.Index(content, "Teams app package:")
		idxSkip := strings.Index(content, "package built; skipping")
		idxInstall := strings.Index(content, "atk install --file-path")
		if idxPkg < 0 || idxSkip < 0 || idxInstall < 0 {
			t.Fatalf("[%s] missing package/skip/install markers: pkg=%d skip=%d install=%d",
				name, idxPkg, idxSkip, idxInstall)
		}
		// Build-only mode must run AFTER the package is written and BEFORE install.
		if !(idxPkg < idxSkip && idxSkip < idxInstall) {
			t.Errorf("[%s] SKIP guard is misordered: pkg=%d skip=%d install=%d (want pkg<skip<install)",
				name, idxPkg, idxSkip, idxInstall)
		}
		// The manual-sideload fallback must remain reachable.
		if !strings.Contains(content, "Upload a custom app") {
			t.Errorf("[%s] script missing the manual-sideload fallback", name)
		}
	}

	// The bash TitleId extraction must tolerate no match (grep exits 1 under
	// `set -euo pipefail`) so the empty-id fallback branch stays reachable.
	bash := teamsSideloadScriptContent(teamsSideloadBashTmpl, agentName, botName, msaAppID)
	if !strings.Contains(bash, `//I' || true)`) {
		t.Errorf("bash TitleId extraction must end with '|| true' to survive no match")
	}
}

// TestTeamsSideloadScriptTruncatesManifestFields asserts the Teams manifest
// short fields are bounded (name.short<=30, description.short<=80 per v1.19),
// since valid agent names may be longer than those limits.
func TestTeamsSideloadScriptTruncatesManifestFields(t *testing.T) {
	longName := strings.Repeat("a", 63)
	pwsh := teamsSideloadScriptContent(teamsSideloadPwshTmpl, longName, "bot", "app-id")
	bash := teamsSideloadScriptContent(teamsSideloadBashTmpl, longName, "bot", "app-id")

	if !strings.Contains(pwsh, "Substring(0, 30)") || !strings.Contains(pwsh, "$shortName") {
		t.Errorf("pwsh script does not bound name.short to 30 chars")
	}
	if !strings.Contains(pwsh, "$shortDesc") || !strings.Contains(pwsh, "80") {
		t.Errorf("pwsh script does not bound description.short to 80 chars")
	}
	if !strings.Contains(bash, `printf '%.30s'`) || !strings.Contains(bash, "$SHORT_NAME") {
		t.Errorf("bash script does not bound name.short to 30 chars")
	}
	if !strings.Contains(bash, `printf '%.80s'`) || !strings.Contains(bash, "$SHORT_DESC") {
		t.Errorf("bash script does not bound description.short to 80 chars")
	}
}

func TestSideloadRunCommand(t *testing.T) {
	// The emitted command must single-quote the path for the target shell so a
	// path with spaces or metacharacters ($, backtick, quote) is neither
	// expanded nor able to break out of the argument; a quoted .ps1 also needs
	// the pwsh call operator. Variable names avoid substrings (e.g. "pw") that
	// gosec's G101 rule treats as credential indicators.
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"pwsh_simple", `a b\x.ps1`, `& 'a b\x.ps1'`},
		{"pwsh_metachars", "a $b`c\\d.ps1", "& 'a $b`c\\d.ps1'"},
		{"pwsh_quote", `a'b\x.ps1`, `& 'a''b\x.ps1'`},
		{"bash_simple", `a b/x.sh`, `bash 'a b/x.sh'`},
		{"bash_metachars", "a $b`c/d.sh", "bash 'a $b`c/d.sh'"},
		{"bash_quote", `a'b/x.sh`, `bash 'a'\''b/x.sh'`},
	}
	for _, tc := range cases {
		if got := sideloadRunCommand(tc.in); got != tc.want {
			t.Errorf("%s: sideloadRunCommand(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}
