// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

const agentTemplatesURL = "https://aka.ms/foundry-agents"

// Template type constants
const (
	// TemplateTypeAgent is a template that points to an agent.yaml manifest file.
	TemplateTypeAgent = "agent"

	// TemplateTypeAzd is a full azd template repository.
	TemplateTypeAzd = "azd"
)

// AgentTemplate represents an agent template entry from the remote JSON catalog.
type AgentTemplate struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Language    string   `json:"language"`
	Framework   string   `json:"framework"`
	Source      string   `json:"source"`
	Tags        []string `json:"tags"`
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
// Returns initModeFromCode or initModeTemplate.
func promptInitMode(ctx context.Context, azdClient *azdext.AzdClient) (string, error) {
	choices := []*azdext.SelectChoice{
		{Label: "Use the code in the current directory", Value: initModeFromCode},
		{Label: "Start new from a template", Value: initModeTemplate},
	}

	resp, err := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message: "How do you want to initialize your agent?",
			Choices: choices,
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

// fetchAgentTemplates retrieves the agent template catalog from the remote JSON URL.
func fetchAgentTemplates(ctx context.Context, httpClient *http.Client) ([]AgentTemplate, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, agentTemplatesURL, nil)
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

	var templates []AgentTemplate
	if err := json.Unmarshal(body, &templates); err != nil {
		return nil, fmt.Errorf("failed to parse agent templates: %w", err)
	}

	return templates, nil
}

// promptAgentTemplate guides the user through language selection and template selection.
// Returns the selected AgentTemplate. The caller should check EffectiveType() to determine
// whether to use the agent.yaml manifest flow or the full azd template flow.
func promptAgentTemplate(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	httpClient *http.Client,
) (*AgentTemplate, error) {
	fmt.Println("Retrieving agent templates...")

	templates, err := fetchAgentTemplates(ctx, httpClient)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve agent templates: %w", err)
	}

	if len(templates) == 0 {
		return nil, fmt.Errorf("no agent templates available")
	}

	// Prompt for language
	languageChoices := []*azdext.SelectChoice{
		{Label: "Python", Value: "python"},
		{Label: "C#", Value: "csharp"},
	}

	langResp, err := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message: "Select a language:",
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

	// Filter templates by selected language
	var filtered []AgentTemplate
	for _, t := range templates {
		if t.Language == selectedLanguage {
			filtered = append(filtered, t)
		}
	}

	if len(filtered) == 0 {
		return nil, fmt.Errorf("no agent templates available for %s", languageChoices[*langResp.Value].Label)
	}

	// Build template choices with framework in label
	templateChoices := make([]*azdext.SelectChoice, len(filtered))
	for i, t := range filtered {
		label := fmt.Sprintf("%s (%s)", t.Title, t.Framework)
		templateChoices[i] = &azdext.SelectChoice{
			Label: label,
			Value: fmt.Sprintf("%d", i),
		}
	}

	templateResp, err := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message: "Select an agent template:",
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
