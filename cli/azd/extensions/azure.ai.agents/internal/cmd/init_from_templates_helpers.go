// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/fatih/color"
)

const agentTemplatesURL = "https://aka.ms/foundry-agents-samples"

const promptVoicePreviewEnvVar = "AZD_AI_AGENT_ENABLE_PROMPT_VOICE"

func promptVoicePreviewEnabled() bool {
	value := strings.TrimSpace(os.Getenv(promptVoicePreviewEnvVar))
	return strings.EqualFold(value, "1") || strings.EqualFold(value, "true") || strings.EqualFold(value, "yes")
}

// Template type constants
const (
	// TemplateTypeAgent is a template that points to an agent.yaml manifest file.
	TemplateTypeAgent = "agent"

	// TemplateTypeAzd is a full azd template repository.
	TemplateTypeAzd = "azd"

	// TemplateTypeAzureYaml is a unified azure.yaml template adopted via the Foundry flow.
	TemplateTypeAzureYaml = "azure.yaml"

	// templateTypeExtensionAIAgent is the discriminator value in the unified
	// awesome-azd templates.json manifest that identifies an agent-init
	// template. Entries with any other (or empty) templateType belong to the
	// standard awesome-azd gallery and are filtered out.
	templateTypeExtensionAIAgent = "extension.ai.agent"

	// featuredTag is the extensionTags value that marks a template for the
	// curated starter list. These templates are shown first; the user can
	// expand to see the full catalog.
	featuredTag = "featured"

	// recommendedTag is the extensionTags value that identifies the default
	// pre-selected template in the featured list.
	recommendedTag = "recommended"

	// seeAllSentinel is the SelectChoice.Value used for the "See all
	// templates..." option appended to the featured list.
	seeAllSentinel = "__see_all__"
)

// AgentTemplate represents an agent template entry from the remote JSON catalog.
// Field names mirror the awesome-azd templates.json schema.
type AgentTemplate struct {
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	Languages          []string `json:"languages"`
	ExtensionFramework string   `json:"extensionFramework"`
	Source             string   `json:"source"`
	ExtensionTags      []string `json:"extensionTags"`
	TemplateType       string   `json:"templateType"`
}

// EffectiveType determines the template type by inspecting the source URL
// and the template's declared templateType.
// If it ends with agent.yaml or agent.manifest.yaml, it's an agent manifest.
// If it ends with azure.yaml or azure.yml AND templateType is "extension.ai.agent",
// it's a unified azure.yaml template.
// Otherwise, it's treated as a full azd template repo.
func (t *AgentTemplate) EffectiveType() string {
	lower := strings.ToLower(t.Source)
	if strings.HasSuffix(lower, "/agent.yaml") ||
		strings.HasSuffix(lower, "/agent.manifest.yaml") ||
		lower == "agent.yaml" ||
		lower == "agent.manifest.yaml" {
		return TemplateTypeAgent
	}
	if t.TemplateType == templateTypeExtensionAIAgent &&
		(strings.HasSuffix(lower, "/azure.yaml") ||
			strings.HasSuffix(lower, "/azure.yml") ||
			lower == "azure.yaml" ||
			lower == "azure.yml") {
		return TemplateTypeAzureYaml
	}
	return TemplateTypeAzd
}

const (
	initModeFromCode = "from_code"
	initModeTemplate = "template"
	// initModeVoice is chosen when the user wants to create a declarative
	// (managed) voice agent. It maps to the same synthesized-manifest fast path
	// as `azd ai agent init --kind prompt-voice`.
	initModeVoice = "prompt_voice"
)

// agentKindChoice represents the discriminator the user picks at the very
// start of `azd ai agent init`. It selects between the supported agent
// runtimes: hosted (the container/code-deploy flow) and prompt (a Foundry
// prompt agent).
//
// There is deliberately no "managed" choice. A managed agent is a prompt agent
// that names an execution harness, so the harness is an independent dimension
// (--harness) rather than a kind of its own; both scaffold `kind: prompt`.
type agentKindChoice string

const (
	// AgentKindChoiceHosted is the existing hosted-agent path — the customer
	// supplies code or a container image and the platform runs it on Azure
	// Container Apps.
	AgentKindChoiceHosted agentKindChoice = "hosted"
	// AgentKindChoicePrompt is the prompt agent path — the customer declares
	// model + instructions and Foundry runs the agent. The scaffolded agent.yaml
	// uses kind: prompt (see agent_yaml.AgentKindPrompt). Whether it also names
	// a `harness:` is decided separately, by --harness or the kind menu entry.
	AgentKindChoicePrompt agentKindChoice = "prompt"
)

// harnessNone is the --harness value that explicitly opts out of a harness,
// letting `--harness none` degrade a harnessed template to a plain prompt agent.
const harnessNone = "none"

// resolveInitHarness resolves the harness written to the scaffolded agent.yaml.
// An explicit --harness value always wins over impliedHarness — the harness the
// context already suggests, whether that is the menu entry the user picked or
// the `harness:` block of a supplied manifest. Both are validated the same way,
// so a harness that is no longer accepted is reported wherever it came from.
func resolveInitHarness(harnessFlag, impliedHarness string) (string, error) {
	requested := harnessFlag
	if strings.TrimSpace(requested) == "" {
		requested = impliedHarness
	}

	harness := strings.ToLower(strings.TrimSpace(requested))
	switch harness {
	case "", harnessNone:
		return "", nil
	case agent_api.ManagedAgentHarnessGitHubCopilot:
		return agent_api.ManagedAgentHarnessGitHubCopilot, nil
	}

	if replacement, removed := agent_api.RemovedManagedAgentHarnesses[harness]; removed {
		// Named separately from the generic "unknown value" case so the error
		// tells the user what to type instead of only what is allowed.
		return "", exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("--harness %q is no longer accepted", harness),
			fmt.Sprintf("use --harness %s instead", replacement),
		)
	}

	return "", exterrors.Validation(
		exterrors.CodeInvalidParameter,
		fmt.Sprintf("unknown --harness value %q", requested),
		fmt.Sprintf("supported values are: %s, %s", agent_api.ManagedAgentHarnessGitHubCopilot, harnessNone),
	)
}

// kindMenuEntry is one row of the interactive kind picker. A row maps to a
// (kind, harness) pair rather than to a kind alone, because the harnessed
// prompt agent differs from the plain one only by its `harness:` block. Keeping
// the harness on the entry lets the menu offer it as a single choice without
// reintroducing a "managed" kind that nothing downstream understands.
type kindMenuEntry struct {
	label   string
	kind    agentKindChoice
	harness string
}

// agentKindMenu is the ordered set of rows shown by promptAgentKind.
var agentKindMenu = []kindMenuEntry{
	{
		label: "Hosted agent — Bring your own code or framework",
		kind:  AgentKindChoiceHosted,
	},
	{
		label: "Prompt agent (no code, Foundry-managed) — " +
			"Configure a model, instructions, and tools",
		kind: AgentKindChoicePrompt,
	},
	{
		label: "Prompt agent with GitHub Copilot harness (preview) — " +
			"Configure a model, instructions, tools, and skills",
		kind:    AgentKindChoicePrompt,
		harness: agent_api.ManagedAgentHarnessGitHubCopilot,
	},
}

// promptAgentKind asks the user which agent kind to initialize, returning the
// kind and the harness that choice implies. In no-prompt mode it returns
// AgentKindChoiceHosted to preserve today's behavior for CI callers that do not
// yet know about the new kinds. The selection is the very first interactive
// prompt in `azd ai agent init` and routes the rest of the init flow.
func promptAgentKind(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	noPrompt bool,
) (agentKindChoice, string, error) {
	if noPrompt {
		return AgentKindChoiceHosted, "", nil
	}

	choices := make([]*azdext.SelectChoice, 0, len(agentKindMenu))
	for _, entry := range agentKindMenu {
		choices = append(choices, &azdext.SelectChoice{
			Label: entry.label,
			Value: string(entry.kind),
		})
	}
	defaultIndex := int32(0)

	resp, err := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message:       "What type of agent do you want to initialize?",
			Choices:       choices,
			SelectedIndex: &defaultIndex,
		},
	})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return "", "", exterrors.Cancelled("agent kind selection was cancelled")
		}
		return "", "", fmt.Errorf("failed to prompt for agent kind: %w", err)
	}

	// Two menu rows share the value "prompt", so the answer is resolved by
	// index. Guard it: an out-of-range index would otherwise pick a harness at
	// random or panic.
	selected := int(*resp.Value)
	if selected < 0 || selected >= len(agentKindMenu) {
		return "", "", fmt.Errorf("agent kind selection returned an out-of-range index %d", selected)
	}

	entry := agentKindMenu[selected]
	return entry.kind, entry.harness, nil
}

// warnPromptAgentPreview tells the user that prompt-agent support in azd is
// still in preview. It is called from the single place every prompt-agent init
// funnels through, so the notice also reaches flag-driven runs (--kind prompt)
// and manifest-driven ones, not just the interactive picker.
//
// This warns rather than blocks: preview is a stability signal, not a gate.
func warnPromptAgentPreview(writer io.Writer) {
	// Each segment is colored independently. Nesting output.WithBold inside
	// output.WithWarningFormat would emit a reset mid-string, dropping the
	// surrounding yellow and switching the foreground to white from there on.
	emphasis := color.New(color.FgYellow, color.Bold)

	fmt.Fprintf(writer, "%s%s%s",
		output.WithWarningFormat("\n(!) Prompt agents are a "),
		emphasis.Sprint("preview feature of the azd CLI experience"),
		output.WithWarningFormat(
			". The authoring layout and commands may change in a future release.\n\n",
		),
	)
}

// promptInitMode asks the user whether to use existing code or start from a template.
// If the current directory is empty, automatically returns initModeTemplate.
// In no-prompt mode with existing local files, defaults to using the current directory.
// Returns initModeFromCode or initModeTemplate.
// voiceInitChoice is the interactive menu entry for creating a prompt voice agent.
// It is appended to the init-mode choices only when prompt voice private preview
// is explicitly enabled.
var voiceInitChoice = &azdext.SelectChoice{
	Label: "Create a prompt voice agent",
	Value: initModeVoice,
}

// promptInitMode asks the user whether to use existing code, start from a
// template, or create a prompt voice agent.
// If the current directory is empty, the "use existing code" option is omitted
// (there is no code to use). The voice option is private preview and only shown
// when promptVoicePreviewEnabled returns true.
// In no-prompt mode the directory contents decide: empty -> template, otherwise
// use the current directory. Voice is only selectable interactively (or via
// --kind prompt-voice in no-prompt mode).
// Returns initModeFromCode, initModeTemplate, or initModeVoice.
func promptInitMode(ctx context.Context, azdClient *azdext.AzdClient, noPrompt bool) (string, error) {
	empty, err := dirIsEmpty(".")
	if err != nil {
		return "", fmt.Errorf("checking current directory: %w", err)
	}

	if noPrompt {
		if empty {
			return initModeTemplate, nil
		}
		return initModeFromCode, nil
	}
	voicePreviewEnabled := promptVoicePreviewEnabled()
	if empty && !voicePreviewEnabled {
		return initModeTemplate, nil
	}

	var choices []*azdext.SelectChoice
	if empty {
		// No local code to adopt; offer template + voice.
		choices = []*azdext.SelectChoice{
			{Label: "Start new from a template", Value: initModeTemplate},
		}
	} else {
		choices = []*azdext.SelectChoice{
			{Label: "Use the code in the current directory", Value: initModeFromCode},
			{Label: "Start new from a template", Value: initModeTemplate},
		}
	}
	if voicePreviewEnabled {
		choices = append(choices, voiceInitChoice)
	}

	defaultIndex := int32(0)

	resp, err := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message:       "How do you want to initialize your agent?",
			Choices:       choices,
			SelectedIndex: &defaultIndex,
		},
	})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return "", exterrors.Cancelled("initialization mode selection was cancelled")
		}
		return "", fmt.Errorf("failed to prompt for initialization mode: %w", err)
	}

	return choices[*resp.Value].Value, nil
}

// dirIsEmpty reports whether dir contains no entries at all.
func dirIsEmpty(dir string) (bool, error) {
	f, err := os.Open(dir) //nolint:gosec // caller supplies a project directory
	if err != nil {
		return false, err
	}
	defer f.Close()
	_, err = f.Readdirnames(1)
	if errors.Is(err, io.EOF) {
		return true, nil
	}
	return false, err
}

// fetchAgentTemplates retrieves the agent template catalog from the remote
// awesome-azd manifest URL.
func fetchAgentTemplates(ctx context.Context, httpClient *http.Client) ([]AgentTemplate, error) {
	return fetchAgentTemplatesFromURL(ctx, httpClient, agentTemplatesURL)
}

// fetchAgentTemplatesFromURL retrieves the awesome-azd templates manifest from
// the given URL and returns only entries whose templateType marks them as
// agent-init templates. The URL is parameterized to keep this function
// directly testable against an httptest server.
func fetchAgentTemplatesFromURL(
	ctx context.Context,
	httpClient *http.Client,
	url string,
) ([]AgentTemplate, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch agent templates: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch agent templates: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read agent templates response: %w", err)
	}

	var all []AgentTemplate
	if err := json.Unmarshal(body, &all); err != nil {
		return nil, fmt.Errorf("failed to parse agent templates: %w", err)
	}

	// Keep only agent-init entries. The shared templates.json manifest also
	// carries the awesome-azd gallery; those entries must not surface here.
	filtered := make([]AgentTemplate, 0, len(all))
	for _, t := range all {
		if t.TemplateType == templateTypeExtensionAIAgent {
			filtered = append(filtered, t)
		}
	}

	// Always emit the fetched/matched counts to make transition-period and
	// misconfiguration issues debuggable.
	log.Printf(
		"agent templates manifest: fetched %d templateType=%q (source=%s)",
		len(filtered), templateTypeExtensionAIAgent, url,
	)

	// If we received entries but filtered them all out, the manifest is
	// almost certainly in the legacy format or the discriminator value has
	// changed. Surface that explicitly instead of returning an empty list,
	// which the caller cannot distinguish from an intentionally empty manifest.
	if len(all) > 0 && len(filtered) == 0 {
		return nil, fmt.Errorf(
			"agent templates manifest at %s contained %d entries but none had templateType=%q",
			url, len(all), templateTypeExtensionAIAgent,
		)
	}

	return filtered, nil
}

// isFeatured reports whether the template carries the "featured" extensionTag,
// which marks it for the curated starter list.
func (t *AgentTemplate) isFeatured() bool {
	return slices.Contains(t.ExtensionTags, featuredTag)
}

// isRecommended reports whether the template carries the "recommended"
// extensionTag, which marks it as the default pre-selected template.
func (t *AgentTemplate) isRecommended() bool {
	return slices.Contains(t.ExtensionTags, recommendedTag)
}

// promptAgentTemplate guides the user through language selection and template selection.
// Returns the selected AgentTemplate. The caller should check EffectiveType() to determine
// whether to use the agent.yaml manifest flow or the full azd template flow.
//
// Templates tagged "featured" are shown first in a curated list. The template
// tagged "recommended" gets a (Recommended) suffix in the label and is
// pre-selected. A "See all templates..." option expands to the full catalog.
func promptAgentTemplate(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	httpClient *http.Client,
	noPrompt bool,
) (*AgentTemplate, error) {
	if noPrompt {
		return nil, exterrors.Validation(
			exterrors.CodePromptFailed,
			"template selection requires interactive mode",
			"run 'azd ai agent sample list --output json' to discover available templates, "+
				"then rerun 'azd ai agent init -m <manifestUrl>' (or 'azd init -t <repoUrl>' for full template repos)",
		)
	}

	fmt.Println(output.WithGrayFormat("Retrieving agent templates..."))

	templates, err := fetchAgentTemplates(ctx, httpClient)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve agent templates: %w", err)
	}

	if len(templates) == 0 {
		return nil, fmt.Errorf("no agent templates available")
	}

	// Prompt for language. Values must match the language tokens used in
	// the awesome-azd templates.json `languages` field (e.g. "dotnetCsharp").
	languageChoices := []*azdext.SelectChoice{
		{Label: "Python", Value: "python"},
		{Label: "C#", Value: "dotnetCsharp"},
	}

	langResp, err := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message: "Select a language",
			Choices: languageChoices,
		},
	})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return nil, exterrors.Cancelled("language selection was cancelled")
		}
		return nil, fmt.Errorf("failed to prompt for language: %w", err)
	}

	selectedLanguage := languageChoices[*langResp.Value].Value

	// Filter templates by selected language (entries can declare multiple).
	langFiltered := make([]AgentTemplate, 0, len(templates))
	for _, t := range templates {
		if slices.Contains(t.Languages, selectedLanguage) {
			langFiltered = append(langFiltered, t)
		}
	}

	if len(langFiltered) == 0 {
		return nil, fmt.Errorf(
			"no agent templates available for %s",
			languageChoices[*langResp.Value].Label,
		)
	}

	// Partition into featured vs rest.
	featured, rest := partitionFeatured(langFiltered)

	// When there are both featured and non-featured templates, show the
	// curated featured list first with a "See all templates…" escape hatch.
	// When all templates are featured (len(rest) == 0) or none are
	// (len(featured) == 0), skip the curated step and show the full list
	// directly — a curated list that equals the full list adds no value.
	if len(featured) > 0 && len(rest) > 0 {
		defaultIdx := findRecommendedIndex(featured)

		selected, err := promptSelectTemplate(
			ctx, azdClient, featured,
			"Select a starter template", &defaultIdx, true,
		)
		if err != nil {
			return nil, err
		}

		if selected != nil {
			return selected, nil
		}
		// User chose "See all templates…" → fall through to full list.
	}

	// Show the complete catalog (featured + rest, sorted alphabetically).
	allSorted := slices.Clone(langFiltered)
	slices.SortFunc(allSorted, func(a, b AgentTemplate) int {
		return strings.Compare(a.Title, b.Title)
	})

	// Pre-select the recommended template in the full list too.
	recommendedIdx := findRecommendedIndex(allSorted)

	return promptSelectTemplate(
		ctx, azdClient, allSorted,
		"Select an agent template", &recommendedIdx, false,
	)
}

// partitionFeatured splits templates into featured (tagged "featured") and
// the rest. Both slices are sorted alphabetically by title.
func partitionFeatured(templates []AgentTemplate) (featured, rest []AgentTemplate) {
	for _, t := range templates {
		if t.isFeatured() {
			featured = append(featured, t)
		} else {
			rest = append(rest, t)
		}
	}

	sortByTitle := func(a, b AgentTemplate) int {
		return strings.Compare(a.Title, b.Title)
	}
	slices.SortFunc(featured, sortByTitle)
	slices.SortFunc(rest, sortByTitle)

	return featured, rest
}

// findRecommendedIndex returns the index of the recommended default template
// in the given list. It looks for a template tagged "recommended"; if none
// is found it returns 0 (first item in the list).
func findRecommendedIndex(templates []AgentTemplate) int32 {
	for i, t := range templates {
		if t.isRecommended() {
			return boundedInt32Index(i)
		}
	}
	return 0
}

// promptSelectTemplate presents a select prompt for the given templates.
// defaultIdx, when non-nil, pre-selects that index in the list.
// When includeSeeAll is true, a "See all templates…" option is appended;
// selecting it causes the function to return (nil, nil) so the caller can
// re-prompt with the full list.
func promptSelectTemplate(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	templates []AgentTemplate,
	message string,
	defaultIdx *int32,
	includeSeeAll bool,
) (*AgentTemplate, error) {
	choices := make([]*azdext.SelectChoice, len(templates))
	for i, t := range templates {
		choices[i] = &azdext.SelectChoice{
			Label: t.Title,
			Value: fmt.Sprintf("%d", i),
		}
	}

	if includeSeeAll {
		choices = append(choices, &azdext.SelectChoice{
			Label: "See all templates...",
			Value: seeAllSentinel,
		})
	}

	opts := &azdext.SelectOptions{
		Message: message,
		Choices: choices,
	}
	if defaultIdx != nil {
		opts.SelectedIndex = defaultIdx
	}

	resp, err := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
		Options: opts,
	})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return nil, exterrors.Cancelled("template selection was cancelled")
		}
		return nil, fmt.Errorf("failed to prompt for template: %w", err)
	}

	selected := choices[*resp.Value]
	if selected.Value == seeAllSentinel {
		return nil, nil
	}

	return &templates[*resp.Value], nil
}

// findAgentManifest searches the directory tree rooted at dir for the first
// agent.yaml or agent.manifest.yaml file. Returns the path if found, or empty string if not.
func findAgentManifest(dir string) (string, error) {
	manifestNames := map[string]bool{
		"agent.yaml":          true,
		"agent.manifest.yaml": true,
	}

	var found string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip directories we can't read
		}
		if d.IsDir() {
			return nil
		}
		if manifestNames[strings.ToLower(d.Name())] {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("searching for agent manifest: %w", err)
	}

	return found, nil
}

// detectLocalManifest checks only the immediate directory for an agent manifest file.
// Returns the path to the found manifest (preferring agent.manifest.yaml over agent.yaml,
// then .yml variants), or an empty string if none contain valid manifest content.
// Returns a non-nil error for unexpected I/O failures (e.g. permission errors).
func detectLocalManifest(dir string) (string, error) {
	candidates := []string{
		"agent.manifest.yaml",
		"agent.yaml",
		"agent.manifest.yml",
		"agent.yml",
	}

	for _, name := range candidates {
		candidate := filepath.Join(dir, name)
		_, err := os.Stat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("checking for manifest %s: %w", candidate, err)
		}
		if isValidManifestFile(candidate) {
			return candidate, nil
		}
	}
	return "", nil
}

// isValidManifestFile reads the file and checks whether it can be loaded as
// a valid AgentManifest via LoadAndValidateAgentManifest.
func isValidManifestFile(path string) bool {
	//nolint:gosec // path comes from a known filename in a user-controlled directory
	content, err := os.ReadFile(path)
	if err != nil {
		return false
	}

	_, err = agent_yaml.LoadAndValidateAgentManifest(content)
	return err == nil
}
