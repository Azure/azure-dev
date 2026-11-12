// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore nextgen noncompliant managementgroups

package provisioning

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"slices"
	"strings"

	"azure.ai.projects/internal/azure"
	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/shared/foundry/policy"
)

const (
	// SkipModelPolicyChecksEnvKey opts out of client-side Azure Policy pre-checks.
	SkipModelPolicyChecksEnvKey = policy.SkipPolicyChecksEnvKey
	// LegacySkipModelPolicyChecksEnvKey is the legacy opt-out environment variable.
	LegacySkipModelPolicyChecksEnvKey = policy.LegacySkipPolicyChecksEnvKey

	defaultARMEndpoint                    = "https://management.azure.com"
	cognitiveServicesDeploymentAPIVersion = "2025-10-01-preview"
	evaluateDeploymentPoliciesAPIVersion  = "2026-03-15-preview"
	defaultFoundryGovernPortalURL         = "https://ai.azure.com/?flight=govern_ignite"
	modelPolicyEvaluationTimeout          = policy.DefaultPolicyPreflightTimeout
)

var (
	modelPolicyARMEndpoint = defaultARMEndpoint
	newModelPolicyPipeline = defaultModelPolicyPipeline
)

// ModelPolicyViolation describes an Azure Policy Deny assignment that blocks a model deployment.
type ModelPolicyViolation struct {
	DeploymentName string
	ModelFormat    string
	ModelName      string
	ModelVersion   string
	AssignmentID   string
	DisplayName    string
	PortalURL      string
	Message        string
}

// ShouldSkipModelPolicyChecks reports whether client-side model deployment Azure Policy
// pre-checks are disabled via environment configuration.
func ShouldSkipModelPolicyChecks(env map[string]string) bool {
	return policy.ShouldSkipPolicyChecks(env)
}

// ParseFoundryProjectResourceID parses and validates a Microsoft Foundry project ARM resource ID.
func ParseFoundryProjectResourceID(projectID string) (subID, rg, account, project string, ok bool) {
	parsed, valid := policy.ParseFoundryProjectID(projectID)
	if !valid || parsed == nil {
		return "", "", "", "", false
	}
	return parsed.SubscriptionID, parsed.ResourceGroup, parsed.ProjectResourceName, parsed.ProjectName, true
}

func isValidModelPolicyAssignmentID(assignmentID string) bool {
	return policy.IsValidPolicyAssignmentID(assignmentID)
}

// BuildModelPolicyAssignmentPortalURL constructs the Foundry Portal Manage -> Govern deep link
// for a blocking Azure Policy assignment. Falls back to the generic Foundry Govern URL when
// projectResourceID or assignmentID cannot be validated.
func BuildModelPolicyAssignmentPortalURL(projectResourceID, assignmentID string) string {
	return policy.BuildPolicyAssignmentPortalURL(projectResourceID, assignmentID)
}

func defaultModelPolicyPipeline(cred azcore.TokenCredential) runtime.Pipeline {
	return policy.NewARMPipeline(cred, "azure-ai-projects-policy", "v1.0.0", azure.NewArmClientOptions())
}

func hasParameterKey(parameters map[string]any, name string) bool {
	for k := range parameters {
		if strings.EqualFold(k, name) {
			return true
		}
	}
	return false
}

func resolveTemplateParameterValue(src *templateSource, name string) any {
	if src == nil {
		return nil
	}
	for k, rawParam := range src.parameters {
		if !strings.EqualFold(k, name) {
			continue
		}
		if paramMap, ok := rawParam.(map[string]any); ok {
			if inner, hasValue := paramMap["value"]; hasValue {
				return inner
			}
		}
		return rawParam
	}
	return parameterValue(src, name)
}

func effectiveModelPolicyTarget(
	src *templateSource,
	fallbackProjectID string,
	synthResult *synthesis.Result,
) (string, []synthesis.Deployment) {
	projectID := strings.TrimSpace(fallbackProjectID)
	if src != nil &&
		(templateDeclaresParameter(src, "projectResourceId") ||
			hasParameterKey(src.parameters, "projectResourceId")) {
		rawID, _ := resolveTemplateParameterValue(src, "projectResourceId").(string)
		projectID = strings.TrimSpace(rawID)
	}
	if _, _, _, _, ok := ParseFoundryProjectResourceID(projectID); !ok {
		return "", nil
	}

	if src != nil && src.parameters != nil {
		return projectID, decodeModelDeploymentsValue(resolveTemplateParameterValue(src, "deployments"))
	}
	return projectID, extractEffectiveModelDeployments(nil, synthResult)
}

func extractEffectiveModelDeployments(
	parameters map[string]any,
	synthResult *synthesis.Result,
) []synthesis.Deployment {
	if parameters != nil {
		for k, rawParam := range parameters {
			if !strings.EqualFold(k, "deployments") {
				continue
			}
			val := rawParam
			if paramMap, ok := rawParam.(map[string]any); ok {
				if inner, hasValue := paramMap["value"]; hasValue {
					val = inner
				}
			}
			return decodeModelDeploymentsValue(val)
		}
		return nil
	}
	if synthResult != nil && synthResult.Parameters != nil {
		for k, rawParam := range synthResult.Parameters {
			if strings.EqualFold(k, "deployments") {
				return decodeModelDeploymentsValue(rawParam)
			}
		}
	}
	return nil
}

func decodeModelDeploymentsValue(val any) []synthesis.Deployment {
	if val == nil {
		return nil
	}
	if typed, ok := val.([]synthesis.Deployment); ok {
		return typed
	}
	if pointers, ok := val.([]*synthesis.Deployment); ok {
		out := make([]synthesis.Deployment, 0, len(pointers))
		for _, p := range pointers {
			if p != nil {
				out = append(out, *p)
			}
		}
		return out
	}
	rawBytes, err := json.Marshal(val)
	if err != nil {
		return nil
	}
	var decoded []synthesis.Deployment
	if err := json.Unmarshal(rawBytes, &decoded); err != nil {
		return nil
	}
	return decoded
}

func mergeModelDeployments(deployments []synthesis.Deployment) []synthesis.Deployment {
	if len(deployments) == 0 {
		return nil
	}
	var merged []synthesis.Deployment
	for _, dep := range deployments {
		name := strings.TrimSpace(dep.Name)
		modelName := strings.TrimSpace(dep.Model.Name)
		modelFormat := strings.TrimSpace(dep.Model.Format)
		modelVersion := strings.TrimSpace(dep.Model.Version)
		if name == "" {
			name = modelName
		}
		if name == "" {
			continue
		}
		idx := slices.IndexFunc(merged, func(existing synthesis.Deployment) bool {
			return strings.EqualFold(existing.Name, name)
		})
		if idx >= 0 {
			if merged[idx].Model.Format == "" && modelFormat != "" {
				merged[idx].Model.Format = modelFormat
			}
			if merged[idx].Model.Name == "" && modelName != "" {
				merged[idx].Model.Name = modelName
			}
			if merged[idx].Model.Version == "" && modelVersion != "" {
				merged[idx].Model.Version = modelVersion
			}
			if merged[idx].Sku.Name == "" && strings.TrimSpace(dep.Sku.Name) != "" {
				merged[idx].Sku.Name = strings.TrimSpace(dep.Sku.Name)
			}
			if merged[idx].Sku.Capacity <= 0 && dep.Sku.Capacity > 0 {
				merged[idx].Sku.Capacity = dep.Sku.Capacity
			}
			continue
		}
		merged = append(merged, synthesis.Deployment{
			Name: name,
			Model: synthesis.DeploymentModel{
				Format:  modelFormat,
				Name:    modelName,
				Version: modelVersion,
			},
			Sku: dep.Sku,
		})
	}
	return merged
}

// EvaluateProjectModelDeploymentsPolicy evaluates model deployments against the Cognitive Services
// account's `evaluateDeploymentPolicies` ARM endpoint and returns any blocking Deny violations.
func EvaluateProjectModelDeploymentsPolicy(
	ctx context.Context,
	cred azcore.TokenCredential,
	projectResourceID string,
	deployments []synthesis.Deployment,
) ([]ModelPolicyViolation, error) {
	subID, rg, account, _, ok := ParseFoundryProjectResourceID(projectResourceID)
	if !ok {
		return nil, fmt.Errorf("invalid Foundry project resource ID: %s", projectResourceID)
	}
	merged := mergeModelDeployments(deployments)
	if len(merged) == 0 {
		return nil, nil
	}
	if cred == nil {
		return nil, fmt.Errorf("Azure credential is required for model policy evaluation")
	}
	return evaluateModelDeploymentsWithPipeline(
		ctx,
		newModelPolicyPipeline(cred),
		modelPolicyARMEndpoint,
		projectResourceID,
		subID,
		rg,
		account,
		merged,
	)
}

func evaluateModelDeploymentsWithPipeline(
	ctx context.Context,
	pipeline runtime.Pipeline,
	armEndpoint string,
	projectResourceID string,
	_ string,
	_ string,
	_ string,
	deployments []synthesis.Deployment,
) ([]ModelPolicyViolation, error) {
	evalCtx, cancel := context.WithTimeout(ctx, modelPolicyEvaluationTimeout)
	defer cancel()

	candidates := make([]policy.ModelCandidate, 0, len(deployments))
	for _, dep := range deployments {
		candidates = append(candidates, policy.ModelCandidate{
			DeploymentName: dep.Name,
			Format:         dep.Model.Format,
			Name:           dep.Model.Name,
			Version:        dep.Model.Version,
		})
	}

	evaluator := policy.NewARMDeploymentPolicyEvaluator(pipeline, armEndpoint).
		WithRequireMatchingDeploymentModel(true)
	evalRes, err := evaluator.EvaluateModelDeployments(evalCtx, projectResourceID, candidates)
	violations := make([]ModelPolicyViolation, 0, len(evalRes.Violations))
	for _, v := range evalRes.Violations {
		displayName := v.DisplayName
		if v.AssignmentID == "" && displayName == "Azure Policy assignment" {
			displayName = "Azure Policy"
		}
		violations = append(violations, ModelPolicyViolation{
			DeploymentName: v.DeploymentName,
			ModelFormat:    v.ModelFormat,
			ModelName:      v.ModelName,
			ModelVersion:   v.ModelVersion,
			AssignmentID:   v.AssignmentID,
			DisplayName:    displayName,
			PortalURL:      v.PortalURL,
			Message: fmt.Sprintf(
				"The %q policy doesn't allow model %q for deployment %q in this project.",
				displayName,
				v.TargetValue,
				v.DeploymentName,
			),
		})
	}
	return violations, err
}

// CheckProjectModelDeploymentsPolicy evaluates model deployments against Azure Policy and returns
// a blocking exterrors.Validation(CodeModelBlockedByPolicy, ...) error when a Deny policy blocks
// any deployment. Transport, auth, or service errors fail open and are logged as diagnostics.
func CheckProjectModelDeploymentsPolicy(
	ctx context.Context,
	cred azcore.TokenCredential,
	projectResourceID string,
	deployments []synthesis.Deployment,
	env map[string]string,
) error {
	if ShouldSkipModelPolicyChecks(env) || len(deployments) == 0 || strings.TrimSpace(projectResourceID) == "" {
		return nil
	}
	violations, err := EvaluateProjectModelDeploymentsPolicy(ctx, cred, projectResourceID, deployments)
	if len(violations) > 0 {
		if err != nil {
			log.Printf("[FoundryPolicy] Partial model deployment policy check error: %v", err)
		}
		return ModelPolicyViolationsToError(violations)
	}
	if err != nil {
		log.Printf("[FoundryPolicy] Model deployment policy check failed (failing open): %v", err)
		return nil
	}
	return nil
}

// ModelPolicyViolationsToError converts one or more model policy violations into a structured
// exterrors.Validation error with deduplicated messages and Foundry Govern portal links.
func ModelPolicyViolationsToError(violations []ModelPolicyViolation) error {
	if len(violations) == 0 {
		return nil
	}
	var messages []string
	var links []string
	for _, v := range violations {
		displayName := strings.TrimSpace(v.DisplayName)
		if displayName == "" {
			displayName = "Azure Policy assignment"
		}
		msg := strings.TrimSpace(v.Message)
		if msg == "" {
			modelLabel := strings.TrimSpace(v.ModelName)
			if ver := strings.TrimSpace(v.ModelVersion); ver != "" && modelLabel != "" {
				modelLabel = fmt.Sprintf("%s (version %s)", modelLabel, ver)
			}
			msg = fmt.Sprintf(
				"The %q policy doesn't allow model %q for deployment %q in this project.",
				displayName,
				modelLabel,
				v.DeploymentName,
			)
		}
		if !slices.Contains(messages, msg) {
			messages = append(messages, msg)
		}
		if portalURL := strings.TrimSpace(v.PortalURL); portalURL != "" {
			linkLine := fmt.Sprintf("  View policy (%s): %s", displayName, portalURL)
			if !slices.Contains(links, linkLine) {
				links = append(links, linkLine)
			}
		}
	}

	fullMessage := fmt.Sprintf("model deployment is blocked by Azure Policy: %s", strings.Join(messages, " "))
	suggestion := "Update the model deployment configuration to comply with your organization's Azure Policy " +
		"assignments, or select a different Microsoft Foundry project."
	if len(links) > 0 {
		suggestion += "\n" + strings.Join(links, "\n")
	}
	return exterrors.Validation(exterrors.CodeModelBlockedByPolicy, fullMessage, suggestion)
}

// FormatModelPolicyViolationsWarning formats model policy violations as a non-blocking
// warning banner for CLI flows such as `azd ai project add`.
func FormatModelPolicyViolationsWarning(violations []ModelPolicyViolation) string {
	if len(violations) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(output.WithWarningFormat(
		"Warning: One or more model deployments do not comply with Azure Policy in the selected project:\n",
	))
	for _, v := range violations {
		displayName := strings.TrimSpace(v.DisplayName)
		if displayName == "" {
			displayName = "Azure Policy assignment"
		}
		msg := strings.TrimSpace(v.Message)
		if msg == "" {
			modelLabel := strings.TrimSpace(v.ModelName)
			if ver := strings.TrimSpace(v.ModelVersion); ver != "" && modelLabel != "" {
				modelLabel = fmt.Sprintf("%s (version %s)", modelLabel, ver)
			}
			msg = fmt.Sprintf(
				"The %q policy doesn't allow model %q for deployment %q in this project.",
				displayName,
				modelLabel,
				v.DeploymentName,
			)
		}
		b.WriteString(fmt.Sprintf("  - %s\n", msg))
		if portalURL := strings.TrimSpace(v.PortalURL); portalURL != "" {
			b.WriteString(fmt.Sprintf("    View policy: %s\n", portalURL))
		}
	}
	return b.String()
}
