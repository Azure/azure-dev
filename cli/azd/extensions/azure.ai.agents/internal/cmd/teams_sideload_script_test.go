// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"regexp"
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
	// The Teams app id is derived from the STABLE bot name, not the version-scoped
	// msaAppId, so a redeploy updates the same app instead of duplicating it.
	teamsAppID := deterministicTeamsAppID(botName)

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

// TestTeamsSideloadLoginDetection guards the "not signed in" auto-login path:
// both generated scripts must recognize the actual error text the current ATK
// CLI prints for an unauthenticated user, otherwise a fresh user silently falls
// through to the install-failed guidance instead of being logged in and retried.
func TestTeamsSideloadLoginDetection(t *testing.T) {
	const (
		agentName = "echo-agent"
		botName   = "echo-agent-bot-uai"
		msaAppID  = "11111111-2222-3333-4444-555555555555"
	)

	// The literal message emitted by `atk install` when no account is signed in.
	const atkUnauthenticated = "Cannot get token. Use 'atk account login m365' to log in the correct account."
	// A successful install line must NOT be mistaken for the login-required case.
	const atkInstalled = "Successfully installed the app. TitleId: U_1234567890"

	// The alternation both scripts embed to detect the login-required state. The
	// bash (ERE) and pwsh (.NET) patterns use the same alternatives; RE2 accepts
	// this subset, so we can assert the intended matching behavior here.
	loginRequired := regexp.MustCompile(`(?i)not (logged|signed) in|auth.*required|` +
		`please\s?login|login\s?first|no account|cannot get token|log in the correct account`)

	if !loginRequired.MatchString(atkUnauthenticated) {
		t.Fatalf("login-required pattern does not match the ATK unauthenticated error: %q", atkUnauthenticated)
	}
	if loginRequired.MatchString(atkInstalled) {
		t.Errorf("login-required pattern wrongly matched a successful install line: %q", atkInstalled)
	}

	// Both scripts must carry the phrases that match the real ATK error so the
	// embedded regexes stay in sync with the behavior asserted above.
	for name, content := range map[string]string{
		"pwsh": teamsSideloadScriptContent(teamsSideloadPwshTmpl, agentName, botName, msaAppID),
		"bash": teamsSideloadScriptContent(teamsSideloadBashTmpl, agentName, botName, msaAppID),
	} {
		// Normalize the two whitespace forms the scripts use (bash ERE uses a
		// literal space, pwsh .NET uses \s+) so the phrase check works for both.
		norm := strings.ToLower(content)
		norm = strings.ReplaceAll(norm, `\s+`, " ")
		norm = strings.ReplaceAll(norm, `\s?`, " ")
		norm = strings.Join(strings.Fields(norm), " ")
		if !strings.Contains(norm, "cannot get token") ||
			!strings.Contains(norm, "log in the correct account") {
			t.Errorf("[%s] login-detection regex does not cover the ATK 'Cannot get token' error", name)
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
	if len(paths) != teamsSideloadTargets {
		t.Fatalf("expected %d scripts written, got %d: %v", teamsSideloadTargets, len(paths), paths)
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

func TestWriteTeamsSideloadScriptsPreservesUserFiles(t *testing.T) {
	root := t.TempDir()
	proj := &azdext.ProjectConfig{Path: root}
	svc := &azdext.ServiceConfig{Name: "echo-agent", RelativePath: "src"}
	srcDir := filepath.Join(root, "src")
	if err := os.MkdirAll(srcDir, 0o750); err != nil {
		t.Fatal(err)
	}

	// A pre-existing user-owned file that happens to share the bash script name
	// (no azd-generated marker) must be left untouched and not reported as written.
	userBash := filepath.Join(srcDir, teamsSideloadScriptBash)
	userContent := "#!/usr/bin/env bash\necho \"my own script\"\n"
	if err := os.WriteFile(userBash, []byte(userContent), 0o600); err != nil {
		t.Fatal(err)
	}

	paths := writeTeamsSideloadScripts(proj, svc, "echo-agent", "echo-agent-bot-uai", "app-id")

	// The bash name collided with a user file, so only the pwsh script is written:
	// a partial write must report fewer than teamsSideloadTargets so the caller
	// does not advertise the fast path.
	if len(paths) != 1 {
		t.Fatalf("expected only the non-colliding script to be written, got %d: %v", len(paths), paths)
	}
	if len(paths) == teamsSideloadTargets {
		t.Errorf("a partial write must not equal teamsSideloadTargets (%d)", teamsSideloadTargets)
	}

	for _, p := range paths {
		if p == userBash {
			t.Errorf("clobbered user-owned file %q was reported as written", p)
		}
	}
	got, err := os.ReadFile(userBash)
	if err != nil {
		t.Fatalf("user file was removed: %v", err)
	}
	if string(got) != userContent {
		t.Errorf("user-owned file was overwritten:\n got: %q\nwant: %q", string(got), userContent)
	}

	// A previously azd-generated script (carrying this bot's marker) is refreshed in place.
	genContent := "# " + teamsGeneratedMarkerFor("echo-agent-bot-uai") + "\n# stale\n"
	if err := os.WriteFile(userBash, []byte(genContent), 0o600); err != nil {
		t.Fatal(err)
	}
	paths = writeTeamsSideloadScripts(proj, svc, "echo-agent", "echo-agent-bot-uai", "app-id")
	refreshed := false
	for _, p := range paths {
		if p == userBash {
			refreshed = true
		}
	}
	if !refreshed {
		t.Errorf("expected the azd-generated script %q to be refreshed", userBash)
	}
	got, err = os.ReadFile(userBash)
	if err != nil {
		t.Fatalf("generated script missing after refresh: %v", err)
	}
	if !strings.Contains(string(got), "app-id") || strings.Contains(string(got), "stale") {
		t.Errorf("azd-generated script was not refreshed in place: %q", string(got))
	}
}

// TestWriteTeamsSideloadScriptsRejectsOtherAgent guards the shared-source-dir
// case: if a second activity service resolves to the same project:/src, its
// postdeploy must not overwrite scripts a different agent already generated
// there (only the last writer's bot id would install). The other agent's file
// is left byte-for-byte intact and is not reported as written.
func TestWriteTeamsSideloadScriptsRejectsOtherAgent(t *testing.T) {
	root := t.TempDir()
	proj := &azdext.ProjectConfig{Path: root}
	svc := &azdext.ServiceConfig{Name: "echo-agent", RelativePath: "src"}
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o750); err != nil {
		t.Fatal(err)
	}

	// Simulate a script another agent already generated in this shared directory.
	otherBash := filepath.Join(root, "src", teamsSideloadScriptBash)
	otherContent := "#!/usr/bin/env bash\n# " + teamsGeneratedMarkerFor("other-bot-id") + "\n# other agent\n"
	if err := os.WriteFile(otherBash, []byte(otherContent), 0o600); err != nil {
		t.Fatal(err)
	}

	paths := writeTeamsSideloadScripts(proj, svc, "echo-agent", "echo-agent-bot-uai", "this-bot-id")
	for _, p := range paths {
		if p == otherBash {
			t.Errorf("overwrote another agent's script %q", otherBash)
		}
	}
	// A partial write (only the pwsh script) must not claim all targets succeeded.
	if len(paths) == teamsSideloadTargets {
		t.Errorf("a partial write must not equal teamsSideloadTargets (%d)", teamsSideloadTargets)
	}
	got, err := os.ReadFile(otherBash)
	if err != nil {
		t.Fatalf("other agent's file was removed: %v", err)
	}
	if string(got) != otherContent {
		t.Errorf("other agent's file was overwritten:\n got: %q\nwant: %q", string(got), otherContent)
	}
}

// TestWriteTeamsSideloadScriptsStableAcrossVersionChange guards the redeploy
// case: a new agent version has a fresh, version-scoped instance client id
// (msaAppID), but the ownership marker and Teams app id are keyed on the STABLE
// bot name. So azd must recognize its own previously generated scripts and
// refresh them in place (with the new bot id), keeping the Teams app id constant
// instead of duplicating the app or refusing to update.
func TestWriteTeamsSideloadScriptsStableAcrossVersionChange(t *testing.T) {
	root := t.TempDir()
	proj := &azdext.ProjectConfig{Path: root}
	svc := &azdext.ServiceConfig{Name: "echo-agent", RelativePath: "src"}
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o750); err != nil {
		t.Fatal(err)
	}
	const botName = "echo-agent-bot-uai"

	first := writeTeamsSideloadScripts(proj, svc, "echo-agent", botName, "client-id-v1")
	if len(first) != teamsSideloadTargets {
		t.Fatalf("initial deploy must write all %d scripts, got %d", teamsSideloadTargets, len(first))
	}

	// Redeploy: same bot name, but a brand-new version-scoped client id.
	second := writeTeamsSideloadScripts(proj, svc, "echo-agent", botName, "client-id-v2")
	if len(second) != teamsSideloadTargets {
		t.Fatalf("redeploy with a new version client id must refresh all %d scripts, got %d: %v",
			teamsSideloadTargets, len(second), second)
	}

	data, err := os.ReadFile(filepath.Join(root, "src", teamsSideloadScriptBash))
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	// The Teams app id stays stable (keyed on the bot name), so re-runs update
	// the same installed app.
	if !strings.Contains(body, deterministicTeamsAppID(botName)) {
		t.Errorf("Teams app id must stay stable across a version change")
	}
	// The refreshed script carries the NEW bot id and drops the old one.
	if !strings.Contains(body, "client-id-v2") {
		t.Errorf("refreshed script must carry the new bot id")
	}
	if strings.Contains(body, "client-id-v1") {
		t.Errorf("refreshed script must drop the previous bot id")
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
	// The emitted command must keep a path with spaces as one argument: the .ps1
	// is run via `powershell -File "..."` (double quotes group in cmd.exe,
	// powershell, and bash; backslashes stay intact so the Windows path
	// resolves), the .sh via single-quoted bash. Struct field names avoid
	// substrings (e.g. "pw") that gosec's G101 rule treats as credential
	// indicators.
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"ps1_simple", `C:\a b\x.ps1`, `powershell -NoProfile -File "C:\a b\x.ps1"`},
		{"ps1_backslashes", `C:\Users\a\svc\x.ps1`, `powershell -NoProfile -File "C:\Users\a\svc\x.ps1"`},
		{"ps1_single_quote", `C:\a'b\x.ps1`, `powershell -NoProfile -File "C:\a'b\x.ps1"`},
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
