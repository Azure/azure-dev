// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"slices"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/fatih/color"
)

const agentTemplatesURL = "https://aka.ms/foundry-agents-samples"

// Template type constants
const (
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

// EffectiveType determines the supported template type from the source and
// declared templateType. An empty result means the source is unsupported.
//
// Unified azure.yaml sources must be explicitly declared as agent templates.
// Non-file sources remain full azd repositories. YAML files other than a
// correctly declared azure.yaml are rejected rather than falling through to
// the repository flow.
func (t *AgentTemplate) EffectiveType() string {
	source := strings.TrimSpace(t.Source)
	if source == "" {
		return ""
	}

	sourcePath := source
	if parsed, err := url.Parse(source); err == nil && parsed.Path != "" {
		sourcePath = parsed.Path
	} else if delimiter := strings.IndexAny(sourcePath, "?#"); delimiter >= 0 {
		sourcePath = sourcePath[:delimiter]
	}

	sourcePath = strings.TrimSuffix(strings.ReplaceAll(sourcePath, `\`, "/"), "/")
	filename := strings.ToLower(path.Base(sourcePath))
	switch filename {
	case "azure.yaml", "azure.yml":
		if t.TemplateType == templateTypeExtensionAIAgent {
			return TemplateTypeAzureYaml
		}
		return ""
	case "agent.yaml", "agent.yml", "agent.manifest.yaml", "agent.manifest.yml":
		return ""
	default:
		if strings.HasSuffix(filename, ".yaml") || strings.HasSuffix(filename, ".yml") {
			return ""
		}
		return TemplateTypeAzd
	}
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
type agentKindChoice string

const (
	// AgentKindChoiceHosted is the existing hosted-agent path — the customer
	// supplies code or a container image and the platform runs it on Azure
	// Container Apps.
	AgentKindChoiceHosted agentKindChoice = "hosted"
	// AgentKindChoicePrompt is the prompt agent path — the customer declares
	// model + instructions and Foundry runs the agent. The generated service
	// definition uses kind: prompt. Whether it also names
	// a `harness:` is decided separately, by --harness or the kind menu entry.
	AgentKindChoicePrompt agentKindChoice = "prompt"
)

// harnessNone is the --harness value that explicitly opts out of a harness,
// letting `--harness none` degrade a harnessed template to a plain prompt agent.
const harnessNone = "none"

// resolveInitHarness resolves the harness written to the generated definition.
// An explicit --harness value always wins over impliedHarness — the harness the
// context already suggests, whether that is the menu entry the user picked or
// an adopted service's `harness:` block. Both are validated the same way,
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

	return "", exterrors.Validation(
		exterrors.CodeInvalidParameter,
		fmt.Sprintf("unknown --harness value %q", requested),
		fmt.Sprintf("supported values are: %s, %s", agent_api.ManagedAgentHarnessGitHubCopilot, harnessNone),
	)
}

// warnPromptAgentPreview tells the user that prompt-agent support in azd is
// still in preview. It is called from the single place every prompt-agent init
// funnels through, so the notice reaches both flag-driven runs (--kind prompt)
// and manifest-driven ones.
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

// voiceInitChoice is the interactive menu entry for creating a prompt voice agent.
var voiceInitChoice = &azdext.SelectChoice{
	Label: "Create a prompt voice agent",
	Value: initModeVoice,
}

// promptInitMode asks the user whether to use existing code, start from a
// template, or create a prompt voice agent.
// If the current directory is empty, the "use existing code" option is omitted
// (there is no code to use).
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
	choices = append(choices, voiceInitChoice)

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
	displayURL := catalogURLForDisplay(url)
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

	// Keep only supported agent-init entries. The shared templates.json
	// manifest also carries the awesome-azd gallery, and older catalogs may
	// still contain legacy agent manifest sources. Neither should surface.
	filtered := make([]AgentTemplate, 0, len(all))
	agentEntries := 0
	unsupportedSources := 0
	for _, t := range all {
		if t.TemplateType != templateTypeExtensionAIAgent {
			continue
		}
		agentEntries++
		if t.EffectiveType() == "" {
			unsupportedSources++
			continue
		}
		filtered = append(filtered, t)
	}

	// Emit counts without catalog source values, which may contain credentials.
	log.Printf(
		"agent templates manifest: accepted %d templateType=%q entries; rejected %d unsupported sources",
		len(filtered), templateTypeExtensionAIAgent, unsupportedSources,
	)

	if len(all) > 0 && agentEntries == 0 {
		return nil, fmt.Errorf(
			"agent templates manifest at %s contained %d entries but none had templateType=%q",
			displayURL, len(all), templateTypeExtensionAIAgent,
		)
	}
	if agentEntries > 0 && len(filtered) == 0 {
		return nil, fmt.Errorf(
			"agent templates manifest at %s contained %d templateType=%q entries but none used a supported "+
				"azure.yaml or full repository source",
			displayURL, agentEntries, templateTypeExtensionAIAgent,
		)
	}

	return filtered, nil
}

func catalogURLForDisplay(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "<catalog URL>"
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	parsed.RawFragment = ""
	return parsed.String()
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
// whether to use the unified azure.yaml flow or the full azd template flow.
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
