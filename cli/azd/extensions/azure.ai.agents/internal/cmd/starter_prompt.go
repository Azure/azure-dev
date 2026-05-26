// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// starter_prompt.go renders the embedded starter-prompt template for the
// agent-init pre-flow and exposes a small clipboard helper that
// gracefully degrades on headless / CI / SSH environments where no
// clipboard is reachable.
//
// The template lives in starter_prompts/agent_init.md (embedded). Per-
// extension starter prompts is the per-extension ownership pattern --
// other ai.* extensions adopting this flow drop their own templates
// under their own starter_prompts/ dir without contaminating
// azure.ai.docs (which owns SKILL.md content, not prompts).

package cmd

import (
	"bytes"
	"embed"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"text/template"

	"github.com/atotto/clipboard"
	"github.com/fatih/color"
)

// starterPromptFS embeds every starter-prompt template shipped by this
// extension. Add a template by dropping a new .md file under
// starter_prompts/.
//
//go:embed starter_prompts/*.md
var starterPromptFS embed.FS

// StarterPromptVars is the data shape passed to the agent-init template.
// All fields are optional; the template renders sensible output when any
// field is empty.
type StarterPromptVars struct {
	// ProjectPath is the absolute path to the project root (typically
	// the user's current working directory).
	ProjectPath string
	// SkillPath is the relative path where the AZD AI skill was installed
	// (e.g. ".claude/skills/azd-ai-skill"). Kept for struct compatibility
	// but not referenced by the agent_init.md template.
	SkillPath string
	// FoundryProjectId is the full ARM resource ID of the Foundry project
	// the user selected in the pre-flow (Q4). Empty when the user chose
	// "Create a new Foundry project" or skipped.
	FoundryProjectId string
	// ModelDeployment is the name of the model deployment the user
	// selected in the pre-flow (Q5). Empty when the user chose "Create
	// a new model deployment", "Skip", or no project was selected.
	ModelDeployment string
}

// renderStarterPrompt returns the rendered agent-init prompt body with
// no trailing whitespace. Returns a non-nil error only on template-
// parse failures (impossible in normal builds since the template is
// embedded at build time and validated by the test below).
func renderStarterPrompt(vars StarterPromptVars) (string, error) {
	return renderStarterPromptNamed("agent_init", vars)
}

// renderStarterPromptNamed is the testable seam: renders any embedded
// starter-prompt template by stem name (e.g. "agent_init" ->
// starter_prompts/agent_init.md).
func renderStarterPromptNamed(stem string, vars StarterPromptVars) (string, error) {
	path := "starter_prompts/" + stem + ".md"
	body, err := starterPromptFS.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read embedded starter prompt %s: %w", path, err)
	}

	tmpl, err := template.New(stem).Parse(string(body))
	if err != nil {
		return "", fmt.Errorf("parse starter prompt template %s: %w", path, err)
	}

	var out bytes.Buffer
	if err := tmpl.Execute(&out, vars); err != nil {
		return "", fmt.Errorf("execute starter prompt template %s: %w", path, err)
	}
	return strings.TrimRight(out.String(), " \t\n"), nil
}

// ClipboardOutcome describes the result of attempting to copy text to
// the system clipboard. Distinguishes between "we never tried because
// the environment looks headless" and "we tried but the OS clipboard
// returned an error" so callers can show different fallback hints.
type ClipboardOutcome int

const (
	// ClipboardCopied means the clipboard now holds the requested text.
	ClipboardCopied ClipboardOutcome = iota
	// ClipboardSkipped means we never attempted -- the env looks
	// headless (CI=true, TERM=dumb, no DISPLAY on Linux, SSH session,
	// etc.) so a clipboard prompt would just confuse the user.
	ClipboardSkipped
	// ClipboardFailed means we tried but the clipboard library returned
	// an error (xclip/wl-copy missing, etc.). Caller should print the
	// "copy manually" hint.
	ClipboardFailed
)

// clipboardCopier abstracts the actual library call so tests can inject
// a fake. Default production wiring uses atotto/clipboard.
type clipboardCopier func(text string) error

// clipboardEnv abstracts the env lookup so the env-aware skip logic is
// testable without t.Setenv polluting the test process. The map-key
// values are returned by Lookup; missing keys return ("", false).
type clipboardEnv interface {
	Lookup(key string) (string, bool)
}

// osClipboardEnv implements clipboardEnv against the live process env.
type osClipboardEnv struct{}

func (osClipboardEnv) Lookup(key string) (string, bool) {
	return os.LookupEnv(key)
}

// CopyToClipboard copies text using the default production wiring
// (atotto/clipboard + os.LookupEnv). Returns the outcome so callers can
// render the appropriate user-facing message.
func CopyToClipboard(text string) ClipboardOutcome {
	return copyToClipboardWith(text, clipboard.WriteAll, osClipboardEnv{})
}

// copyToClipboardWith is the testable seam.
func copyToClipboardWith(text string, write clipboardCopier, env clipboardEnv) ClipboardOutcome {
	if isHeadlessEnv(env) {
		return ClipboardSkipped
	}
	if err := write(text); err != nil {
		return ClipboardFailed
	}
	return ClipboardCopied
}

// isHeadlessEnv reports whether the current process looks like it has
// no usable clipboard. Heuristics:
//
//   - Any CI env var present (matches azdext.isCIEnv; covers GitHub
//     Actions, ADO, Jenkins, GitLab, CircleCI, Travis, Buildkite,
//     CodeBuild, plus a presence-only CI=<anything> check)
//   - TERM=dumb
//   - Linux with neither DISPLAY nor WAYLAND_DISPLAY set
//   - Any SSH session (SSH_CONNECTION or SSH_TTY set)
//
// On Windows and macOS the OS clipboard is always reachable in
// principle, so we only skip on the universal heuristics there.
func isHeadlessEnv(env clipboardEnv) bool {
	for _, key := range headlessCIEnvVars {
		if v, ok := env.Lookup(key); ok && v != "" {
			return true
		}
	}
	if v, ok := env.Lookup("TERM"); ok && strings.EqualFold(v, "dumb") {
		return true
	}
	if _, ok := env.Lookup("SSH_CONNECTION"); ok {
		return true
	}
	if _, ok := env.Lookup("SSH_TTY"); ok {
		return true
	}
	if runtime.GOOS == "linux" {
		_, hasX := env.Lookup("DISPLAY")
		_, hasWayland := env.Lookup("WAYLAND_DISPLAY")
		if !hasX && !hasWayland {
			return true
		}
	}
	return false
}

// headlessCIEnvVars mirrors azdext.ciEnvVars so the extension agrees with
// the host on what counts as a CI environment. Any non-empty value here
// is treated as CI (presence-based, not just CI="true").
var headlessCIEnvVars = []string{
	"CI",
	"GITHUB_ACTIONS",
	"TF_BUILD",
	"JENKINS_URL",
	"GITLAB_CI",
	"CIRCLECI",
	"TRAVIS",
	"BUILDKITE",
	"CODEBUILD_BUILD_ID",
}

// printStarterPrompt writes a styled "Starter prompt for your AI agent:"
// header followed by the rendered body. The body is printed verbatim so
// the user can select + copy it manually if the clipboard step is
// skipped or fails.
//
// The header uses bold yellow to draw the eye
// without competing with azd's standard purple branding above.
func printStarterPrompt(w io.Writer, body string) {
	header := color.New(color.FgYellow, color.Bold).Sprint("Starter prompt for your AI agent:")
	fmt.Fprintln(w, header)
	fmt.Fprintln(w)
	fmt.Fprintln(w, body)
	fmt.Fprintln(w)
}
