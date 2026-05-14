// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"azureaiagent/internal/pkg/agents/dataset_api"
	"azureaiagent/internal/pkg/agents/eval_api"
	"azureaiagent/internal/pkg/agents/opteval"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/google/uuid"
)

// foundryBaseDir is the base directory for eval artifacts under the project root.
const foundryBaseDir = ".azure/.foundry"

// resolveEvalOutputPath resolves the eval config output path.
func resolveEvalOutputPath(output, agentProject string) string {
	return eval_api.ResolveEvalOutputPath(output, agentProject)
}

// resolveEvalConfigPath resolves the eval config path for reading.
func resolveEvalConfigPath(config, agentProject string) string {
	return eval_api.ResolveEvalConfigPath(config, agentProject)
}

// ensureFoundryDirs creates the .azure/.foundry directory tree with standard
// subdirectories (datasets, evaluators, results).
func ensureFoundryDirs(projectRoot string) error {
	base := filepath.Join(projectRoot, ".azure", ".foundry")
	for _, sub := range []string{"datasets", "evaluators", "results"} {
		if err := os.MkdirAll(filepath.Join(base, sub), 0750); err != nil {
			return err
		}
	}
	return nil
}

// saveDatasetGenerationResult saves the raw dataset generation result JSON.
func saveDatasetGenerationResult(projectRoot, datasetName string, result json.RawMessage) {
	if datasetName == "" || len(result) == 0 {
		return
	}
	dir := filepath.Join(projectRoot, ".azure", ".foundry", "datasets")
	if err := os.MkdirAll(dir, 0750); err != nil {
		log.Printf("[debug] failed to create dataset dir: %v", err)
		return
	}
	// Pretty-print the JSON for human review.
	var pretty json.RawMessage
	if err := json.Unmarshal(result, &pretty); err == nil {
		if formatted, err := json.MarshalIndent(pretty, "", "  "); err == nil {
			result = formatted
		}
	}
	path := filepath.Join(dir, datasetName+".json")
	if err := os.WriteFile(path, result, 0600); err != nil {
		log.Printf("[debug] failed to save dataset result: %v", err)
	}
}

// downloadDatasetArtifact downloads the dataset and writes it locally.
// If the download fails (e.g., non-TLS test server), a placeholder is written.
func downloadDatasetArtifact(
	ctx context.Context,
	client *dataset_api.DatasetClient,
	projectRoot string,
	ref *opteval.DatasetRef,
	apiVersion string,
) error {
	if ref == nil || ref.Name == "" {
		return nil
	}

	dest := datasetArtifactPath(projectRoot, ref)
	dir := filepath.Dir(dest)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("creating dataset artifact dir: %w", err)
	}

	// Attempt full download via the dataset API.
	cred, credErr := client.GetDatasetCredential(ctx, ref.Name, ref.Version, apiVersion)
	if credErr != nil {
		// Gracefully write a placeholder when credential fetch fails.
		log.Printf("[debug] dataset credential fetch failed: %v — writing placeholder", credErr)
		return os.WriteFile(dest, []byte("{}\n"), 0600)
	}

	downloadURL := cred.ResolvedDownloadURI()
	if downloadURL == "" {
		return os.WriteFile(dest, []byte("{}\n"), 0600)
	}

	data, dlErr := client.DownloadDataset(ctx, downloadURL)
	if dlErr != nil {
		log.Printf("[debug] dataset download failed: %v — writing placeholder", dlErr)
		return os.WriteFile(dest, []byte("{}\n"), 0600)
	}

	return os.WriteFile(dest, data, 0600)
}

// datasetArtifactPath returns the local filesystem path for a downloaded dataset.
func datasetArtifactPath(projectRoot string, ref *opteval.DatasetRef) string {
	if ref == nil || ref.Name == "" {
		return ""
	}
	name := ref.Name
	if ref.Version != "" {
		name = name + "-" + ref.Version
	}
	return filepath.Join(projectRoot, ".azure", ".foundry", "datasets", name+".jsonl")
}

// saveEvaluatorResult saves the raw evaluator generation result.
func saveEvaluatorResult(projectRoot, evaluatorName string, result json.RawMessage) {
	if evaluatorName == "" || len(result) == 0 {
		return
	}
	dir := filepath.Join(projectRoot, ".azure", ".foundry", "evaluators")
	if err := os.MkdirAll(dir, 0750); err != nil {
		log.Printf("[debug] failed to create evaluator dir: %v", err)
		return
	}
	var pretty json.RawMessage
	if err := json.Unmarshal(result, &pretty); err == nil {
		if formatted, err := json.MarshalIndent(pretty, "", "  "); err == nil {
			result = formatted
		}
	}
	path := filepath.Join(dir, evaluatorName+".json")
	if err := os.WriteFile(path, result, 0600); err != nil {
		log.Printf("[debug] failed to save evaluator result: %v", err)
	}
}

// writeEvalReviewArtifacts writes human-readable review artifacts for evaluators.
// It writes a stub YAML file for each evaluator unless a result JSON already exists.
func writeEvalReviewArtifacts(projectRoot string, cfg *eval_api.EvalConfig) {
	if cfg == nil {
		return
	}
	dir := filepath.Join(projectRoot, ".azure", ".foundry", "evaluators")
	if err := os.MkdirAll(dir, 0750); err != nil {
		log.Printf("[debug] failed to create evaluator review dir: %v", err)
		return
	}
	for _, evaluator := range cfg.Evaluators {
		if evaluator == "" {
			continue
		}
		// Skip if a result JSON already exists.
		jsonPath := filepath.Join(dir, evaluator+".json")
		if _, err := os.Stat(jsonPath); err == nil {
			continue
		}
		yamlPath := filepath.Join(dir, evaluator+".yaml")
		stub := fmt.Sprintf("# Evaluator stub: %s\nname: %s\n", evaluator, evaluator)
		if err := os.WriteFile(yamlPath, []byte(stub), 0600); err != nil {
			log.Printf("[debug] failed to write evaluator stub: %v", err)
		}
	}

	// Print artifact paths for user review.
	artifactsDir := filepath.Join(projectRoot, ".azure", ".foundry")
	fmt.Printf("\n   Artifacts:  %s\n", artifactsDir)
	if cfg.DatasetReference != nil && cfg.DatasetReference.Name != "" {
		name := cfg.DatasetReference.Name
		if cfg.DatasetReference.Version != "" {
			name += "-" + cfg.DatasetReference.Version
		}
		fmt.Printf("               datasets/%s.jsonl\n", name)
	}
	for _, evaluator := range cfg.Evaluators {
		if evaluator != "" {
			fmt.Printf("               evaluators/%s.json\n", evaluator)
		}
	}
}

// writeJSONFile writes a value as formatted JSON to the specified path.
func writeJSONFile(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return fmt.Errorf("creating output directory: %w", err)
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling JSON: %w", err)
	}
	return os.WriteFile(path, data, 0600)
}

// buildEvalReportURL constructs the Foundry portal URL for an eval run report.
// It reads AZURE_AI_PROJECT_ID from the azd environment and encodes the subscription ID.
// Returns empty string on any failure.
func buildEvalReportURL(ctx context.Context, azdClient *azdext.AzdClient, envName, evalID, runID string) string {
	if azdClient == nil || envName == "" || evalID == "" || runID == "" {
		return ""
	}
	v, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: envName,
		Key:     "AZURE_AI_PROJECT_ID",
	})
	if err != nil || v.Value == "" {
		log.Printf("[debug] could not read AZURE_AI_PROJECT_ID: %v", err)
		return ""
	}
	reportURL, err := evalReportURL(v.Value, evalID, runID)
	if err != nil {
		log.Printf("[debug] failed to build eval report URL: %v", err)
		return ""
	}
	return reportURL
}

// evalReportURL constructs a URL to the eval run report in the Foundry portal.
// It parses the ARM resource ID to extract subscription, resource group, account, and project info.
func evalReportURL(projectResourceID, evalID, runID string) (string, error) {
	resourceID, err := arm.ParseResourceID(projectResourceID)
	if err != nil {
		return "", fmt.Errorf("failed to parse project resource ID: %w", err)
	}

	encodedSub, err := encodeSubscriptionForURL(resourceID.SubscriptionID)
	if err != nil {
		return "", fmt.Errorf("failed to encode subscription ID: %w", err)
	}

	if resourceID.Parent == nil ||
		!strings.Contains(string(resourceID.ResourceType.Type), "/") {
		return "", fmt.Errorf(
			"resource ID does not represent a Foundry project (missing parent account): %s",
			projectResourceID,
		)
	}

	return fmt.Sprintf(
		"https://ai.azure.com/nextgen/r/%s,%s,,%s,%s/build/evaluations/%s/run/%s",
		encodedSub, resourceID.ResourceGroupName,
		resourceID.Parent.Name, resourceID.Name,
		evalID, runID,
	), nil
}

// encodeSubscriptionForURL encodes a subscription ID GUID as base64 without padding.
func encodeSubscriptionForURL(subscriptionID string) (string, error) {
	guid, err := uuid.Parse(subscriptionID)
	if err != nil {
		return "", fmt.Errorf("invalid subscription ID format: %w", err)
	}
	guidBytes, _ := guid.MarshalBinary()
	return strings.TrimRight(base64.URLEncoding.EncodeToString(guidBytes), "="), nil
}

// formatAny converts any value to a string for display.
func formatAny(v any) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case float64:
		if val == float64(int64(val)) {
			return fmt.Sprintf("%d", int64(val))
		}
		return fmt.Sprintf("%g", val)
	case bool:
		return fmt.Sprintf("%t", val)
	default:
		return fmt.Sprintf("%v", val)
	}
}
