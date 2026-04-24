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
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

const agentTemplatesURL = "https://aka.ms/foundry-agents"

// Template type constants
const (
	// TemplateTypeAgent is a template that points to an agent.yaml manifest file.
	TemplateTypeAgent = "agent"

	// TemplateTypeAzd is a full azd template repository.
	TemplateTypeAzd = "azd"

	// templateTypeExtensionAIAgent is the discriminator value in the unified
	// awesome-azd templates.json manifest that identifies an agent-init
	// template. Entries with any other (or empty) templateType belong to the
	// standard awesome-azd gallery and are filtered out.
	templateTypeExtensionAIAgent = "extension.ai.agent"
)

// AgentTemplate represents an agent template entry from the remote JSON catalog.
// Field names mirror the awesome-azd templates.json schema.
type AgentTemplate struct {
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	Languages          []string `json:"languages"`
	ExtensionFramework string   `json:"extensionFramework"`
	Source             string   `json:"source"`
	Tags               []string `json:"extensionTags"`
	TemplateType       string   `json:"templateType"`
}

// EffectiveType determines the template type by inspecting the source URL.
// If it ends with agent.yaml or agent.manifest.yaml, it's an agent manifest.
// Otherwise, it's treated as a full azd template repo.
func (t *AgentTemplate) EffectiveType() string {
	lower := strings.ToLower(t.Source)
	if strings.HasSuffix(lower, "/agent.yaml") ||
		strings.HasSuffix(lower, "/agent.manifest.yaml") ||
		lower == "agent.yaml" ||
		lower == "agent.manifest.yaml" {
		return TemplateTypeAgent
	}
	return TemplateTypeAzd
}

const (
	initModeFromCode = "from_code"
	initModeTemplate = "template"
)

// promptInitMode asks the user whether to use existing code or start from a template.
// If the current directory is empty, automatically returns initModeTemplate.
// Returns initModeFromCode or initModeTemplate.
func promptInitMode(ctx context.Context, azdClient *azdext.AzdClient) (string, error) {
	empty, err := dirIsEmpty(".")
	if err != nil {
		return "", fmt.Errorf("checking current directory: %w", err)
	}

	if empty {
		return initModeTemplate, nil
	}

	choices := []*azdext.SelectChoice{
		{Label: "Use the code in the current directory", Value: initModeFromCode},
		{Label: "Start new from a template", Value: initModeTemplate},
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
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}

	return len(entries) == 0, nil
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

	//nolint:gosec // URL is supplied by the caller from a trusted constant or a test server
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

	return filtered, nil
}

// promptAgentTemplate guides the user through language selection and template selection.
// Returns the selected AgentTemplate. The caller should check EffectiveType() to determine
// whether to use the agent.yaml manifest flow or the full azd template flow.
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
			"use 'azd ai agent init -m <manifest>' to initialize from a template non-interactively",
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
	var filtered []AgentTemplate
	for _, t := range templates {
		if slices.Contains(t.Languages, selectedLanguage) {
			filtered = append(filtered, t)
		}
	}

	if len(filtered) == 0 {
		return nil, fmt.Errorf("no agent templates available for %s", languageChoices[*langResp.Value].Label)
	}

	// Sort templates alphabetically by title
	slices.SortFunc(filtered, func(a, b AgentTemplate) int {
		return strings.Compare(a.Title, b.Title)
	})

	// Build template choices with framework in label
	templateChoices := make([]*azdext.SelectChoice, len(filtered))
	for i, t := range filtered {
		label := fmt.Sprintf("%s (%s)", t.Title, t.ExtensionFramework)
		templateChoices[i] = &azdext.SelectChoice{
			Label: label,
			Value: fmt.Sprintf("%d", i),
		}
	}

	templateResp, err := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message: "Select an agent template",
			Choices: templateChoices,
		},
	})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return nil, exterrors.Cancelled("template selection was cancelled")
		}
		return nil, fmt.Errorf("failed to prompt for template: %w", err)
	}

	selectedTemplate := filtered[*templateResp.Value]
	return &selectedTemplate, nil
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
