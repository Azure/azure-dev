// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// init_preflow.go implements the agent-driven onboarding pre-flow that
// runs at the very top of `azd ai agent init` in interactive mode.
//
// Flow (locked in design pass, see plan.md Phase 7):
//
//   Q1 [Confirm]  Do you want your coding agent to set up and create an
//                 agent in Microsoft Foundry?
//                   No  -> return (handled=false) -> existing init runs.
//                   Yes -> continue.
//
//   Q2 [Confirm]  Install the AZD AI skill for your coding
//                 agent?
//                   Yes -> Q3 -> install
//                   No  -> skip install, go to starter prompt
//
//   Q3 [Select]   Which coding agent are you using?
//                 (claude / codex / gemini / copilot / opencode / custom)
//                 custom -> prompt for path
//
//   Install       Shell out to `azd ai doc skills install ...`. If the
//                 docs extension is missing, offer to install it first.
//
//   Render        Print the starter prompt, optionally copy it to the
//                 system clipboard, show a tool-specific "you're ready
//                 to go" block.
//
//   Return        (handled=true) -- caller skips the existing init flow.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"azureaiagent/internal/exterrors"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/fatih/color"
)

// docsExtensionID is the canonical ID of the docs front-door extension
// that owns `azd ai doc skills install`. Kept as a constant so the
// install-detection helper and the dispatch helper agree on the spelling.
const docsExtensionID = "azure.ai.docs"

// preflowTarget mirrors a built-in target choice in the docs install
// command, with the tool-friendly extras the pre-flow needs:
// displayLabel (shown in the Select choice list, with gray-colored
// path) and pasteInstruction (used in the ready-to-go block).
type preflowTarget struct {
	// targetValue is the --target argument passed to
	// `azd ai doc skills install` (e.g. "copilot").
	targetValue string
	// displayName is the tool's user-facing name (e.g. "GitHub Copilot").
	displayName string
	// installPath is the relative directory the install writes into.
	// Empty for "custom" -- user provides via the follow-up prompt.
	installPath string
	// pasteInstruction is the per-tool sentence in the ready-to-go
	// block (e.g. "Open GitHub Copilot Chat and paste the prompt.").
	pasteInstruction string
}

// preflowTargets is the ordered list of choices shown in Q3. Order
// drives both the Select option order and the help text. Matches
// the targets table in azure.ai.docs' skill_install.go.
var preflowTargets = []preflowTarget{
	{
		targetValue:      "claude",
		displayName:      "Claude Code",
		installPath:      ".claude/skills/azd-ai-skill",
		pasteInstruction: "Open Claude Code and paste the prompt.",
	},
	{
		targetValue:      "codex",
		displayName:      "Codex",
		installPath:      ".agents/skills/azd-ai-skill",
		pasteInstruction: "Open Codex CLI and paste the prompt.",
	},
	{
		targetValue:      "gemini",
		displayName:      "Gemini CLI",
		installPath:      ".agents/skills/azd-ai-skill",
		pasteInstruction: "Open Gemini CLI and paste the prompt.",
	},
	{
		targetValue:      "copilot",
		displayName:      "GitHub Copilot",
		installPath:      ".agents/skills/azd-ai-skill",
		pasteInstruction: "Open GitHub Copilot Chat and paste the prompt.",
	},
	{
		targetValue:      "opencode",
		displayName:      "Opencode",
		installPath:      ".agents/skills/azd-ai-skill",
		pasteInstruction: "Open Opencode and paste the prompt.",
	},
	{
		targetValue:      "custom",
		displayName:      "Custom path",
		installPath:      "",
		pasteInstruction: "Open your coding agent and paste the prompt.",
	},
}

// InitPreflowAction is the action object the cobra RunE constructs and
// calls when in interactive mode (matches the action-object pattern used
// by sample_list.go / show.go / etc.).
type InitPreflowAction struct {
	out       io.Writer
	azdClient *azdext.AzdClient
	runner    azdRunner
	// cwd is the working directory used both for rendering the starter
	// prompt (ProjectPath substitution) and as the implicit root for
	// install paths.
	cwd string
	// copyClip copies text to the system clipboard. Returns the
	// 3-valued outcome (Copied / Skipped / Failed). Injected so tests
	// can drive every branch deterministically.
	copyClip func(text string) ClipboardOutcome
	// azureContext holds the Azure subscription/tenant scope resolved
	// during Q4 (Foundry project selection). Seeded empty by the caller
	// so methods can populate it without allocating.
	azureContext *azdext.AzureContext
}

// Run executes the pre-flow. Returns (handled, err) where:
//   - handled == true: the user delegated to a coding agent; the caller
//     MUST skip the existing InitAction so we do not double-prompt.
//   - handled == false: the user declined Q1; the caller proceeds with
//     the existing init flow unchanged.
func (a *InitPreflowAction) Run(ctx context.Context) (bool, error) {
	delegate, err := a.askDelegate(ctx)
	if err != nil {
		return false, err
	}
	if !delegate {
		// Q1=No -- existing init takes over. The caller checks the
		// handled bool, so returning err=nil here is correct.
		return false, nil
	}

	// From here on we own the flow regardless of errors; always return
	// handled=true so the caller skips InitAction.

	// chosen tracks the tool the user picked at Q3. When Q2=No (no
	// install) we never run Q3 -- fall back to the "custom" copy in the
	// ready-to-go block since we cannot name a specific tool then.
	//
	// We MUST track the chosen target directly rather than recover it
	// from the install path because codex/gemini/copilot/opencode all
	// install to the same path (.agents/skills/azd-ai-skill); a
	// reverse-lookup by path would always resolve to the first matching
	// entry (codex), producing wrong "Open Codex CLI ..." text even
	// when the user selected GitHub Copilot.
	chosen := preflowTargets[len(preflowTargets)-1] // "custom" default

	var installedAt string
	wantInstall, err := a.askInstallSkill(ctx)
	if err != nil {
		return true, err
	}
	if wantInstall {
		target, customPath, err := a.askTargetTool(ctx)
		if err != nil {
			return true, err
		}
		chosen = target
		path, err := a.installSkill(ctx, target, customPath)
		if err != nil {
			return true, err
		}
		installedAt = path
	}

	// Q4: Foundry project selection.
	project, credential, err := a.askFoundryProject(ctx)
	if err != nil {
		return true, err
	}

	projectId := ""
	if project != nil {
		projectId = project.ResourceId
	}

	// Q5: Model deployment selection.
	modelDeployment, err := a.askModelDeployment(ctx, project, credential)
	if err != nil {
		return true, err
	}

	body, err := renderStarterPrompt(StarterPromptVars{
		ProjectPath:      a.cwd,
		SkillPath:        installedAt,
		FoundryProjectId: projectId,
		ModelDeployment:  modelDeployment,
	})
	if err != nil {
		return true, fmt.Errorf("render starter prompt: %w", err)
	}

	printStarterPrompt(a.out, body)
	a.handleClipboard(ctx, body)
	a.printReadyToGo(chosen, installedAt)

	return true, nil
}

// askDelegate is Q1. Default value is "No" so the existing init flow is
// the path of least surprise for users who just hit enter.
func (a *InitPreflowAction) askDelegate(ctx context.Context) (bool, error) {
	resp, err := a.azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
		Options: &azdext.ConfirmOptions{
			Message:      "Do you want your coding agent to set up and create an agent in Microsoft Foundry?",
			DefaultValue: new(false),
		},
	})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return false, exterrors.Cancelled("initialization was cancelled")
		}
		return false, fmt.Errorf("prompt delegate-to-agent: %w", err)
	}
	if resp == nil || resp.Value == nil {
		return false, nil
	}
	return *resp.Value, nil
}

// askInstallSkill is Q2. Default value is "Yes" -- if the user said
// yes to Q1 it's a strong signal they want the skill installed.
func (a *InitPreflowAction) askInstallSkill(ctx context.Context) (bool, error) {
	resp, err := a.azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
		Options: &azdext.ConfirmOptions{
			Message:      "Install the AZD AI skill for your coding agent?",
			DefaultValue: new(true),
		},
	})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return false, exterrors.Cancelled("initialization was cancelled")
		}
		return false, fmt.Errorf("prompt install-skill: %w", err)
	}
	if resp == nil || resp.Value == nil {
		return false, nil
	}
	return *resp.Value, nil
}

// askTargetTool is Q3. Returns the chosen target plus, for "custom", the
// resolved relative path the user typed.
func (a *InitPreflowAction) askTargetTool(ctx context.Context) (preflowTarget, string, error) {
	choices := make([]*azdext.SelectChoice, len(preflowTargets))
	for i, t := range preflowTargets {
		choices[i] = &azdext.SelectChoice{
			Value: t.targetValue,
			Label: targetSelectLabel(t),
		}
	}

	resp, err := a.azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message: "Which coding agent are you using?",
			Choices: choices,
		},
	})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return preflowTarget{}, "", exterrors.Cancelled("initialization was cancelled")
		}
		return preflowTarget{}, "", fmt.Errorf("prompt coding-agent target: %w", err)
	}
	if resp == nil || resp.Value == nil {
		return preflowTarget{}, "", fmt.Errorf("no target selected")
	}

	chosen := preflowTargets[int(*resp.Value)]

	if chosen.targetValue != "custom" {
		return chosen, "", nil
	}

	pathResp, err := a.azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:     "Custom install path (relative to current directory):",
			HelpMessage: "Example: .my-tool/skills/foundry",
		},
	})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return preflowTarget{}, "", exterrors.Cancelled("initialization was cancelled")
		}
		return preflowTarget{}, "", fmt.Errorf("prompt custom install path: %w", err)
	}
	if pathResp == nil {
		return preflowTarget{}, "", fmt.Errorf("no custom install path provided")
	}
	customPath := strings.TrimSpace(pathResp.Value)
	if customPath == "" {
		return preflowTarget{}, "", fmt.Errorf("custom install path must not be empty")
	}
	return chosen, customPath, nil
}

// targetSelectLabel renders a Q3 Select choice label: tool name first,
// path in gray after, e.g. "GitHub Copilot (.agents/skills/azd-ai-skill)".
// Matches the look of azd's `WithGrayFormat` convention.
func targetSelectLabel(t preflowTarget) string {
	if t.installPath == "" {
		return t.displayName
	}
	return fmt.Sprintf("%s %s", t.displayName, output.WithGrayFormat("("+t.installPath+")"))
}

// installSkill performs the actual install via the docs front-door
// extension. Returns the install path on success (used for the starter
// prompt's SkillPath substitution).
func (a *InitPreflowAction) installSkill(ctx context.Context, target preflowTarget, customPath string) (string, error) {
	if err := a.ensureDocsExtension(ctx); err != nil {
		return "", err
	}

	args := []string{"ai", "doc", "skills", "install",
		"--target", target.targetValue,
		"--no-prompt",
		"--output", "json",
	}
	if target.targetValue == "custom" {
		args = append(args, "--path", customPath)
	}

	// Capture the child's stdout so we can parse the JSON install
	// receipt. Forward stderr to the parent terminal so any install
	// failure detail is visible to the user live -- passing nil to
	// runner.Run would discard stderr (os/exec drops nil Cmd.Stderr).
	var stdout strings.Builder
	if err := a.runner.Run(ctx, args, &stdout, a.out); err != nil {
		// We pre-checked docs-extension presence in ensureDocsExtension
		// above (see ext_lookup.go for the rationale on why we don't
		// rely on azd's auto-install). Any error here is from the
		// install command itself; wrap and re-raise.
		return "", fmt.Errorf("run `azd ai doc skills install`: %w", err)
	}

	var result skillInstallReceipt
	raw := strings.TrimSpace(stdout.String())
	if raw == "" {
		// No JSON -> degrade to "we don't know the path" rather than fail.
		if target.targetValue == "custom" {
			return customPath, nil
		}
		return target.installPath, nil
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		// JSON parse failure is not fatal -- the install itself
		// succeeded (exit 0). Fall back to the declared path.
		if target.targetValue == "custom" {
			return customPath, nil
		}
		return target.installPath, nil
	}
	if result.Path != "" {
		return result.Path, nil
	}
	if target.targetValue == "custom" {
		return customPath, nil
	}
	return target.installPath, nil
}

// skillInstallReceipt mirrors the JSON wire shape emitted by
// `azd ai doc skills install --output json`. Decoupled from the
// azure.ai.docs source struct so the two extensions can ship
// independently without cross-extension type imports.
type skillInstallReceipt struct {
	Status string   `json:"status"`
	Target string   `json:"target"`
	Path   string   `json:"path"`
	Files  []string `json:"files"`
}

// ensureDocsExtension verifies that azure.ai.docs is installed. When it
// is not, prompts the user to install it and shells out to
// `azd ext install azure.ai.docs` on confirm. Returns an error explaining
// what to run when the user declines.
func (a *InitPreflowAction) ensureDocsExtension(ctx context.Context) error {
	lookup, err := lookupExtension(ctx, a.runner, docsExtensionID)
	if err != nil {
		// Lookup failure is treated as a soft warning rather than a hard
		// stop: the install dispatch below may still work (e.g. the user
		// installed via an unusual source). The shell-out's own error
		// surfaces if the dispatch really does fail.
		fmt.Fprintf(a.out, "%s could not check whether %s is installed: %v\n",
			color.YellowString("warning:"), docsExtensionID, err)
		return nil
	}
	if lookup.Installed {
		return nil
	}

	resp, err := a.azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
		Options: &azdext.ConfirmOptions{
			Message: fmt.Sprintf(
				"The %s extension is required. Install it now?", docsExtensionID),
			DefaultValue: new(true),
		},
	})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return exterrors.Cancelled("initialization was cancelled")
		}
		return fmt.Errorf("prompt install %s: %w", docsExtensionID, err)
	}

	if resp == nil || resp.Value == nil || !*resp.Value {
		return fmt.Errorf(
			"%s is not installed. Run `azd ext install %s` and re-try",
			docsExtensionID, docsExtensionID)
	}

	fmt.Fprintln(a.out)
	fmt.Fprintf(a.out, "Installing %s...\n", docsExtensionID)
	if err := installExtension(ctx, a.runner, docsExtensionID, a.out, a.out); err != nil {
		return fmt.Errorf("auto-install %s: %w", docsExtensionID, err)
	}
	return nil
}

// handleClipboard offers to copy the prompt to the system clipboard
// when the environment looks interactive, and prints the right
// follow-up message in every outcome (copied / skipped / failed /
// user-declined).
func (a *InitPreflowAction) handleClipboard(ctx context.Context, body string) {
	// Pre-check the environment. When we know clipboard access is
	// impossible (CI, headless Linux, SSH, etc.), skip the confirm
	// prompt entirely -- asking would only confuse the user.
	if env := (osClipboardEnv{}); isHeadlessEnv(env) {
		fmt.Fprintln(a.out, output.WithGrayFormat(
			"Copy the prompt above manually -- no clipboard available in this environment."))
		fmt.Fprintln(a.out)
		return
	}

	resp, err := a.azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
		Options: &azdext.ConfirmOptions{
			Message:      "Copy prompt to clipboard?",
			DefaultValue: new(true),
		},
	})
	if err != nil {
		if exterrors.IsCancellation(err) {
			// Cancellation here is non-fatal -- we already printed the
			// prompt, the user can copy it manually.
			fmt.Fprintln(a.out)
			return
		}
		fmt.Fprintln(a.out, output.WithGrayFormat(
			"Skipped clipboard copy. Copy the prompt above manually."))
		fmt.Fprintln(a.out)
		return
	}
	if resp == nil || resp.Value == nil || !*resp.Value {
		fmt.Fprintln(a.out, output.WithGrayFormat(
			"OK -- copy the prompt above manually when you're ready."))
		fmt.Fprintln(a.out)
		return
	}

	switch a.copyClip(body) {
	case ClipboardCopied:
		fmt.Fprintln(a.out, output.WithSuccessFormat("The prompt is copied to your clipboard!"))
	case ClipboardSkipped:
		// Belt-and-suspenders: handleClipboard pre-checked, but if the
		// helper still reports Skipped (e.g. the env changed mid-run),
		// soft-fail with the same message.
		fmt.Fprintln(a.out, output.WithGrayFormat(
			"Copy the prompt above manually -- no clipboard available in this environment."))
	case ClipboardFailed:
		fmt.Fprintln(a.out, output.WithGrayFormat(
			"Could not access the clipboard -- copy the prompt above manually."))
	}
	fmt.Fprintln(a.out)
}

// printReadyToGo writes the tool-specific "You're ready to go!" block.
// The block is the final thing the user sees from azd before they paste
// the prompt into their coding agent.
//
//   - Bold yellow header                          ("You're ready to go!")
//   - Paste instruction tailored to the target    ("Open Claude Code ...")
//   - What the agent will do                      (short narrative)
//   - Prefer-to-set-up-manually fallback          (azd commands)
//   - Docs link                                   (azd ai doc agent)
//
// When installedAt is empty (user declined Q2), the paste instruction
// drops the install reference but keeps the rest of the block intact so
// the user still has the docs link and manual-fallback commands.
func (a *InitPreflowAction) printReadyToGo(target preflowTarget, installedAt string) {
	bold := color.New(color.FgYellow, color.Bold)
	fmt.Fprintln(a.out, bold.Sprint("You're ready to go!"))
	fmt.Fprintln(a.out)

	fmt.Fprintln(a.out, color.New(color.Bold).Sprint(target.pasteInstruction))
	fmt.Fprintln(a.out)

	if installedAt != "" {
		fmt.Fprintln(a.out, output.WithGrayFormat("Your agent will use the AZD AI skill at %s", installedAt))
		fmt.Fprintln(a.out, output.WithGrayFormat("to scaffold, provision, and deploy a Foundry agent tailored"))
		fmt.Fprintln(a.out, output.WithGrayFormat("to your project."))
	} else {
		fmt.Fprintln(a.out, output.WithGrayFormat("Your agent will follow the starter prompt to scaffold, provision,"))
		fmt.Fprintln(a.out, output.WithGrayFormat("and deploy a Foundry agent tailored to your project."))
	}
	fmt.Fprintln(a.out)

	fmt.Fprintln(a.out, color.New(color.Bold).Sprint("Prefer to set up manually?"))
	fmt.Fprintln(a.out, output.WithGrayFormat("  azd ai agent init             Run the interactive scaffolder yourself."))
	fmt.Fprintln(a.out, output.WithGrayFormat("  azd provision                 Provision Foundry resources."))
	fmt.Fprintln(a.out, output.WithGrayFormat("  azd deploy                    Deploy the agent."))
	fmt.Fprintln(a.out, output.WithGrayFormat("  azd ai agent show             Inspect the deployed agent."))
	fmt.Fprintln(a.out)

	fmt.Fprint(a.out, output.WithGrayFormat("Docs: "))
	fmt.Fprintln(a.out, output.WithLinkFormat("https://aka.ms/azd-ai-agent-docs"))
	fmt.Fprintln(a.out, output.WithGrayFormat("      Or run `azd ai doc agent` for the agent-friendly topic index."))
	fmt.Fprintln(a.out)
}

// --- Q4: Foundry project selection ---

// askFoundryProject is Q4. Presents the "use existing / create new" choice for the
// Foundry project. When "use existing" is chosen it prompts for an Azure subscription
// (without persisting to any azd environment), lists projects in that subscription,
// and lets the user pick one. When "create new" is chosen, prompts for subscription
// and location to enable model catalog browsing in Q5.
//
// Returns (project, credential, error):
//   - project != nil with full details: user selected an existing project; credential
//     is valid and was used to list projects (caller may reuse it for model deployment listing in Q5).
//   - project != nil with only SubscriptionId and Location: user chose "Create a new Foundry project"
//     and selected subscription/location; credential is valid for that subscription.
//   - project == nil: user chose "Create a new Foundry project" from initial prompt and there
//     are no existing projects to list; credential is nil.
func (a *InitPreflowAction) askFoundryProject(
	ctx context.Context,
) (*FoundryProjectInfo, azcore.TokenCredential, error) {
	choices := []*azdext.SelectChoice{
		{Label: "Use an existing Foundry project", Value: "existing"},
		{Label: "Create a new Foundry project", Value: "new"},
	}

	resp, err := a.azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message: "Select a Foundry project to host your agent",
			Choices: choices,
		},
	})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return nil, nil, exterrors.Cancelled("initialization was cancelled")
		}
		return nil, nil, fmt.Errorf("prompt Foundry project choice: %w", err)
	}
	if resp == nil || resp.Value == nil || choices[*resp.Value].Value == "new" {
		// User chose "Create a new Foundry project" — prompt for subscription and location
		// so Q5 can browse the model catalog.
		subscriptionId, tenantId, location, credential, err := promptSubscriptionAndLocationPreflow(ctx, a.azdClient)
		if err != nil {
			return nil, nil, err
		}
		// Return a minimal project info with subscription, tenant, and location
		// (no actual project since we're creating new).
		return &FoundryProjectInfo{
			SubscriptionId: subscriptionId,
			TenantId:       tenantId,
			Location:       location,
		}, credential, nil
	}

	// User wants an existing project -- resolve subscription and credential.
	subscriptionId, credential, err := a.getPreflowSubscriptionCredential(ctx)
	if err != nil {
		return nil, nil, err
	}

	// Get the tenant ID from the credential resolution
	tenantId, err := a.getTenantForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, nil, err
	}

	// List Foundry projects from ARM (no env writes).
	spinner := ux.NewSpinner(&ux.SpinnerOptions{
		Text:        "Searching for Foundry projects in your subscription...",
		ClearOnStop: true,
	})
	if err := spinner.Start(ctx); err != nil {
		return nil, nil, fmt.Errorf("start spinner: %w", err)
	}
	projects, listErr := listFoundryProjects(ctx, credential, subscriptionId)
	if stopErr := spinner.Stop(ctx); stopErr != nil {
		return nil, nil, stopErr
	}
	if listErr != nil {
		return nil, nil, fmt.Errorf("list Foundry projects: %w", listErr)
	}

	if len(projects) == 0 {
		fmt.Fprintln(a.out, output.WithGrayFormat(
			"No Foundry projects found in the selected subscription. The coding agent will create one."))
		// Prompt for location so Q5 (model deployment) can still browse
		// the model catalog -- otherwise hasAzureContext is false and
		// the "Create a new model deployment" choice silently degrades.
		location, locationErr := a.promptLocationPreflow(ctx)
		if locationErr != nil {
			return nil, nil, locationErr
		}
		return &FoundryProjectInfo{
			SubscriptionId: subscriptionId,
			TenantId:       tenantId,
			Location:       location,
		}, credential, nil
	}

	// Build select choices from the project list.
	projectChoices := make([]*azdext.SelectChoice, 0, len(projects)+1)
	for i, p := range projects {
		projectChoices = append(projectChoices, &azdext.SelectChoice{
			Label: fmt.Sprintf("%s / %s (%s)", p.AccountName, p.ProjectName, p.Location),
			Value: fmt.Sprintf("%d", i),
		})
	}
	projectChoices = append(projectChoices, &azdext.SelectChoice{
		Label: "Create a new Foundry project",
		Value: "__create_new__",
	})

	projectResp, err := a.azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message: "Select a Foundry project",
			Choices: projectChoices,
		},
	})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return nil, nil, exterrors.Cancelled("initialization was cancelled")
		}
		return nil, nil, fmt.Errorf("select Foundry project: %w", err)
	}

	selectedIdx := int(*projectResp.Value)
	if selectedIdx < 0 || selectedIdx >= len(projects) {
		// "Create a new Foundry project" was chosen from the list.
		// Prompt for location so Q5 can browse the model catalog.
		location, locationErr := a.promptLocationPreflow(ctx)
		if locationErr != nil {
			return nil, nil, locationErr
		}
		// Return minimal project info with subscription, tenant, and location for Q5.
		return &FoundryProjectInfo{
			SubscriptionId: subscriptionId,
			TenantId:       tenantId,
			Location:       location,
		}, credential, nil
	}

	selected := projects[selectedIdx]
	// Also store the tenant ID in the selected project
	selected.TenantId = tenantId
	return &selected, credential, nil
}

// getPreflowSubscriptionCredential prompts for an Azure subscription (without persisting
// to any azd environment) and returns the subscription ID plus a matching credential.
// This is intentionally lightweight: the preflow runs before an azd environment exists,
// so we cannot use ensureSubscription (which writes AZURE_SUBSCRIPTION_ID to env).
func (a *InitPreflowAction) getPreflowSubscriptionCredential(
	ctx context.Context,
) (string, azcore.TokenCredential, error) {
	subResp, err := a.azdClient.Prompt().PromptSubscription(ctx, &azdext.PromptSubscriptionRequest{})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return "", nil, exterrors.Cancelled("initialization was cancelled")
		}
		return "", nil, fmt.Errorf("select Azure subscription: %w", err)
	}
	if subResp == nil || subResp.Subscription == nil {
		return "", nil, fmt.Errorf("no subscription selected")
	}

	tenantId := subResp.Subscription.UserTenantId

	// Resolve the credential using the user-access tenant (not the resource tenant).
	cred, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
		TenantID:                   tenantId,
		AdditionallyAllowedTenants: []string{"*"},
	})
	if err != nil {
		return "", nil, fmt.Errorf("create Azure credential: %w", err)
	}

	return subResp.Subscription.Id, cred, nil
}

// getTenantForSubscription looks up the tenant ID for a given subscription.
func (a *InitPreflowAction) getTenantForSubscription(
	ctx context.Context,
	subscriptionId string,
) (string, error) {
	tenantResp, err := a.azdClient.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{
		SubscriptionId: subscriptionId,
	})
	if err != nil {
		return "", fmt.Errorf("lookup tenant for subscription: %w", err)
	}
	return tenantResp.TenantId, nil
}

// promptLocationPreflow prompts for an Azure location without writing to the environment.
// Used when creating a new Foundry project in the preflow.
func (a *InitPreflowAction) promptLocationPreflow(ctx context.Context) (string, error) {
	allowedLocations, err := supportedRegionsForInit(ctx)
	if err != nil {
		return "", err
	}

	fmt.Println("Select an Azure location. This determines which models are available and where your Foundry project resources will be deployed.")
	locationName, err := promptLocationForInit(ctx, a.azdClient, &azdext.AzureContext{
		Scope: &azdext.AzureScope{},
	}, allowedLocations)
	if err != nil {
		return "", err
	}

	return locationName, nil
}

// --- Q5: Model deployment selection ---

// askModelDeployment is Q5. When a Foundry project was selected in Q4 (project != nil
// with full details), offers "Use an existing model deployment" / "Create a new" / "Skip".
// When creating a new project (project has only SubscriptionId and Location), offers
// "Create a new" / "Skip" and uses the model catalog for selection. Otherwise (no project
// at all from Q4) only "Create a new" / "Skip" are offered.
//
// credential must be the one returned by askFoundryProject; it is used to list deployments
// when selecting an existing deployment, or to access the model catalog when creating new.
func (a *InitPreflowAction) askModelDeployment(
	ctx context.Context,
	project *FoundryProjectInfo,
	credential azcore.TokenCredential,
) (string, error) {
	// Determine if we have a full project (can list existing deployments) or just
	// subscription/location (creating new project).
	hasFullProject := project != nil && project.ResourceGroupName != "" && project.AccountName != ""
	hasAzureContext := project != nil && project.SubscriptionId != "" && project.Location != ""

	var choices []*azdext.SelectChoice
	if hasFullProject {
		// Full project — offer all three choices
		choices = []*azdext.SelectChoice{
			{Label: "Use an existing model deployment", Value: "existing"},
			{Label: "Create a new model deployment", Value: "new"},
			{Label: "Skip model deployment selection", Value: "skip"},
		}
	} else {
		// No project or creating new project — only "Create new" or "Skip"
		choices = []*azdext.SelectChoice{
			{Label: "Create a new model deployment", Value: "new"},
			{Label: "Skip model deployment selection", Value: "skip"},
		}
	}

	resp, err := a.azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message: "Model deployment: how would you like to proceed?",
			Choices: choices,
		},
	})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return "", exterrors.Cancelled("initialization was cancelled")
		}
		return "", fmt.Errorf("prompt model deployment choice: %w", err)
	}
	if resp == nil || resp.Value == nil {
		return "", nil
	}

	selectedValue := choices[*resp.Value].Value

	switch selectedValue {
	case "existing":
		// List deployments in the selected project.
		spinner := ux.NewSpinner(&ux.SpinnerOptions{
			Text:        "Searching for model deployments in your Foundry project...",
			ClearOnStop: true,
		})
		if err := spinner.Start(ctx); err != nil {
			return "", fmt.Errorf("start spinner: %w", err)
		}
		deployments, listErr := listProjectDeployments(
			ctx, credential,
			project.SubscriptionId, project.ResourceGroupName, project.AccountName,
		)
		if stopErr := spinner.Stop(ctx); stopErr != nil {
			return "", stopErr
		}
		if listErr != nil {
			return "", fmt.Errorf("list model deployments: %w", listErr)
		}

		if len(deployments) == 0 {
			fmt.Fprintln(a.out, output.WithGrayFormat(
				"No model deployments found in the selected project. The coding agent will create one."))
			return "", nil
		}

		deployChoices := make([]*azdext.SelectChoice, 0, len(deployments))
		for _, d := range deployments {
			label := fmt.Sprintf("%s (%s v%s)", d.Name, d.ModelName, d.Version)
			deployChoices = append(deployChoices, &azdext.SelectChoice{
				Label: label,
				Value: d.Name,
			})
		}

		deployResp, err := a.azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
			Options: &azdext.SelectOptions{
				Message: "Select a model deployment",
				Choices: deployChoices,
			},
		})
		if err != nil {
			if exterrors.IsCancellation(err) {
				return "", exterrors.Cancelled("initialization was cancelled")
			}
			return "", fmt.Errorf("select model deployment: %w", err)
		}

		deployIdx := int(*deployResp.Value)
		if deployIdx < 0 || deployIdx >= len(deployments) {
			return "", nil
		}
		return deployments[deployIdx].Name, nil

	case "new":
		// Create a new model deployment — browse the model catalog.
		if !hasAzureContext {
			// No subscription/location available (shouldn't happen with current flow,
			// but handle defensively).
			return "", nil
		}
		modelDeploymentName, err := selectModelCatalogPreflow(
			ctx, a.azdClient, project.SubscriptionId, project.TenantId, project.Location,
		)
		if err != nil {
			return "", err
		}
		return modelDeploymentName, nil

	case "skip":
		return "", nil

	default:
		return "", nil
	}
}
