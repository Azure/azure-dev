// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	_ "embed"
	"log"
	"os"
	"runtime"
	"text/template"

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

// teamsAppIDNamespace is a fixed namespace used to derive a Teams app id from the
// bot's msaAppId. Using a deterministic UUIDv5 keeps the Teams app identity
// stable across re-runs and re-deploys, so `atk install` updates the same app
// instead of piling up duplicate entries in the user's app list.
var teamsAppIDNamespace = uuid.MustParse("6ba7b811-9dad-11d1-80b4-00c04fd430c8")

// deterministicTeamsAppID derives a stable Teams app id (distinct from the bot
// id) for the given msaAppId.
func deterministicTeamsAppID(msaAppID string) string {
	return uuid.NewSHA1(teamsAppIDNamespace, []byte("foundry-teams-app:"+msaAppID)).String()
}

// teamsColorIconB64 is a 192x192 solid-color PNG and teamsOutlineIconB64 is a
// 32x32 transparent outline PNG, embedded as base64 so the generated scripts can
// write valid Teams icons with no image tooling on any OS. Replace with your own
// branding by editing the generated script or the produced color.png/outline.png.
// cspell:disable
const (
	// teamsColorIconB64 and teamsOutlineIconB64 are split into fixed-width chunks
	// only to satisfy the lll line-length linter; concatenation yields the exact
	// original base64 for each PNG.
	teamsColorIconB64 = "iVBORw0KGgoAAAANSUhEUgAAAMAAAADACAYAAABS3GwHAAABiUlEQVR42u3TMQ0AAAjAMHxyIBdZYIKPHjWwZJHVA1+FCBgADAAGAAOAAcAAYAAwABgADAAGAAOAAcAAYAAwABgADAAGAAOAAcAAYA" +
		"AwABgADAAGAAOAAcAAYAAwABgADAAGAAOAAcAAYAAwABgADAAGAAOAAcAAYAAwABgADAAGwAAiYAAwABgADAAGAAOAAcAAYAAwABgADAAGAAOAAcAAYAAwABgADAAGAAOAAcAAYAAwABgADAAGAAOA" +
		"AcAAYAAwABgADAAGAAOAAcAAYAAwABgADAAGAAOAAcAAYAAwABgAA4ABwABgADAAGAAMAAYAA4ABwABgADAAGAAMAAYAA4ABwABgADAAGAAMAAYAA4ABwABgADAAGAAMAAYAA4ABwABgADAAGAAMAA" +
		"YAA4ABwABgADAAGAAMAAYAA4ABwAAYQAQMAAYAA4ABwABgADAAGAAMAAYAA4ABwABgADAAGAAMAAYAA4ABwABgADAAGAAMAAYAA4ABwABgADAAGAAMAAYAA8ClBeSCFRle66JBAAAAAElFTkSuQmCC"
	teamsOutlineIconB64 = "iVBORw0KGgoAAAANSUhEUgAAACAAAAAgCAYAAABzenr0AAAAaklEQVR42u1XQQ4AIAjq/5+2D7RlJoKbnBPJpdJag26wC2iJYULsE5DkWeefk1fEHgmyKhgKzHxDpbcP8yFayc2J6mU3L3KijYAR0E" +
		"8ApQ3pg0hiFNOXkcQ6phsSCUsmYUolbLnMx2SAwgZ903yusz4vOQAAAABJRU5ErkJggg=="
)

// cspell:enable

// teamsSideloadData is the template model shared by the pwsh and bash scripts.
type teamsSideloadData struct {
	AgentName     string
	BotName       string
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
		BotName:       botName,
		MsaAppID:      msaAppID,
		TeamsAppId:    deterministicTeamsAppID(msaAppID),
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
// the deploy, so the generated scripts make no Azure calls.
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

// preferredSideloadScript returns the generated script best suited to the current
// OS (the .ps1 on Windows, the .sh elsewhere), or "" if none was written.
func preferredSideloadScript(scriptPaths []string) string {
	wantPwsh := runtime.GOOS == "windows"
	for _, p := range scriptPaths {
		isPwsh := len(p) >= 4 && p[len(p)-4:] == ".ps1"
		if isPwsh == wantPwsh {
			return p
		}
	}
	if len(scriptPaths) > 0 {
		return scriptPaths[0]
	}
	return ""
}
