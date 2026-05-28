// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// help_output.go layers three sections on top of the default cobra `--help`
// output:
//
//  1. A state-aware "Get started" preamble (root command only). Renders only
//     when the current workspace is incomplete -- quiet for fully-deployed
//     projects so seasoned users see no noise.
//
//  2. An Environments & Environment Variables section. Documents how azd
//     loads env vars from .azure/<env>/.env and lists the agents-specific
//     vars.
//
//  3. A Docs & Agent Skills section. Points at the in-binary read paths
//     (show, project show, doctor) plus the azure.ai.docs front-door
//     extension that surfaces the agent-friendly workflow docs.
//
// All three sections live in this file (not in banner.go) because banner.go
// is responsible only for the visual ASCII banner, and rendering decisions
// for the env-var/docs/preamble sections require a context-bound lookup
// that the banner doesn't need.

package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"azureaiagent/internal/helpformat"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// installAgentsHelpOutput installs the agents-extension help func. On the
// root command we render a custom layout:
//
//	banner
//	Short / Long description
//	state-aware "Get started" preamble (when applicable)
//	Usage / Aliases / Commands / Flags  (via cmd.UsageString)
//	Environments & Environment Variables
//	Docs & Agent Skills
//
// Subcommand --help is delegated unchanged to cobra's default HelpFunc.
//
// We install a styled UsageTemplate on the root so the cmd.UsageString()
// call below returns underlined-header sections. We deliberately do NOT
// call helpformat.Install (which would also set a HelpTemplate); the
// root keeps its bespoke HelpFunc so the banner / state-aware preamble /
// trailing env-vars / docs sections continue to bracket the styled middle.
func installAgentsHelpOutput(rootCmd *cobra.Command) {
	helpformat.InstallUsageOnly(rootCmd)

	defaultHelp := rootCmd.HelpFunc()
	rootCmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		w := cmd.OutOrStdout()
		if cmd != rootCmd {
			defaultHelp(cmd, args)
			return
		}

		printBanner(w)

		// Short or Long, mirroring cobra's default-template precedence so the
		// description still leads -- followed by the state-aware preamble
		// before any Usage block.
		if desc := strings.TrimRightFunc(cmd.Long, unicode.IsSpace); desc != "" {
			fmt.Fprintln(w, desc)
			fmt.Fprintln(w)
		} else if desc := strings.TrimRightFunc(cmd.Short, unicode.IsSpace); desc != "" {
			fmt.Fprintln(w, desc)
			fmt.Fprintln(w)
		}

		if preamble := resolveGetStartedPreamble(cmd.Context()); preamble != "" {
			// preamble already ends with "\n"; pair Fprint with Fprintln so
			// exactly one blank line sits between it and the Usage block.
			fmt.Fprint(w, preamble)
			fmt.Fprintln(w)
		}

		// UsageString emits Usage / Aliases / Commands / Flags / etc. via the
		// SDK-wrapped UsageFunc, so reserved-flag overrides still apply.
		fmt.Fprint(w, cmd.UsageString())

		fmt.Fprintln(w)
		fmt.Fprint(w, environmentVariablesSection())
		fmt.Fprintln(w)
		fmt.Fprint(w, docsAndAgentSkillsSection())
	})
}

// resolveGetStartedPreamble returns a short "Get started" hint when the
// current workspace is missing something the agent needs. Returns empty
// when nothing actionable is missing (fully deployed) so the help output
// stays terse for users who already know what they're doing.
//
// Detection ladder, top match wins:
//  1. No azure.yaml in cwd / parent     -> azd init + azd ai agent init
//  2. azure.yaml exists, no ai.agent svc -> azd ai agent init
//  3. ai.agent service, no project endpoint -> azd provision + project show
//  4. Project endpoint, no AGENT_*_*_ENDPOINT env var -> azd deploy
//  5. Fully deployed                    -> empty
func resolveGetStartedPreamble(ctx context.Context) string {
	// Walk up the filesystem looking for azure.yaml. Best-effort -- any
	// error short-circuits to "no project detected" so the preamble can
	// still surface useful guidance.
	azureYamlPath, found := findAzureYaml()
	if !found {
		return formatGetStarted(
			"No azd project detected. Get started with:",
			"azd ai agent init         Initialize an azd ai agent project.",
		)
	}

	// Re-use the azd host to inspect the project. If the host isn't running
	// (e.g. someone invoked the extension binary directly), skip the deeper
	// detection -- the user already has azure.yaml, which is enough context
	// to call init or deploy themselves.
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return ""
	}
	defer azdClient.Close()

	hasAgentSvc := hasAgentService(ctx, azdClient)
	if !hasAgentSvc {
		return formatGetStarted(
			fmt.Sprintf("azure.yaml at %s has no azd ai agent service. Get started with:", azureYamlPath),
			"azd ai agent init         Add an azd ai agent service to this project.",
		)
	}

	if !hasResolvedProjectEndpoint(ctx) {
		return formatGetStarted(
			"No Foundry project endpoint resolved. Get started with:",
			"azd provision             Provision Foundry resources for this project.",
			"azd ai project show       Inspect the current project context.",
		)
	}

	if !hasDeployedAgent(ctx, azdClient) {
		return formatGetStarted(
			"Agent not yet deployed. Get started with:",
			"azd deploy                Deploy the agent.",
			"azd ai agent show         Inspect the deployed agent status (returns 'not_deployed' until then).",
		)
	}

	// Fully deployed -- stay quiet.
	return ""
}

// formatGetStarted renders the preamble block: a bold header line followed
// by two-column lines of `command  description`. Uses a clean two-column
// spacing style; the heading uses the same purple as the banner for visual unity.
func formatGetStarted(header string, lines ...string) string {
	var b strings.Builder
	purple := color.RGB(109, 53, 255).Add(color.Bold)
	b.WriteString(purple.Sprint(header))
	b.WriteString("\n\n")
	for _, line := range lines {
		b.WriteString("  ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// findAzureYaml walks up from the current working directory looking for an
// azure.yaml. Returns the absolute path and true if found, empty + false
// otherwise. Bounded by the filesystem root.
func findAzureYaml() (string, bool) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", false
	}
	dir := cwd
	for {
		candidate := filepath.Join(dir, "azure.yaml")
		if _, statErr := os.Stat(candidate); statErr == nil {
			return candidate, true
		}
		parent := filepath.Dir(dir)
		if parent == dir { // reached the root
			return "", false
		}
		dir = parent
	}
}

// hasAgentService reports whether the active azd project lists any service
// with type "azure.ai.agent". Best-effort -- returns false on any RPC or
// inspection error.
func hasAgentService(ctx context.Context, azdClient *azdext.AzdClient) bool {
	resp, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil || resp == nil || resp.Project == nil {
		return false
	}
	for _, svc := range resp.Project.Services {
		if svc != nil && strings.EqualFold(svc.Host, agentServiceHostName) {
			return true
		}
	}
	return false
}

// agentServiceHostName is the azure.yaml `host:` value for an azd ai agent
// service. Lower-case because the EqualFold comparison normalizes.
const agentServiceHostName = "azure.ai.agent"

// hasResolvedProjectEndpoint returns true when the 5-level cascade in
// resolveProjectEndpoint produces a value. Wraps the existing resolver so
// we don't replicate its precedence rules here.
func hasResolvedProjectEndpoint(ctx context.Context) bool {
	resolved, err := resolveProjectEndpoint(ctx, resolveProjectEndpointOpts{})
	return err == nil && resolved != nil && resolved.Endpoint != ""
}

// hasDeployedAgent returns true when ANY env value matching the pattern
// AGENT_*_ENDPOINT (or AGENT_*_*_ENDPOINT) exists on the current azd env.
// Best-effort -- treats RPC failures as "no deployed agent".
func hasDeployedAgent(ctx context.Context, azdClient *azdext.AzdClient) bool {
	envResp, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil || envResp == nil || envResp.Environment == nil {
		return false
	}
	values, err := azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{
		Name: envResp.Environment.Name,
	})
	if err != nil || values == nil {
		return false
	}
	for _, kv := range values.KeyValues {
		if kv == nil {
			continue
		}
		if strings.HasPrefix(kv.Key, "AGENT_") && strings.HasSuffix(kv.Key, "_ENDPOINT") && kv.Value != "" {
			return true
		}
	}
	return false
}

// environmentVariablesSection renders the Environments & Environment Variables help block.
// Documents the .azure/<env>/.env mechanism plus the agent-specific vars.
// Lives on the root --help only so it stays terse on leaf-command help.
//
// Header uses helpformat.SectionHeader so it matches the bold+underlined
// styling of the Install-managed sections above (Usage, Available
// Commands, Flags, Global Flags). Command tokens (azd env *) render
// blue and placeholders (<name>, <KEY>, <VALUE>) render yellow, mirroring
// the Examples block convention.
func environmentVariablesSection() string {
	var b strings.Builder
	b.WriteString(helpformat.SectionHeader("Environments & Environment Variables"))
	b.WriteString("\n  azd loads environment variables from `.azure/<env-name>/.env` in your\n")
	b.WriteString("  project. Manage them with:\n\n")
	// Right-pad the styled cell with ANSI-aware accounting: padding sits
	// AFTER the colored token so the visible width still aligns the
	// description column at column 32. The pad-count formula below treats
	// each visible token character as one column, ignoring the zero-width
	// ANSI escape bytes that fatih/color injects around the styled text.
	envLine := func(cmd, desc string) {
		const col = 30
		visible := len(cmd)
		pad := strings.Repeat(" ", max(2, col-visible))
		b.WriteString("  ")
		b.WriteString(helpformat.Command(cmd))
		b.WriteString(pad)
		b.WriteString(desc)
		b.WriteString("\n")
	}
	envLineWithArg := func(cmd, arg, desc string) {
		const col = 30
		visible := len(cmd) + 1 + len(arg)
		pad := strings.Repeat(" ", max(2, col-visible))
		b.WriteString("  ")
		b.WriteString(helpformat.Command(cmd))
		b.WriteString(" ")
		b.WriteString(helpformat.Arg(arg))
		b.WriteString(pad)
		b.WriteString(desc)
		b.WriteString("\n")
	}
	envLine("azd env list", "List azd environments in this project.")
	envLineWithArg("azd env new", "<name>", "Create a new azd environment.")
	envLineWithArg("azd env select", "<name>", "Switch the active azd environment.")
	envLineWithArg("azd env get", "<KEY>", "Read a value from the active env.")
	b.WriteString(fmt.Sprintf("  %s %s %s%sWrite a value to the active env.\n",
		helpformat.Command("azd env set"),
		helpformat.Arg("<KEY>"),
		helpformat.Arg("<VALUE>"),
		strings.Repeat(" ", 30-len("azd env set <KEY> <VALUE>")),
	))
	b.WriteString("\n  Variables read by this extension:\n\n")
	// Env var names render yellow to read as placeholder-like values
	// (matching the Arg convention) so they stand out from prose.
	varLine := func(name, desc string) {
		const col = 30
		visible := len(name)
		pad := strings.Repeat(" ", max(2, col-visible))
		b.WriteString("  ")
		b.WriteString(helpformat.Arg(name))
		b.WriteString(pad)
		b.WriteString(desc)
		b.WriteString("\n")
	}
	varLine("AZURE_SUBSCRIPTION_ID", "Azure subscription used for all resource operations.")
	varLine("AZURE_LOCATION", "Default Azure region for provisioning resources.")
	varLine("AZURE_AI_PROJECT_ENDPOINT", "Project endpoint, read from active azd env. (legacy, will be removed in a future release)")
	varLine("FOUNDRY_PROJECT_ENDPOINT", "Project endpoint, read from active azd env. (recommended)")
	varLine("AZURE_AI_PROJECT_ID", "ARM resource ID; used to build the Foundry")
	b.WriteString("                                portal playground URL.\n")
	varLine("AGENT_<SVC>_<PROTO>_ENDPOINT", "Per-service deployed endpoint URL, one per")
	b.WriteString("                                protocol (e.g. AGENT_MY_AGENT_RESPONSES_ENDPOINT).\n")
	varLine("AGENT_<SVC>_ENDPOINT", "Legacy single-endpoint var for older deployments.")
	return b.String()
}

// docsAndAgentSkillsSection renders the Docs & Agent Skills help block.
// The agent-friendly workflow docs are owned by the azure.ai.docs extension
// (a separate front-door extension) and reached via `azd ai doc agent`.
// This section also points at the in-binary read paths that exist today
// (show, project show, doctor) so agents can drive the most common
// inspection workflows without installing the docs extension first.
//
// Header uses helpformat.SectionHeader for visual parity with the
// Install-managed sections; command tokens render blue, --output flag
// blue, the json arg yellow.
func docsAndAgentSkillsSection() string {
	var b strings.Builder
	b.WriteString(helpformat.SectionHeader("Docs & Agent Skills"))
	b.WriteString("\n  Inspect state, identity, and health from the terminal:\n\n")
	// Each line: blue command + blue --output flag + yellow json + padded description.
	// The visible width is len(cmd) + " --output json" (14) = cmd+14. Aim
	// for description column 50 -- the longest cmd is "azd ai project show"
	// (19 chars) + 14 = 33, +17 spaces = 50.
	docLine := func(cmd, desc string) {
		const col = 48
		visible := len(cmd) + len(" --output json")
		pad := strings.Repeat(" ", max(2, col-visible))
		b.WriteString("  ")
		b.WriteString(helpformat.Command(cmd))
		b.WriteString(" ")
		b.WriteString(helpformat.Flag("--output"))
		b.WriteString(" ")
		b.WriteString(helpformat.Arg("json"))
		b.WriteString(pad)
		b.WriteString(desc)
		b.WriteString("\n")
	}
	docLine("azd ai agent show", "Inspect the deployed agent record (JSON).")
	docLine("azd ai project show", "Inspect identity, subscription, and project context.")
	docLine("azd ai agent doctor", "Diagnose configuration, auth, and deployment issues.")
	b.WriteString("\n  Agent-friendly workflow docs (install the azure.ai.docs extension):\n\n")
	// These lines are plain commands, no --output flag.
	cmdLine := func(cmd, desc string) {
		const col = 48
		visible := len(cmd)
		pad := strings.Repeat(" ", max(2, col-visible))
		b.WriteString("  ")
		b.WriteString(helpformat.Command(cmd))
		b.WriteString(pad)
		b.WriteString(desc)
		b.WriteString("\n")
	}
	cmdLine("azd ext install azure.ai.docs", "One-time install of the docs front door.")
	cmdLine("azd ai doc", "List ai.* extensions with docs available.")
	cmdLine("azd ai doc agent", "List skill topics for this extension.")
	b.WriteString(fmt.Sprintf("  %s %s%sPrint one topic (initialize, configure, investigate, operate).\n",
		helpformat.Command("azd ai doc agent"),
		helpformat.Arg("<topic>"),
		strings.Repeat(" ", 48-len("azd ai doc agent <topic>")),
	))
	return b.String()
}
