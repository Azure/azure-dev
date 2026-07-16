// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"azureaiagent/internal/cmd/nextstep"
	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/github"
	"github.com/fatih/color"
	"google.golang.org/protobuf/types/known/structpb"
	"gopkg.in/yaml.v3"
)

// foundryServiceHosts are the azure.yaml service `host` values that identify a
// unified Microsoft Foundry project manifest. The legacy `microsoft.foundry`
// host is included for backward compatibility with older non-split files.
var foundryServiceHosts = map[string]struct{}{
	"azure.ai.agent":      {},
	"azure.ai.project":    {},
	"azure.ai.connection": {},
	"azure.ai.toolbox":    {},
	"microsoft.foundry":   {},
}

// looksLikeFoundryAzureYaml reports whether the given YAML content is a unified
// Foundry `azure.yaml` project manifest rather than an agent manifest.
//
// It returns true when the document has a top-level `services:` map in which at
// least one service declares a Foundry `host:`. Agent manifests have a top-level
// `template:` and no `services:`, so they never match. This lets `azd ai agent
// init -m <pointer>` route a unified `azure.yaml` to the adoption path and an
// agent manifest to the legacy generate path unambiguously.
func looksLikeFoundryAzureYaml(content []byte) bool {
	var top map[string]any
	if err := yaml.Unmarshal(content, &top); err != nil {
		return false
	}

	services, ok := top["services"].(map[string]any)
	if !ok {
		return false
	}

	for _, svc := range services {
		svcMap, ok := svc.(map[string]any)
		if !ok {
			continue
		}
		host, ok := svcMap["host"].(string)
		if !ok {
			continue
		}
		if _, isFoundry := foundryServiceHosts[host]; isFoundry {
			return true
		}
	}

	return false
}

// foundryProjectName returns the top-level `name:` of a unified azure.yaml, used
// to derive the project folder name. Returns "" when the name is absent or the
// content cannot be parsed.
func foundryProjectName(content []byte) string {
	var top map[string]any
	if err := yaml.Unmarshal(content, &top); err != nil {
		return ""
	}
	if name, ok := top["name"].(string); ok {
		return strings.TrimSpace(name)
	}
	return ""
}

// foundryDeploymentEntry holds a parsed deployment along with the service key
// it was declared in, so the azure.yaml can be updated after verification.
type foundryDeploymentEntry struct {
	ServiceName string
	Deployment  project.Deployment
}

// azureYamlServices is the minimal typed structure for parsing deployments from
// a unified azure.yaml. Only the fields needed for deployment verification are
// declared; yaml.v3 ignores unrecognized keys.
type azureYamlServices struct {
	Services map[string]azureYamlService `yaml:"services"`
}

type azureYamlService struct {
	Host        string               `yaml:"host"`
	Deployments []project.Deployment `yaml:"deployments"`
}

// foundryDeployments parses the azure.yaml content and returns all model
// deployments declared under services with `host: azure.ai.project`.
func foundryDeployments(content []byte) []foundryDeploymentEntry {
	var doc azureYamlServices
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return nil
	}

	var entries []foundryDeploymentEntry
	for svcName, svc := range doc.Services {
		if svc.Host != "azure.ai.project" {
			continue
		}
		for _, dep := range svc.Deployments {
			entries = append(entries, foundryDeploymentEntry{
				ServiceName: svcName,
				Deployment:  dep,
			})
		}
	}
	return entries
}

func foundryProjectServiceForModelOverride(content []byte, flagName string) (string, error) {
	if flagName == "" {
		flagName = "--model"
	}
	var doc azureYamlServices
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return "", exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			"sample azure.yaml could not be parsed",
			"fix the sample manifest and run init again",
		)
	}
	serviceNames := make([]string, 0, len(doc.Services))
	for svcName, svc := range doc.Services {
		if svc.Host == "azure.ai.project" {
			serviceNames = append(serviceNames, svcName)
		}
	}
	slices.Sort(serviceNames)
	switch len(serviceNames) {
	case 0:
		return "", exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			"sample azure.yaml does not declare an azure.ai.project service for model deployment",
			fmt.Sprintf("add an azure.ai.project service or omit %s", flagName),
		)
	case 1:
		return serviceNames[0], nil
	default:
		return "", exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("%s is ambiguous: sample declares %d project services (%s)",
				flagName, len(serviceNames), strings.Join(serviceNames, ", ")),
			fmt.Sprintf("remove %s, or edit azure.yaml to add the deployment to the intended project service", flagName),
		)
	}
}

func agentServiceNames(content []byte) []string {
	var doc azureYamlServices
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return nil
	}
	var names []string
	for svcName, svc := range doc.Services {
		if svc.Host == "azure.ai.agent" {
			names = append(names, svcName)
		}
	}
	slices.Sort(names)
	return names
}

func updateAzureYamlAgentName(ctx context.Context, azdClient *azdext.AzdClient, serviceName, agentName string) error {
	val, err := structpb.NewValue(agentName)
	if err != nil {
		return fmt.Errorf("encoding agent name for service %q: %w", serviceName, err)
	}
	if _, err := azdClient.Project().SetServiceConfigValue(ctx, &azdext.SetServiceConfigValueRequest{
		ServiceName: serviceName,
		Path:        "name",
		Value:       val,
	}); err != nil {
		return fmt.Errorf("updating agent name in azure.yaml for service %q: %w", serviceName, err)
	}
	return nil
}

func updateAzureYamlAgentProtocols(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	serviceName string,
	protocols []protocolInfo,
) error {
	protocolDocs := make([]any, 0, len(protocols))
	for _, p := range protocols {
		protocolDocs = append(protocolDocs, map[string]any{"protocol": p.Name, "version": p.Version})
	}
	val, err := structpb.NewValue(protocolDocs)
	if err != nil {
		return fmt.Errorf("encoding protocols for service %q: %w", serviceName, err)
	}
	if _, err := azdClient.Project().SetServiceConfigValue(ctx, &azdext.SetServiceConfigValueRequest{
		ServiceName: serviceName,
		Path:        "protocols",
		Value:       val,
	}); err != nil {
		return fmt.Errorf("updating protocols in azure.yaml for service %q: %w", serviceName, err)
	}
	return nil
}

func agentNameOverrideServices(content []byte, agentName string) ([]string, error) {
	if agentName == "" {
		return nil, nil
	}
	if _, err := validateInitAgentName(agentName); err != nil {
		return nil, err
	}
	agentServices := agentServiceNames(content)
	if len(agentServices) > 1 {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("--agent-name is ambiguous: sample declares %d agent services (%s)",
				len(agentServices), strings.Join(agentServices, ", ")),
			"remove --agent-name, or edit azure.yaml to rename each agent individually",
		)
	}
	return agentServices, nil
}

func agentOverrideServices(content []byte, flagName string) ([]string, error) {
	agentServices := agentServiceNames(content)
	if len(agentServices) > 1 {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("%s is ambiguous: sample declares %d agent services (%s)",
				flagName, len(agentServices), strings.Join(agentServices, ", ")),
			fmt.Sprintf("remove %s, or edit azure.yaml to update each agent individually", flagName),
		)
	}
	return agentServices, nil
}

func resolveDeploymentForModelFlag(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	azureContext *azdext.AzureContext,
	modelName string,
) (*project.Deployment, error) {
	if modelName == "" {
		return nil, nil
	}
	deployment, err := resolveModelDeployment(
		ctx, azdClient, azureContext, &azdext.AiModel{Name: modelName}, azureContext.Scope.Location,
	)
	if err != nil {
		return nil, err
	}
	return &project.Deployment{
		Name: deployment.ModelName,
		Model: project.DeploymentModel{
			Name:    deployment.ModelName,
			Format:  deployment.Format,
			Version: deployment.Version,
		},
		Sku: project.DeploymentSku{
			Name:     deployment.Sku.Name,
			Capacity: int(deployment.Capacity),
		},
	}, nil
}

// verifyAzureYamlDeployments checks each model deployment declared in the
// unified azure.yaml against the selected Foundry project's existing
// deployments. It prompts the user for each deployment and returns the filtered
// list of deployments that should remain in the azure.yaml (i.e. those that
// need provisioning) and the full list of referenced deployments (for env var).
func verifyAzureYamlDeployments(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	credential azcore.TokenCredential,
	azureContext *azdext.AzureContext,
	envName string,
	entries []foundryDeploymentEntry,
	noPrompt bool,
	modelDeploymentFlag string,
	modelFlag string,
) (keptEntries []foundryDeploymentEntry, referencedDeployments []project.Deployment, modified bool, err error) {
	// Get the Foundry project ID from the environment.
	resp, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: envName,
		Key:     "AZURE_AI_PROJECT_ID",
	})
	if err != nil {
		return nil, nil, false, fmt.Errorf("failed to get AZURE_AI_PROJECT_ID: %w", err)
	}

	var allDeployments []FoundryDeploymentInfo
	foundryProjectId := resp.Value
	if foundryProjectId != "" {
		parts := strings.Split(foundryProjectId, "/")
		if len(parts) < 9 {
			return nil, nil, false, fmt.Errorf(
				"invalid AZURE_AI_PROJECT_ID format: expected at least 9 path segments, got %d", len(parts))
		}

		subscription := parts[2]
		resourceGroup := parts[4]
		accountName := parts[8]

		allDeployments, err = listProjectDeployments(ctx, credential, subscription, resourceGroup, accountName)
		if err != nil {
			return nil, nil, false, fmt.Errorf("failed to list deployments in Foundry project: %w", err)
		}
	}

	// --model-deployment flag: auto-select the named deployment, skip interactive loop.
	if modelDeploymentFlag != "" {
		for _, d := range allDeployments {
			if strings.EqualFold(d.Name, modelDeploymentFlag) {
				log.Printf("--model-deployment: using existing deployment '%s' (model: %s, version: %s)",
					d.Name, d.ModelName, d.Version)
				referencedDeployments = append(referencedDeployments, project.Deployment{
					Name: d.Name,
					Model: project.DeploymentModel{
						Name:    d.ModelName,
						Format:  d.ModelFormat,
						Version: d.Version,
					},
					Sku: project.DeploymentSku{
						Name:     d.SkuName,
						Capacity: d.SkuCapacity,
					},
				})
				// All azure.yaml deployments are removed (existing deployment is used instead).
				return nil, referencedDeployments, true, nil
			}
		}
		return nil, nil, false, exterrors.Validation(
			exterrors.CodeModelDeploymentNotFound,
			fmt.Sprintf("model deployment %q not found in Foundry project", modelDeploymentFlag),
			"verify the deployment name or omit --model-deployment to select interactively",
		)
	}

	for _, entry := range entries {
		dep := entry.Deployment

		// Find matching deployments by model name.
		matchingDeployments := make(map[string]*FoundryDeploymentInfo)
		for i := range allDeployments {
			d := &allDeployments[i]
			if d.ModelName == dep.Model.Name {
				matchingDeployments[d.Name] = d
			}
		}

		if len(matchingDeployments) > 0 {
			// Sort for deterministic selection.
			sortedNames := make([]string, 0, len(matchingDeployments))
			for name := range matchingDeployments {
				sortedNames = append(sortedNames, name)
			}
			slices.Sort(sortedNames)

			if noPrompt {
				// Auto-use the first matching deployment.
				name := sortedNames[0]
				existing := matchingDeployments[name]
				log.Printf(
					"--no-prompt: using existing deployment '%s' (version: %s) for model '%s'",
					name, existing.Version, dep.Model.Name,
				)
				referencedDeployments = append(referencedDeployments, project.Deployment{
					Name: name,
					Model: project.DeploymentModel{
						Name:    dep.Model.Name,
						Format:  existing.ModelFormat,
						Version: existing.Version,
					},
					Sku: project.DeploymentSku{
						Name:     existing.SkuName,
						Capacity: existing.SkuCapacity,
					},
				})
				modified = true
				continue
			}

			// Show deployment details and prompt.
			fmt.Printf("\nModel deployment %s is defined in the azure.yaml:\n", output.WithHighLightFormat("'%s'", dep.Name))
			fmt.Printf("  Model: %s (%s), version %s\n", dep.Model.Name, dep.Model.Format, dep.Model.Version)
			fmt.Printf("  SKU: %s, capacity %d\n", dep.Sku.Name, dep.Sku.Capacity)
			fmt.Println()

			fmt.Println("Existing deployment(s) using the same model were found in your Foundry project:")
			for _, name := range sortedNames {
				d := matchingDeployments[name]
				fmt.Printf("  • %s — version %s, SKU: %s (capacity %d)\n",
					name, d.Version, d.SkuName, d.SkuCapacity)
			}
			fmt.Println()

			// Build prompt choices: use each existing + optionally deploy as specified + choose different + skip
			choices := make([]*azdext.SelectChoice, 0, len(sortedNames)+3)
			for _, name := range sortedNames {
				d := matchingDeployments[name]
				choices = append(choices, &azdext.SelectChoice{
					Value: "use:" + name,
					Label: fmt.Sprintf("Use existing deployment '%s' (version: %s, SKU: %s)",
						name, d.Version, d.SkuName),
				})
			}
			// Only offer "deploy as specified" if no existing deployment is an exact match.
			hasExactMatch := false
			for _, d := range matchingDeployments {
				if d.Name == dep.Name &&
					d.Version == dep.Model.Version &&
					d.SkuName == dep.Sku.Name &&
					d.SkuCapacity == dep.Sku.Capacity {
					hasExactMatch = true
					break
				}
			}
			if !hasExactMatch {
				choices = append(choices, &azdext.SelectChoice{
					Value: "deploy",
					Label: "Deploy as specified in azure.yaml",
				})
			}
			choices = append(choices,
				&azdext.SelectChoice{
					Value: "change",
					Label: "Choose a different model",
				},
				&azdext.SelectChoice{
					Value: "skip",
					Label: "Skip this model entirely (remove from azure.yaml)",
				},
			)

			defaultIdx := int32(0)
			selectResp, err := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
				Options: &azdext.SelectOptions{
					Message:       "How would you like to proceed?",
					Choices:       choices,
					SelectedIndex: &defaultIdx,
				},
			})
			if err != nil {
				if exterrors.IsCancellation(err) {
					return nil, nil, false, exterrors.Cancelled("model deployment verification was cancelled")
				}
				return nil, nil, false, fmt.Errorf("failed to prompt for deployment choice: %w", err)
			}

			selected := choices[*selectResp.Value].Value
			switch {
			case strings.HasPrefix(selected, "use:"):
				name := strings.TrimPrefix(selected, "use:")
				existing := matchingDeployments[name]
				referencedDeployments = append(referencedDeployments, project.Deployment{
					Name: name,
					Model: project.DeploymentModel{
						Name:    dep.Model.Name,
						Format:  existing.ModelFormat,
						Version: existing.Version,
					},
					Sku: project.DeploymentSku{
						Name:     existing.SkuName,
						Capacity: existing.SkuCapacity,
					},
				})
				modified = true
				fmt.Printf("Using existing deployment '%s'.\n", name)

			case selected == "deploy":
				keptEntries = append(keptEntries, foundryDeploymentEntry{
					ServiceName: entry.ServiceName,
					Deployment:  dep,
				})
				referencedDeployments = append(referencedDeployments, dep)

			case selected == "change":
				newDep, isExisting, err := promptAlternativeDeployment(ctx, azdClient, azureContext, allDeployments, modelFlag)
				if err != nil {
					return nil, nil, false, err
				}
				if newDep != nil {
					if !isExisting {
						keptEntries = append(keptEntries, foundryDeploymentEntry{
							ServiceName: entry.ServiceName,
							Deployment:  *newDep,
						})
					}
					referencedDeployments = append(referencedDeployments, *newDep)
				}
				modified = true

			case selected == "skip":
				modified = true
				fmt.Println(output.WithWarningFormat(
					"Skipped model '%s'. It will be removed from the azure.yaml.", dep.Model.Name))
			}

		} else {
			// No matching deployment in the project (or no project yet).
			if noPrompt {
				// Auto-deploy as specified.
				log.Printf("--no-prompt: no matching deployment for model '%s', will deploy as specified",
					dep.Model.Name)
				keptEntries = append(keptEntries, foundryDeploymentEntry{
					ServiceName: entry.ServiceName,
					Deployment:  dep,
				})
				referencedDeployments = append(referencedDeployments, dep)
				continue
			}

			if foundryProjectId == "" {
				fmt.Printf("\nModel deployment %s is defined in the azure.yaml:\n",
					output.WithHighLightFormat("'%s'", dep.Name))
			} else {
				color.Yellow(
					"\nNo existing deployment for model '%s' was found in your Foundry project.\n",
					dep.Model.Name,
				)
				fmt.Printf("Model deployment %s is defined in the azure.yaml:\n",
					output.WithHighLightFormat("'%s'", dep.Name))
			}
			fmt.Printf("  Model: %s (%s), version %s\n", dep.Model.Name, dep.Model.Format, dep.Model.Version)
			fmt.Printf("  SKU: %s, capacity %d\n\n", dep.Sku.Name, dep.Sku.Capacity)

			noMatchChoices := []*azdext.SelectChoice{
				{Value: "deploy", Label: "Deploy as specified in azure.yaml"},
				{Value: "change", Label: "Choose a different model"},
				{Value: "skip", Label: "Skip this model entirely (remove from azure.yaml)"},
			}

			defaultIdx := int32(0)
			selectResp, err := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
				Options: &azdext.SelectOptions{
					Message:       "How would you like to proceed?",
					Choices:       noMatchChoices,
					SelectedIndex: &defaultIdx,
				},
			})
			if err != nil {
				if exterrors.IsCancellation(err) {
					return nil, nil, false, exterrors.Cancelled("model deployment verification was cancelled")
				}
				return nil, nil, false, fmt.Errorf("failed to prompt for deployment choice: %w", err)
			}

			switch noMatchChoices[*selectResp.Value].Value {
			case "deploy":
				keptEntries = append(keptEntries, foundryDeploymentEntry{
					ServiceName: entry.ServiceName,
					Deployment:  dep,
				})
				referencedDeployments = append(referencedDeployments, dep)

			case "change":
				newDep, isExisting, err := promptAlternativeDeployment(ctx, azdClient, azureContext, allDeployments, modelFlag)
				if err != nil {
					return nil, nil, false, err
				}
				if newDep != nil {
					if !isExisting {
						keptEntries = append(keptEntries, foundryDeploymentEntry{
							ServiceName: entry.ServiceName,
							Deployment:  *newDep,
						})
					}
					referencedDeployments = append(referencedDeployments, *newDep)
				}
				modified = true

			case "skip":
				modified = true
				fmt.Println(output.WithWarningFormat(
					"Skipped model '%s'. It will be removed from the azure.yaml.", dep.Model.Name))
			}
		}
	}

	return keptEntries, referencedDeployments, modified, nil
}

// promptAlternativeDeployment lets the user browse the model catalog or pick an
// existing deployment from the project. It returns the chosen deployment, or nil
// if no selection was made. The isExisting flag indicates whether the user picked
// an already-deployed model (true) or a new one from the catalog (false).
func promptAlternativeDeployment(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	azureContext *azdext.AzureContext,
	allDeployments []FoundryDeploymentInfo,
	modelFlag string,
) (dep *project.Deployment, isExisting bool, err error) {
	// Determine whether to prompt for catalog vs existing, or skip straight to catalog.
	useCatalog := true
	if len(allDeployments) > 0 {
		altChoices := []*azdext.SelectChoice{
			{Value: "catalog", Label: "Browse the model catalog"},
			{Value: "existing", Label: "Use an existing deployment from this project"},
		}

		defaultIdx := int32(0)
		altResp, err := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
			Options: &azdext.SelectOptions{
				Message:       "How would you like to choose a model?",
				Choices:       altChoices,
				SelectedIndex: &defaultIdx,
			},
		})
		if err != nil {
			if exterrors.IsCancellation(err) {
				return nil, false, exterrors.Cancelled("model selection was cancelled")
			}
			return nil, false, fmt.Errorf("failed to prompt for alternative model choice: %w", err)
		}
		useCatalog = altChoices[*altResp.Value].Value == "catalog"
	}

	if useCatalog {
		// Use the full model + deployment prompt which handles version,
		// SKU, and capacity selection (same as manifest path).
		defaultModel := "gpt-4.1-mini"
		if modelFlag != "" {
			defaultModel = modelFlag
		}
		promptReq := &azdext.PromptAiModelRequest{
			AzureContext: azureContext,
			Filter:       agentModelFilter([]string{azureContext.Scope.Location}, nil),
			SelectOptions: &azdext.SelectOptions{
				Message: "Select a model",
			},
			DefaultValue: defaultModel,
		}

		modelResp, err := azdClient.Prompt().PromptAiModel(ctx, promptReq)
		if err != nil {
			if exterrors.IsCancellation(err) {
				return nil, false, exterrors.Cancelled("model selection was cancelled")
			}
			return nil, false, fmt.Errorf("failed to prompt for model selection: %w", err)
		}

		model := modelResp.Model

		var defaultCap int32 = 50
		deploymentResp, err := azdClient.Prompt().PromptAiDeployment(ctx, &azdext.PromptAiDeploymentRequest{
			AzureContext: azureContext,
			ModelName:    model.Name,
			Options: &azdext.AiModelDeploymentOptions{
				Locations: []string{azureContext.Scope.Location},
				Capacity:  &defaultCap,
			},
			Quota: &azdext.QuotaCheckOptions{
				MinRemainingCapacity: 1,
			},
		})
		if err != nil {
			if exterrors.IsCancellation(err) {
				return nil, false, exterrors.Cancelled("deployment configuration was cancelled")
			}
			return nil, false, fmt.Errorf("failed to prompt for deployment details: %w", err)
		}

		d := deploymentResp.Deployment
		skuName := "GlobalStandard"
		if d.Sku != nil && d.Sku.Name != "" {
			skuName = d.Sku.Name
		}

		return &project.Deployment{
			Name: d.ModelName,
			Model: project.DeploymentModel{
				Name:    d.ModelName,
				Format:  d.Format,
				Version: d.Version,
			},
			Sku: project.DeploymentSku{
				Name:     skuName,
				Capacity: int(d.Capacity),
			},
		}, false, nil
	}

	// Let user pick from all deployments in the project.
	type labeledDep struct {
		label string
		info  *FoundryDeploymentInfo
	}
	items := make([]labeledDep, 0, len(allDeployments))
	for i := range allDeployments {
		d := &allDeployments[i]
		items = append(items, labeledDep{
			label: fmt.Sprintf("%s (%s, version %s)", d.Name, d.ModelName, d.Version),
			info:  d,
		})
	}
	slices.SortFunc(items, func(a, b labeledDep) int {
		return strings.Compare(a.label, b.label)
	})

	choices := make([]*azdext.SelectChoice, len(items))
	for i, item := range items {
		choices[i] = &azdext.SelectChoice{
			Value: item.label,
			Label: item.label,
		}
	}

	defaultIdx := int32(0)
	selResp, err := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message:       "Select a deployment",
			Choices:       choices,
			SelectedIndex: &defaultIdx,
		},
	})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return nil, false, exterrors.Cancelled("deployment selection was cancelled")
		}
		return nil, false, fmt.Errorf("failed to select existing deployment: %w", err)
	}

	selected := items[*selResp.Value]
	d := selected.info
	return &project.Deployment{
		Name: d.Name,
		Model: project.DeploymentModel{
			Name:    d.ModelName,
			Format:  d.ModelFormat,
			Version: d.Version,
		},
		Sku: project.DeploymentSku{
			Name:     d.SkuName,
			Capacity: d.SkuCapacity,
		},
	}, true, nil
}

// updateAzureYamlDeployments writes the filtered deployment list back to the
// azure.yaml project service. Deployments the user chose to "use existing" or
// "skip" are excluded, leaving only those that need provisioning.
func updateAzureYamlDeployments(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	serviceName string,
	deployments []project.Deployment,
) error {
	// Convert deployments to a structpb-compatible value.
	depSlice := make([]any, 0, len(deployments))
	for _, d := range deployments {
		depSlice = append(depSlice, map[string]any{
			"name": d.Name,
			"model": map[string]any{
				"format":  d.Model.Format,
				"name":    d.Model.Name,
				"version": d.Model.Version,
			},
			"sku": map[string]any{
				"name":     d.Sku.Name,
				"capacity": d.Sku.Capacity,
			},
		})
	}

	val, err := structpb.NewValue(depSlice)
	if err != nil {
		return fmt.Errorf("encoding deployments for service %q: %w", serviceName, err)
	}

	if _, err := azdClient.Project().SetServiceConfigValue(ctx, &azdext.SetServiceConfigValueRequest{
		ServiceName: serviceName,
		Path:        "deployments",
		Value:       val,
	}); err != nil {
		return fmt.Errorf("updating deployments in azure.yaml for service %q: %w", serviceName, err)
	}

	return nil
}

// readManifestContentForInitDetection returns the pointed-at YAML content for
// init-mode routing. It first uses the cheap peek path; when that cannot read a
// GitHub URL (for example, a private repository), it falls back to the
// authenticated GitHub CLI download path so private unified azure.yaml samples
// can still be classified and adopted.
func readManifestContentForInitDetection(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	manifestPointer string,
	httpClient *http.Client,
) ([]byte, bool) {
	if content, ok := readManifestContentForPeek(ctx, manifestPointer, httpClient); ok {
		return content, true
	}
	if azdClient == nil || !strings.Contains(manifestPointer, "://") {
		return nil, false
	}

	parsedURL, err := url.Parse(manifestPointer)
	if err != nil || !strings.Contains(parsedURL.Hostname(), "github") {
		return nil, false
	}

	commandRunner := exec.NewCommandRunner(&exec.RunnerOptions{
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
	console := input.NewConsole(
		false, // noPrompt
		true,  // isTerminal
		input.Writers{Output: io.Discard},
		input.ConsoleHandles{
			Stderr: os.Stderr,
			Stdin:  os.Stdin,
			Stdout: os.Stdout,
		},
		nil, // formatter
		nil, // externalPromptCfg
	)
	ghCli := github.NewGitHubCli(console, commandRunner)
	if err := ghCli.EnsureInstalled(ctx); err != nil {
		log.Printf("detect unified azure.yaml: ensuring gh is installed: %v", err)
		return nil, false
	}

	urlInfo, err := parseGitHubUrlForAdopt(ctx, azdClient, manifestPointer)
	if err != nil {
		log.Printf("detect unified azure.yaml: parsing GitHub URL: %v", err)
		return nil, false
	}

	apiPath := fmt.Sprintf("/repos/%s/contents/%s", urlInfo.RepoSlug, urlInfo.FilePath)
	if urlInfo.Branch != "" {
		apiPath += fmt.Sprintf("?ref=%s", urlInfo.Branch)
	}
	content, err := downloadGithubManifest(ctx, urlInfo, apiPath, ghCli)
	if err != nil {
		log.Printf("detect unified azure.yaml: downloading GitHub file: %v", err)
		return nil, false
	}

	return []byte(content), true
}

// runInitFromAzureYaml adopts a sample's unified Foundry `azure.yaml` as the
// project-root manifest instead of generating one from an agent manifest
// (#8798). The sample's `azure.yaml` and the files it references are placed at
// the project root via azd-core's native template adoption; the services it
// already declares (project, connections, toolboxes, agents) are not
// re-derived. `content` is the already-fetched azure.yaml used to derive the
// project folder name.
func runInitFromAzureYaml(
	ctx context.Context,
	flags *initFlags,
	azdClient *azdext.AzdClient,
	httpClient *http.Client,
	content []byte,
) error {
	targetDir, folderDisplay := adoptTargetDir(flags, foundryProjectName(content))

	// Adoption is a fresh-project operation: it lays down the project-root
	// azure.yaml. When the target already contains an azd project manifest we
	// cannot adopt over it; merging the sample's services into an existing
	// azure.yaml is tracked separately (#8884).
	if projectManifestExists(targetDir) {
		return exterrors.Validation(
			exterrors.CodeConflictingArguments,
			fmt.Sprintf("a project azure.yaml already exists in %q, so the sample's "+
				"unified azure.yaml cannot be adopted there", targetDir),
			"run this command in an empty directory (or pass a new target directory) to "+
				"adopt the sample, or add an individual agent to this project with "+
				"'azd ai agent init -m <agent.manifest.yaml>'",
		)
	}
	if _, err := agentNameOverrideServices(content, flags.agentName); err != nil {
		return err
	}
	if len(flags.protocols) > 0 {
		if _, err := agentOverrideServices(content, "--protocol"); err != nil {
			return err
		}
		if _, err := resolveKnownProtocols(flags.protocols); err != nil {
			return err
		}
	}
	if flags.modelDeployment != "" {
		if _, err := foundryProjectServiceForModelOverride(content, "--model-deployment"); err != nil {
			return err
		}
	} else if flags.model != "" {
		if _, err := foundryProjectServiceForModelOverride(content, "--model"); err != nil {
			return err
		}
	}

	// Stage the sample as a local template directory (azure.yaml at its root
	// alongside referenced files) that azd-core can adopt with `azd init -t`.
	stagingDir, cleanup, err := stageAzureYamlTemplate(ctx, flags, azdClient, httpClient)
	if err != nil {
		return err
	}
	defer cleanup()

	fmt.Println(output.WithGrayFormat("Adopting the sample's azure.yaml as your project manifest..."))

	envName := deriveEnvName(flags, targetDir)
	if err := scaffoldProject(ctx, azdClient, targetDir, stagingDir, envName); err != nil {
		return err
	}

	// Defensive: the sample should already declare `infra.provider:
	// microsoft.foundry`, but stamp it if missing so provisioning stays
	// bicep-less by default.
	if err := ensureFoundryProviderDeclared(ctx, azdClient); err != nil {
		return err
	}
	if flags.agentName != "" {
		agentServices, err := agentNameOverrideServices(content, flags.agentName)
		if err != nil {
			return err
		}
		for _, agentServiceName := range agentServices {
			if err := updateAzureYamlAgentName(ctx, azdClient, agentServiceName, flags.agentName); err != nil {
				return err
			}
		}
	}
	if len(flags.protocols) > 0 {
		agentServices, err := agentOverrideServices(content, "--protocol")
		if err != nil {
			return err
		}
		protocols, err := resolveKnownProtocols(flags.protocols)
		if err != nil {
			return err
		}
		for _, agentServiceName := range agentServices {
			if err := updateAzureYamlAgentProtocols(ctx, azdClient, agentServiceName, protocols); err != nil {
				return err
			}
		}
	}

	// --- Interactive Azure context setup (subscription, Foundry project) ---
	// The scaffolding created an environment; load it and run the same Foundry
	// project selection flow as the agent-manifest path so the user ends up
	// with a provision-ready environment.
	env := getExistingEnvironment(ctx, envName, azdClient)
	if env == nil {
		// Environment should exist after scaffoldProject; if not, create one.
		env, err = createNewEnvironment(ctx, azdClient, envName)
		if err != nil {
			return err
		}
	}

	azureContext, err := loadAzureContext(ctx, azdClient, env.Name)
	if err != nil {
		return err
	}
	applyAzureContextFlags(azureContext, flags)
	if shouldDeferInitAzureContext(flags.noPrompt, azureContext) {
		if err := persistValidatedAzureContextFlags(ctx, azdClient, azureContext, env.Name, flags); err != nil {
			return err
		}
	}

	// Apply deploy-mode configuration to the adopted agent
	// service(s) before configuring the Foundry project. Whether an
	// Azure Container Registry must be wired (skipACR) depends on the
	// resolved deploy mode: a container agent on an existing project
	// needs AZURE_CONTAINER_REGISTRY_ENDPOINT set here, while a code
	// agent (or a user-supplied --image) does not.
	usesContainer, err := applyDeployModeToAdoptedProject(ctx, flags, azdClient)
	if err != nil {
		return err
	}

	// skipACR is false only for a container deploy whose registry azd
	// manages. Code deploy and --image (bring your own registry) both
	// skip ACR.
	skipACR := !usesContainer || flags.image != ""

	result, err := configureFoundryProject(
		ctx, azdClient, azureContext, env.Name,
		flags.projectResourceId, flags.noPrompt,
		skipACR,
	)
	if err != nil {
		if exterrors.IsCancellation(err) {
			return exterrors.Cancelled("initialization was cancelled")
		}
		return err
	}

	// When an existing project was selected, stamp its endpoint onto the
	// azure.ai.project service so the provisioning provider recognizes the
	// brownfield signal and reuses the project instead of creating a new one.
	if result.FoundryProject != nil {
		if err := stampProjectEndpoint(ctx, azdClient, result.FoundryProject); err != nil {
			return err
		}
	}

	// --- Model deployment verification ---
	// Parse deployments from the azure.yaml and verify them against the
	// selected Foundry project. If the user opts to use existing deployments
	// or skip, we update the on-disk azure.yaml accordingly.
	deploymentEntries := foundryDeployments(content)
	if flags.modelDeployment != "" {
		if result.FoundryProject == nil {
			return exterrors.Validation(
				exterrors.CodeInvalidParameter,
				"--model-deployment requires an existing Foundry project",
				"provide --project-id with --model-deployment, or use --model to provision a new deployment",
			)
		}
		if len(deploymentEntries) == 0 {
			serviceName, err := foundryProjectServiceForModelOverride(content, "--model-deployment")
			if err != nil {
				return err
			}
			deploymentEntries = []foundryDeploymentEntry{{
				ServiceName: serviceName,
				Deployment:  project.Deployment{Name: flags.modelDeployment},
			}}
		}
	} else if flags.model != "" {
		if result == nil || result.Credential == nil {
			return exterrors.Validation(
				exterrors.CodeInvalidParameter,
				"--model requires Azure subscription and location values during sample adoption",
				"pass both --subscription and --location, or run interactively to choose them",
			)
		}
		serviceName, err := foundryProjectServiceForModelOverride(content, "--model")
		if err != nil {
			return err
		}
		deployment, err := resolveDeploymentForModelFlag(ctx, azdClient, azureContext, flags.model)
		if err != nil {
			return err
		}
		if deployment != nil {
			if err := updateAzureYamlDeployments(ctx, azdClient, serviceName, []project.Deployment{*deployment}); err != nil {
				return err
			}
			deploymentEntries = []foundryDeploymentEntry{{ServiceName: serviceName, Deployment: *deployment}}
		}
	}
	if len(deploymentEntries) > 0 && result != nil && result.Credential != nil {
		keptEntries, referencedDeployments, deploymentsModified, err := verifyAzureYamlDeployments(
			ctx, azdClient, result.Credential, azureContext, env.Name,
			deploymentEntries, flags.noPrompt, flags.modelDeployment, flags.model,
		)
		if err != nil {
			if exterrors.IsCancellation(err) {
				return exterrors.Cancelled("initialization was cancelled")
			}
			return err
		}

		// Update the azure.yaml if deployments were modified.
		if deploymentsModified {
			// Group kept deployments by their originating service name.
			byService := make(map[string][]project.Deployment)
			for _, entry := range deploymentEntries {
				// Initialize to empty — ensures services with all removed get an empty list.
				if _, ok := byService[entry.ServiceName]; !ok {
					byService[entry.ServiceName] = nil
				}
			}
			for _, kept := range keptEntries {
				byService[kept.ServiceName] = append(byService[kept.ServiceName], kept.Deployment)
			}

			for svcName, deps := range byService {
				if err := updateAzureYamlDeployments(ctx, azdClient, svcName, deps); err != nil {
					return err
				}
			}
		}

		// Persist the first referenced deployment name as AZURE_AI_MODEL_DEPLOYMENT_NAME.
		setEnv := func(ctx context.Context, key, value string) error {
			return setEnvValue(ctx, azdClient, env.Name, key, value)
		}
		if err := persistFirstDeploymentName(ctx, setEnv, referencedDeployments); err != nil {
			return fmt.Errorf("failed to set AZURE_AI_MODEL_DEPLOYMENT_NAME: %w", err)
		}
	}

	fmt.Printf(
		"\nAdopted the sample's azure.yaml as the project manifest at %s.\n",
		output.WithHighLightFormat("azure.yaml"),
	)

	printAdoptionNextSteps(ctx, azdClient, folderDisplay)
	return nil
}

// adoptTargetDir resolves the directory the adopted project is created in and
// the display path for the "created folder" next-step hint. An explicit --src
// (or positional directory) wins; otherwise a new folder named after the
// sample's project name is used, falling back to the current directory when the
// sample has no name.
func adoptTargetDir(flags *initFlags, projectName string) (targetDir string, folderDisplay string) {
	if flags.src != "" {
		return flags.src, folderDisplayIfNew(flags.src)
	}
	if projectName == "" {
		return ".", ""
	}
	folder := sanitizeAgentName(projectName)
	if folder == "" {
		return ".", ""
	}
	return folder, folderDisplayIfNew(folder)
}

// folderDisplayIfNew returns a slash-formatted display path when dir does not
// yet exist (so the cd hint is only shown for newly-created folders), else "".
func folderDisplayIfNew(dir string) string {
	if dir == "." {
		return ""
	}
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return filepath.ToSlash(dir)
	}
	return ""
}

func projectManifestExists(dir string) bool {
	return fileExists(filepath.Join(dir, "azure.yaml")) ||
		fileExists(filepath.Join(dir, "azure.yml"))
}

// stageAzureYamlTemplate produces a local directory that azd-core can adopt as a
// template (`azd init -t <dir>`): it contains the sample's azure.yaml at its
// root alongside the sibling files/dirs the manifest references.
//
// For a local pointer the pointer's parent directory is used directly when the
// file is already named azure.yaml(.yml); otherwise a temp copy of the
// directory is staged with the manifest written as azure.yaml. For a remote
// GitHub pointer the azure.yaml's containing directory is downloaded into a temp
// staging dir. The returned cleanup removes any temp directory created.
func stageAzureYamlTemplate(
	ctx context.Context,
	flags *initFlags,
	azdClient *azdext.AzdClient,
	httpClient *http.Client,
) (string, func(), error) {
	noop := func() {}
	pointer := flags.manifestPointer

	if isLocalFilePath(pointer) {
		dir := filepath.Dir(pointer)
		base := strings.ToLower(filepath.Base(pointer))
		if base == "azure.yaml" {
			return dir, noop, nil
		}

		// The pointer file isn't named azure.yaml: stage a temp copy of the
		// directory and write the manifest as azure.yaml so azd-core adopts it.
		staging, err := os.MkdirTemp("", "azd-foundry-adopt-*")
		if err != nil {
			return "", noop, fmt.Errorf("creating staging dir: %w", err)
		}
		cleanup := func() { _ = os.RemoveAll(staging) }
		// Staging is all-or-nothing: without azure.yaml at the template root,
		// azd-core would generate a default manifest instead of adopting this
		// sample, so every error path removes the partial copy.
		if err := copyDirectory(dir, staging); err != nil {
			cleanup()
			return "", noop, fmt.Errorf("staging sample directory: %w", err)
		}
		//nolint:gosec // manifest path is an explicit user-provided local path
		data, err := os.ReadFile(pointer)
		if err != nil {
			cleanup()
			return "", noop, fmt.Errorf("reading sample azure.yaml: %w", err)
		}
		//nolint:gosec // staging dir is from os.MkdirTemp and the filename is a constant
		if err := os.WriteFile(filepath.Join(staging, "azure.yaml"), data, osutil.PermissionFile); err != nil {
			cleanup()
			return "", noop, fmt.Errorf("writing staged azure.yaml: %w", err)
		}
		if err := os.Remove(filepath.Join(staging, filepath.Base(pointer))); err != nil && !errors.Is(err, fs.ErrNotExist) {
			cleanup()
			return "", noop, fmt.Errorf("removing staged source manifest: %w", err)
		}
		return staging, cleanup, nil
	}

	// Remote GitHub pointer: download the directory containing the azure.yaml.
	staging, err := os.MkdirTemp("", "azd-foundry-adopt-*")
	if err != nil {
		return "", noop, fmt.Errorf("creating staging dir: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(staging) }
	if err := stageRemoteAzureYaml(ctx, azdClient, httpClient, pointer, staging); err != nil {
		cleanup()
		return "", noop, err
	}
	return staging, cleanup, nil
}

// ensureStagedAzureYaml normalizes a staged template so azd-core sees
// azure.yaml at the template root. azd-core only adopts azure.yaml; if a sample
// ships azure.yml, copy it to azure.yaml and remove the alias to avoid leaving
// duplicate project manifests in the initialized project.
func ensureStagedAzureYaml(staging string) (bool, error) {
	azureYaml := filepath.Join(staging, "azure.yaml")
	if fileExists(azureYaml) {
		return true, nil
	}

	azureYml := filepath.Join(staging, "azure.yml")
	if !fileExists(azureYml) {
		return false, nil
	}

	//nolint:gosec // azure.yml is in a temp staging dir produced by this command
	data, err := os.ReadFile(azureYml)
	if err != nil {
		return false, fmt.Errorf("reading staged azure.yml: %w", err)
	}
	//nolint:gosec // staging dir is from os.MkdirTemp and the filename is a constant
	if err := os.WriteFile(azureYaml, data, osutil.PermissionFile); err != nil {
		return false, fmt.Errorf("writing staged azure.yaml: %w", err)
	}
	if err := os.Remove(azureYml); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("removing staged azure.yml: %w", err)
	}
	return true, nil
}

func clearStagingDirectory(staging string) error {
	if err := os.RemoveAll(staging); err != nil {
		return fmt.Errorf("clearing staging directory: %w", err)
	}
	if err := os.MkdirAll(staging, osutil.PermissionDirectory); err != nil {
		return fmt.Errorf("recreating staging directory: %w", err)
	}
	return nil
}

// stageRemoteAzureYaml downloads the directory containing the remote azure.yaml
// into staging. It first tries an unauthenticated public download (no gh CLI),
// then falls back to the GitHub CLI for private repositories or URL forms the
// naive parser can't handle — mirroring downloadAgentYaml's resolution order.
func stageRemoteAzureYaml(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	httpClient *http.Client,
	pointer string,
	staging string,
) error {
	fmt.Println(output.WithGrayFormat("Downloading sample from GitHub..."))

	triedPublicDownload := false
	if urlInfo := parseGitHubUrlNaive(pointer); urlInfo != nil {
		triedPublicDownload = true
		dirPath := parentDirOf(urlInfo.FilePath)
		err := downloadDirectoryContentsWithoutGhCli(
			ctx, urlInfo.RepoSlug, dirPath, dirPath, urlInfo.Branch, staging, httpClient,
		)
		if err == nil {
			hasAzureYaml, normalizeErr := ensureStagedAzureYaml(staging)
			if normalizeErr != nil {
				return normalizeErr
			}
			if hasAzureYaml {
				return nil
			}
		}
	}

	if triedPublicDownload {
		if err := clearStagingDirectory(staging); err != nil {
			return err
		}
	}

	// Fall back to the GitHub CLI (handles private repos and complex URLs).
	commandRunner := exec.NewCommandRunner(&exec.RunnerOptions{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	})
	console := input.NewConsole(
		false, // noPrompt
		true,  // isTerminal
		input.Writers{Output: os.Stdout},
		input.ConsoleHandles{
			Stderr: os.Stderr,
			Stdin:  os.Stdin,
			Stdout: os.Stdout,
		},
		nil, // formatter
		nil, // externalPromptCfg
	)
	ghCli := github.NewGitHubCli(console, commandRunner)
	if err := ghCli.EnsureInstalled(ctx); err != nil {
		return exterrors.Dependency(
			exterrors.CodeGitHubDownloadFailed,
			fmt.Sprintf("ensuring gh is installed: %s", err),
			"install the GitHub CLI (gh) from https://cli.github.com",
		)
	}

	urlInfo, err := parseGitHubUrlForAdopt(ctx, azdClient, pointer)
	if err != nil {
		return err
	}
	dirPath := parentDirOf(urlInfo.FilePath)
	if err := downloadDirectoryContents(
		ctx, urlInfo.Hostname, urlInfo.RepoSlug, dirPath, dirPath, urlInfo.Branch, staging, ghCli, console,
	); err != nil {
		return exterrors.Dependency(
			exterrors.CodeGitHubDownloadFailed,
			fmt.Sprintf("downloading sample directory: %s", err),
			"verify the URL points to a valid azure.yaml in the repository and you have access",
		)
	}

	hasAzureYaml, err := ensureStagedAzureYaml(staging)
	if err != nil {
		return err
	}
	if !hasAzureYaml {
		return exterrors.Validation(
			exterrors.CodeInvalidManifestPointer,
			"no azure.yaml was found in the downloaded sample directory",
			"verify the URL points to a directory that contains an azure.yaml",
		)
	}
	return nil
}

// parseGitHubUrlForAdopt resolves GitHub repository info for a pointer using the
// azd host (no InitAction required), mirroring (*InitAction).parseGitHubUrl.
func parseGitHubUrlForAdopt(
	ctx context.Context, azdClient *azdext.AzdClient, pointer string,
) (*GitHubUrlInfo, error) {
	urlInfo, err := azdClient.Project().ParseGitHubUrl(ctx, &azdext.ParseGitHubUrlRequest{
		Url: pointer,
	})
	if err != nil {
		return nil, exterrors.Dependency(
			exterrors.CodeGitHubDownloadFailed,
			fmt.Sprintf("parsing GitHub URL: %s", err),
			"verify the URL points to a file in a GitHub repository",
		)
	}
	return &GitHubUrlInfo{
		RepoSlug: urlInfo.RepoSlug,
		Branch:   urlInfo.Branch,
		FilePath: urlInfo.FilePath,
		Hostname: urlInfo.Hostname,
	}, nil
}

// parentDirOf returns the directory portion of a repo-relative file path, or ""
// when the file lives at the repository root (so the download lists the root).
func parentDirOf(filePath string) string {
	parts := strings.Split(filePath, "/")
	if len(parts) <= 1 {
		return ""
	}
	return strings.Join(parts[:len(parts)-1], "/")
}

// stagedAzureYamlExists reports whether the staging directory contains an
// adopted azure.yaml (or azure.yml) at its root.
func stagedAzureYamlExists(staging string) bool {
	return fileExists(filepath.Join(staging, "azure.yaml")) ||
		fileExists(filepath.Join(staging, "azure.yml"))
}

// ensureFoundryProviderDeclared stamps `infra.provider: microsoft.foundry` onto
// the adopted azure.yaml when the sample didn't already declare it, keeping
// provisioning bicep-less by default.
func ensureFoundryProviderDeclared(ctx context.Context, azdClient *azdext.AzdClient) error {
	resp, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return exterrors.Dependency(
			exterrors.CodeProjectNotFound,
			fmt.Sprintf("failed to get project after adoption: %s", err),
			"",
		)
	}
	if hasFoundryProviderDeclared(resp.Project) {
		return nil
	}
	return writeFoundryProvider(ctx, azdClient)
}

// printAdoptionNextSteps emits context-aware next-step guidance after adoption,
// reusing the shared nextstep resolver. State-assembly errors are intentionally
// ignored: the resolver degrades gracefully on partial state.
func printAdoptionNextSteps(ctx context.Context, azdClient *azdext.AzdClient, folderDisplay string) {
	var stateOpts []nextstep.Option
	if folderDisplay != "" {
		stateOpts = append(stateOpts, nextstep.WithCreatedFolder(folderDisplay))
	}
	state, _ := nextstep.AssembleState(ctx, azdClient, stateOpts...)
	_ = printAllNextIfTerminal(os.Stdout, nextstep.ResolveAfterInit(state, readmeExistsForProject(ctx, azdClient)))
}

// applyDeployModeToAdoptedProject locates the azure.ai.agent service in the
// adopted project and applies deploy-mode configuration (code or container)
// based on the --deploy-mode, --runtime, and --entry-point flags. When no
// explicit flag is passed and the service already has a codeConfiguration or
// docker property, the service is left unchanged (the sample is pre-configured).
//
// It reports whether any agent service resolved to a container
// (Docker) deploy so the caller can decide whether an Azure
// Container Registry must be wired (existing project) or created
// on provision.
func applyDeployModeToAdoptedProject(
	ctx context.Context,
	flags *initFlags,
	azdClient *azdext.AzdClient,
) (bool, error) {
	// Validate --image flag early (incompatible with --deploy-mode code).
	if err := validateImageFlag(flags.image, flags.deployMode); err != nil {
		return false, err
	}

	resp, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return false, fmt.Errorf("reading adopted project: %w", err)
	}

	// Collect all agent services in the adopted project.
	type agentEntry struct {
		name string
		svc  *azdext.ServiceConfig
	}
	var agentServices []agentEntry
	for name, svc := range resp.GetProject().GetServices() {
		if svc.GetHost() == AiAgentHost {
			agentServices = append(agentServices, agentEntry{name: name, svc: svc})
		}
	}
	if len(agentServices) == 0 {
		// No agent service found -- nothing to configure.
		return false, nil
	}

	// Apply configuration to each agent service, tracking whether any
	// resolves to a container deploy so the caller can wire an ACR.
	usesContainer := false
	for _, agent := range agentServices {
		container, err := applyDeployModeToService(ctx, flags, azdClient, agent.name, agent.svc)
		if err != nil {
			return false, err
		}
		if container {
			usesContainer = true
		}
	}
	return usesContainer, nil
}

// applyDeployModeToService applies deploy-mode configuration to a
// single agent service and reports whether the resolved mode is a
// container (Docker) deploy. A container deploy that azd builds
// requires an Azure Container Registry; a code (ZIP) deploy does
// not. A user-provided --image is a container deploy but uses the
// caller's own registry, so callers treat --image as skip-ACR.
func applyDeployModeToService(
	ctx context.Context,
	flags *initFlags,
	azdClient *azdext.AzdClient,
	serviceName string,
	svc *azdext.ServiceConfig,
) (bool, error) {
	// Apply --image override to the agent service when provided.
	if flags.image != "" {
		imageValue, err := structpb.NewValue(flags.image)
		if err != nil {
			return false, fmt.Errorf("encoding image value: %w", err)
		}
		if _, err := azdClient.Project().SetServiceConfigValue(ctx, &azdext.SetServiceConfigValueRequest{
			ServiceName: serviceName,
			Path:        "image",
			Value:       imageValue,
		}); err != nil {
			return false, fmt.Errorf("writing image to agent service %q: %w", serviceName, err)
		}
		log.Printf("Applied --image %q to agent service %q", flags.image, serviceName)

		// --image implies container deploy; apply container config and return.
		if err := applyContainerDeployToService(ctx, azdClient, serviceName, svc); err != nil {
			return false, err
		}
		return true, nil
	}

	// Check whether the service already specifies its deploy mode.
	hasCodeConfig := adoptedServiceHasCodeConfig(svc)
	hasDocker := adoptedServiceHasDocker(svc)

	// When no explicit --deploy-mode flag is passed and the service
	// is already configured, respect the sample's existing config. A
	// pre-configured docker property means container deploy.
	if flags.deployMode == "" && (hasCodeConfig || hasDocker) {
		return hasDocker, nil
	}

	// Use the service's subdirectory for language detection (not project root).
	targetDir := svc.GetRelativePath()
	if targetDir == "" {
		targetDir = "."
	}
	showCodeDeploy := isPythonProject(targetDir) || isDotnetProject(targetDir)
	// userProvidedManifest is true: -m was explicitly provided.
	deployMode, err := promptDeployMode(ctx, azdClient, flags.noPrompt, showCodeDeploy, flags.deployMode, true)
	if err != nil {
		return false, fmt.Errorf("resolving deploy mode for adopted project: %w", err)
	}

	if deployMode == "code" {
		return false, applyCodeDeployToService(ctx, flags, azdClient, serviceName, targetDir, svc)
	}
	if err := applyContainerDeployToService(ctx, azdClient, serviceName, svc); err != nil {
		return false, err
	}
	return true, nil
}

// adoptedServiceHasCodeConfig checks whether the adopted agent service already
// declares a codeConfiguration in its properties.
func adoptedServiceHasCodeConfig(svc *azdext.ServiceConfig) bool {
	props := svc.GetAdditionalProperties()
	if props == nil {
		return false
	}
	fields := props.GetFields()
	if fields == nil {
		return false
	}
	v, ok := fields["codeConfiguration"]
	if !ok {
		return false
	}
	// A null value doesn't count as having a codeConfiguration.
	return v != nil && v.GetStructValue() != nil
}

// adoptedServiceHasDocker checks whether the adopted agent service already
// declares a docker configuration in its properties. We check
// additionalProperties rather than svc.GetDocker() because the gRPC mapper
// always returns a non-nil Docker pointer (even for the zero-value struct).
func adoptedServiceHasDocker(svc *azdext.ServiceConfig) bool {
	props := svc.GetAdditionalProperties()
	if props == nil {
		return false
	}
	fields := props.GetFields()
	if fields == nil {
		return false
	}
	v, ok := fields["docker"]
	if !ok {
		return false
	}
	// A null value doesn't count as having docker configured.
	return v != nil && v.GetStructValue() != nil
}

// applyCodeDeployToService writes codeConfiguration onto the adopted agent
// service and updates the service language from "docker" to the appropriate
// language for the selected runtime.
func applyCodeDeployToService(
	ctx context.Context,
	flags *initFlags,
	azdClient *azdext.AzdClient,
	serviceName string,
	targetDir string,
	svc *azdext.ServiceConfig,
) error {
	codeConfig, err := promptCodeConfig(ctx, azdClient, targetDir, flags.noPrompt, codeDeployOptions{
		runtime:       flags.runtime,
		entryPoint:    flags.entryPoint,
		depResolution: flags.depResolution,
	}, true) // userProvidedManifest=true since -m was provided
	if err != nil {
		return fmt.Errorf("resolving code configuration for adopted project: %w", err)
	}

	// Write codeConfiguration onto the service (camelCase keys match the
	// azure.yaml inline format read by the deploy path via JSON unmarshal).
	codeConfigMap := map[string]any{
		"runtime":    codeConfig.Runtime,
		"entryPoint": codeConfig.EntryPoint,
	}
	if codeConfig.DependencyResolution != nil {
		codeConfigMap["dependencyResolution"] = *codeConfig.DependencyResolution
	}

	codeConfigValue, err := structpb.NewValue(codeConfigMap)
	if err != nil {
		return fmt.Errorf("encoding codeConfiguration: %w", err)
	}

	if _, err := azdClient.Project().SetServiceConfigValue(ctx, &azdext.SetServiceConfigValueRequest{
		ServiceName: serviceName,
		Path:        "codeConfiguration",
		Value:       codeConfigValue,
	}); err != nil {
		return fmt.Errorf("writing codeConfiguration to agent service: %w", err)
	}

	// Update the service language to match the runtime.
	language := "python"
	if strings.HasPrefix(codeConfig.Runtime, "dotnet_") {
		language = "csharp"
	}
	langValue, err := structpb.NewValue(language)
	if err != nil {
		return fmt.Errorf("encoding language value: %w", err)
	}
	if _, err := azdClient.Project().SetServiceConfigValue(ctx, &azdext.SetServiceConfigValueRequest{
		ServiceName: serviceName,
		Path:        "language",
		Value:       langValue,
	}); err != nil {
		return fmt.Errorf("updating service language to %s: %w", language, err)
	}

	// Remove docker property if it was previously set (switching from container to code).
	if adoptedServiceHasDocker(svc) {
		if _, err := azdClient.Project().UnsetServiceConfig(ctx, &azdext.UnsetServiceConfigRequest{
			ServiceName: serviceName,
			Path:        "docker",
		}); err != nil {
			log.Printf("warning: could not clear docker property on service %q: %v", serviceName, err)
		}
	}

	log.Printf("Applied code deploy configuration (runtime=%s, entryPoint=%s) to service %q",
		codeConfig.Runtime, codeConfig.EntryPoint, serviceName)
	return nil
}

// applyContainerDeployToService sets the docker property on the adopted agent
// service and ensures the language is "docker". Removes any codeConfiguration
// if present.
func applyContainerDeployToService(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	serviceName string,
	svc *azdext.ServiceConfig,
) error {
	// Set docker property with remote build enabled.
	dockerMap := map[string]any{"remoteBuild": true}
	dockerValue, err := structpb.NewValue(dockerMap)
	if err != nil {
		return fmt.Errorf("encoding docker configuration: %w", err)
	}

	if _, err := azdClient.Project().SetServiceConfigValue(ctx, &azdext.SetServiceConfigValueRequest{
		ServiceName: serviceName,
		Path:        "docker",
		Value:       dockerValue,
	}); err != nil {
		return fmt.Errorf("writing docker property to agent service: %w", err)
	}

	// Set language to docker.
	langValue, err := structpb.NewValue("docker")
	if err != nil {
		return fmt.Errorf("encoding language value: %w", err)
	}
	if _, err := azdClient.Project().SetServiceConfigValue(ctx, &azdext.SetServiceConfigValueRequest{
		ServiceName: serviceName,
		Path:        "language",
		Value:       langValue,
	}); err != nil {
		return fmt.Errorf("updating service language to docker: %w", err)
	}

	// Remove codeConfiguration if present (switching from code to container).
	if adoptedServiceHasCodeConfig(svc) {
		if _, err := azdClient.Project().UnsetServiceConfig(ctx, &azdext.UnsetServiceConfigRequest{
			ServiceName: serviceName,
			Path:        "codeConfiguration",
		}); err != nil {
			log.Printf("warning: could not clear codeConfiguration on service %q: %v", serviceName, err)
		}
	}

	log.Printf("Applied container deploy configuration to service %q", serviceName)
	return nil
}
