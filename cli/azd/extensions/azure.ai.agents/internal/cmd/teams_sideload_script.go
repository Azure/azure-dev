// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"log"
	"os"
	"runtime"
	"strings"
	"text/template"
	"unicode/utf16"

	"azureaiagent/internal/pkg/paths"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/google/uuid"
)

// Generated pack-and-sideload script file names. They are written next to the
// agent source alongside TEAMS_APP_SETUP.md and complete the last manual mile
// (build the Teams app zip + `atk install --scope Personal`) in one command.
const (
	teamsSideloadScriptPwsh = "pack-and-sideload-teams-app.ps1"
	teamsSideloadScriptBash = "pack-and-sideload-teams-app.sh"

	// teamsSideloadTargets is the number of pack+sideload scripts
	// writeTeamsSideloadScripts emits (one per supported shell). The guide and
	// next-steps output only advertise the fast path when both were written.
	teamsSideloadTargets = 2
)

//go:embed assets/teams_pack_sideload.ps1
var teamsSideloadPwshMarkup string

//go:embed assets/teams_pack_sideload.sh
var teamsSideloadBashMarkup string

// Keeping the scripts as real .ps1/.sh files (assets/) lets editors lint them and
// catches syntax errors a Go string literal would hide.
var (
	teamsSideloadPwshTmpl = template.Must(
		template.New("teamsSideloadPwsh").Parse(teamsSideloadPwshMarkup),
	)
	teamsSideloadBashTmpl = template.Must(
		template.New("teamsSideloadBash").Parse(teamsSideloadBashMarkup),
	)
)

// teamsAppIDNamespace is a fixed namespace used to derive a Teams app id from a
// stable, scope-specific bot key. Using a deterministic UUIDv5 keeps the Teams
// app identity stable across re-runs and re-deploys, so `atk install` updates the
// same app instead of piling up duplicate entries in the user's app list.
var teamsAppIDNamespace = uuid.MustParse("6ba7b811-9dad-11d1-80b4-00c04fd430c8")

// deterministicTeamsAppID derives a stable Teams app id (distinct from the bot
// id) from a stable, scope-specific key. The key MUST NOT be the version-scoped
// msaAppId/instance client id: that changes when a new agent version deploys (or
// the managed identity is recreated), which would mint a new Teams app id and
// pile up duplicate Teams apps instead of updating the installed one. The bot
// name (service name + subscription/RG salt) is stable, so callers pass that;
// the current msaAppId is used only for the manifest's bots[].botId.
func deterministicTeamsAppID(stableKey string) string {
	return uuid.NewSHA1(teamsAppIDNamespace, []byte("foundry-teams-app:"+stableKey)).String()
}

// teamsColorIconB64 is a 192x192 solid-color PNG and teamsOutlineIconB64 is a
// 32x32 transparent outline PNG, embedded as base64 so the generated scripts can
// write valid Teams icons with no image tooling on any OS. Replace with your own
// branding by editing the generated script or the produced color.png/outline.png.
// cspell:disable
const (
	// teamsColorIconB64 and teamsOutlineIconB64 are split into short concatenated
	// literals only to satisfy the lll line-length linter (each source line stays
	// well under 125 chars); concatenation yields the exact original base64 for
	// each PNG.
	teamsColorIconB64 = "" +
		"iVBORw0KGgoAAAANSUhEUgAAAMAAAADACAYAAABS3GwHAAABiUlEQVR42u3TMQ0AAAjAMHxyIBdZYIKPHjWwZJHVA1" +
		"+FCBgADAAGAAOAAcAAYAAwABgADAAGAAOAAcAAYAAwABgADAAGAAOAAcAAYAAwABgADAAGAAOAAcAAYAAwABgADAAG" +
		"AAOAAcAAYAAwABgADAAGAAOAAcAAYAAwABgADAAGwAAiYAAwABgADAAGAAOAAcAAYAAwABgADAAGAAOAAcAAYAAwAB" +
		"gADAAGAAOAAcAAYAAwABgADAAGAAOAAcAAYAAwABgADAAGAAOAAcAAYAAwABgADAAGAAOAAcAAYAAwABgAA4ABwABg" +
		"ADAAGAAMAAYAA4ABwABgADAAGAAMAAYAA4ABwABgADAAGAAMAAYAA4ABwABgADAAGAAMAAYAA4ABwABgADAAGAAMAA" +
		"YAA4ABwABgADAAGAAMAAYAA4ABwAAYQAQMAAYAA4ABwABgADAAGAAMAAYAA4ABwABgADAAGAAMAAYAA4ABwABgADAA" +
		"GAAMAAYAA4ABwABgADAAGAAMAAYAA8ClBeSCFRle66JBAAAAAElFTkSuQmCC"
	teamsOutlineIconB64 = "" +
		"iVBORw0KGgoAAAANSUhEUgAAACAAAAAgCAYAAABzenr0AAAAaklEQVR42u1XQQ4AIAjq/5+2D7RlJoKbnBPJpdJag2" +
		"6wC2iJYULsE5DkWeefk1fEHgmyKhgKzHxDpbcP8yFayc2J6mU3L3KijYAR0E8ApQ3pg0hiFNOXkcQ6phsSCUsmYUol" +
		"bLnMx2SAwgZ903yusz4vOQAAAABJRU5ErkJggg=="
)

// cspell:enable

// teamsSideloadData is the template model shared by the pwsh and bash scripts.
type teamsSideloadData struct {
	AgentName     string
	MsaAppID      string
	TeamsAppId    string
	ColorPngB64   string
	OutlinePngB64 string
}

// teamsSideloadScriptContent renders one pack-and-sideload script from the given
// template with the azd-controlled ids baked in.
func teamsSideloadScriptContent(
	tmpl *template.Template, agentName, botName, msaAppID string,
) string {
	var buf bytes.Buffer
	// Inputs are azd-controlled resource names/ids and the templates are
	// compile-time embedded, so execution cannot realistically fail.
	_ = tmpl.Execute(&buf, teamsSideloadData{
		AgentName:     agentName,
		MsaAppID:      msaAppID,
		TeamsAppId:    deterministicTeamsAppID(botName),
		ColorPngB64:   teamsColorIconB64,
		OutlinePngB64: teamsOutlineIconB64,
	})
	return buf.String()
}

// writeTeamsSideloadScripts writes runnable pwsh + bash pack-and-sideload scripts
// next to the agent source so the user can finish the last manual mile (package
// the Teams app + sideload it for themselves) in one command. It returns the
// paths written. Best-effort: any script that fails to write is skipped and
// logged, and this never blocks or fails the deploy. All ids are baked in from
// the deploy, so the generated scripts make no Azure calls. Like the setup guide,
// each script is (re)written on every deploy with the current ids -- the Teams app
// id stays stable (see deterministicTeamsAppID), so re-running just updates the
// same installed app.
func writeTeamsSideloadScripts(
	proj *azdext.ProjectConfig, svc *azdext.ServiceConfig, agentName, botName, msaAppID string,
) []string {
	scripts := []struct {
		file string
		tmpl *template.Template
		mode os.FileMode
	}{
		{teamsSideloadScriptPwsh, teamsSideloadPwshTmpl, 0o600},
		{teamsSideloadScriptBash, teamsSideloadBashTmpl, 0o700},
	}

	var written []string
	for _, s := range scripts {
		scriptPath, err := paths.JoinAllowRoot(proj.GetPath(), svc.GetRelativePath(), s.file)
		if err != nil {
			log.Printf("postdeploy: skipping Teams sideload script %q: %v", s.file, err)
			continue
		}
		content := teamsSideloadScriptContent(s.tmpl, agentName, botName, msaAppID)
		if err := os.WriteFile(scriptPath, []byte(content), s.mode); err != nil {
			log.Printf("postdeploy: failed to write Teams sideload script %q: %v", scriptPath, err)
			continue
		}
		written = append(written, scriptPath)
	}
	return written
}

// preferredSideloadScript returns the generated script that matches the current
// OS (the .ps1 on Windows, the .sh elsewhere), or "" if no matching script was
// written. It deliberately does not fall back to the other platform's script:
// running a .ps1 on Linux/macOS or a .sh on Windows would emit the wrong shell
// syntax, so the caller shows the guide/manual fallback instead.
func preferredSideloadScript(scriptPaths []string) string {
	wantPwsh := runtime.GOOS == "windows"
	for _, p := range scriptPaths {
		isPwsh := strings.HasSuffix(p, ".ps1")
		if isPwsh == wantPwsh {
			return p
		}
	}
	return ""
}

// sideloadRunCommand returns a runnable, expansion-safe invocation of the
// generated script for a user-facing hint. azd prints this hint to whatever shell
// launched it, which on Windows may be cmd.exe OR PowerShell -- and the two
// disagree on quoting (cmd.exe does not treat single quotes as quoting and still
// expands %VAR%). So the .ps1 branch encodes a full PowerShell command
// (`& '<path>'`, with any embedded single quote doubled per the PowerShell
// literal-string rule) as UTF-16LE base64 and passes it via -EncodedCommand: the
// path lives inside the base64 payload, so no parent shell can re-split or expand
// it, and the command runs identically from cmd.exe, powershell.exe, and pwsh. It
// uses powershell.exe (present on every Windows install, unlike PowerShell 7) with
// -ExecutionPolicy Bypass so a default Restricted client still runs the child
// script (the bypass is process-scoped and does not change machine/user policy).
// The .sh branch is only shown on POSIX hosts, whose shells all honor single
// quotes, so it uses a POSIX single-quoted literal (close the quote, add an
// escaped ', reopen). See cli/azd/AGENTS.md ("Shell-safe output").
func sideloadRunCommand(scriptPath string) string {
	if strings.HasSuffix(scriptPath, ".ps1") {
		inner := "& '" + strings.ReplaceAll(scriptPath, "'", "''") + "'"
		return "powershell -NoProfile -ExecutionPolicy Bypass -EncodedCommand " + encodePowerShellCommand(inner)
	}
	// POSIX single-quoted literal: close the quote, add an escaped ', reopen.
	return "bash '" + strings.ReplaceAll(scriptPath, "'", `'\''`) + "'"
}

// encodePowerShellCommand returns the base64 of the UTF-16LE bytes of cmd, the
// wire format powershell.exe / pwsh expect for -EncodedCommand. Because the
// payload is plain base64 ([A-Za-z0-9+/=]), it survives any parent shell verbatim.
func encodePowerShellCommand(cmd string) string {
	units := utf16.Encode([]rune(cmd))
	buf := make([]byte, len(units)*2)
	for i, u := range units {
		// Split each UTF-16 code unit into little-endian bytes; the masks make the
		// truncation explicit and intentional.
		buf[i*2] = byte(u & 0xff)        //nolint:gosec // intentional low-byte truncation
		buf[i*2+1] = byte(u >> 8 & 0xff) //nolint:gosec // intentional high-byte truncation
	}
	return base64.StdEncoding.EncodeToString(buf)
}
