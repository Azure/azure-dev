// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package foundrypolicy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
)

const (
	// DeniedModelsDisplaySuffix is the display-name suffix for denied Foundry models in Resource Graph.
	DeniedModelsDisplaySuffix = "microsoft foundry agents should not use denied models"
	// AllowedModelsDisplaySuffix is the display-name suffix for allowed Foundry models in Resource Graph.
	AllowedModelsDisplaySuffix = "microsoft foundry agents should only use allowed models"

	deploymentAPIVersion  = "2025-10-01-preview"
	modelPolicyAPIVersion = "2026-03-15-preview"
)

var (
	deniedModelsSpec = PolicyDefinitionSpec{
		DisplayNameSuffix: DeniedModelsDisplaySuffix,
		ParameterName:     "deniedModels",
		IsDenyList:        true,
	}
	allowedModelsSpec = PolicyDefinitionSpec{
		DisplayNameSuffix: AllowedModelsDisplaySuffix,
		ParameterName:     "allowedModels",
		IsDenyList:        false,
	}
)

// IsModelBlockedByInventory reports whether a Resource Graph policy assignment blocks model.
func IsModelBlockedByInventory(assignment PolicyAssignment, projectID string, model ModelCandidate) bool {
	if !IsPolicyAssignmentApplicable(assignment, projectID) {
		return false
	}
	var candidates []string
	for _, v := range []string{model.Name, model.DeploymentName} {
		v = strings.ToLower(strings.TrimSpace(v))
		if v != "" && !slices.Contains(candidates, v) {
			candidates = append(candidates, v)
		}
	}
	if len(candidates) == 0 {
		return false
	}

	if MatchesPolicyDefinition(assignment, deniedModelsSpec) {
		patterns, ok := EffectiveStringSliceParam(assignment, deniedModelsSpec.ParameterName)
		if !ok {
			return false
		}
		return slices.ContainsFunc(patterns, func(pattern string) bool {
			return slices.ContainsFunc(candidates, func(c string) bool {
				return MatchesAzurePolicyLike(c, pattern)
			})
		})
	}

	if MatchesPolicyDefinition(assignment, allowedModelsSpec) {
		patterns, ok := EffectiveStringSliceParam(assignment, allowedModelsSpec.ParameterName)
		if !ok {
			return false
		}
		matched := slices.ContainsFunc(patterns, func(pattern string) bool {
			return slices.ContainsFunc(candidates, func(c string) bool {
				return MatchesAzurePolicyLike(c, pattern)
			})
		})
		return !matched
	}

	return false
}

// EvaluateInventoryModelPolicy evaluates Resource-Graph-backed model policy assignments.
func EvaluateInventoryModelPolicy(inventory Inventory, models []ModelCandidate) []Violation {
	if len(models) == 0 {
		return nil
	}
	sorted := SortAssignmentsByID(inventory.Assignments)
	var violations []Violation
	for _, model := range models {
		targetLabel := strings.TrimSpace(model.Name)
		if targetLabel == "" {
			targetLabel = strings.TrimSpace(model.DeploymentName)
		}
		for _, assignment := range sorted {
			if IsModelBlockedByInventory(assignment, inventory.ProjectID, model) {
				displayName := AssignmentDisplayName(assignment)
				violations = append(violations, Violation{
					Target:       TargetModel,
					TargetValue:  targetLabel,
					AssignmentID: assignment.AssignmentID,
					DisplayName:  displayName,
					PortalURL:    BuildPolicyAssignmentPortalURL(inventory.ProjectID, assignment.AssignmentID),
					Message: fmt.Sprintf(
						"The %q policy doesn't allow model %q in this project.",
						displayName,
						targetLabel,
					),
				})
			}
		}
	}
	return violations
}

// ARMDeploymentPolicyEvaluator evaluates model deployment policies using the Cognitive Services
// `evaluateDeploymentPolicies` ARM endpoint (matching Foundry Toolkit's ModelPolicyAccessor).
type ARMDeploymentPolicyEvaluator struct {
	pipeline    runtime.Pipeline
	armEndpoint string
}

// NewARMDeploymentPolicyEvaluator creates an ARMDeploymentPolicyEvaluator backed by pipeline.
func NewARMDeploymentPolicyEvaluator(pipeline runtime.Pipeline, armEndpoint string) *ARMDeploymentPolicyEvaluator {
	if strings.TrimSpace(armEndpoint) == "" {
		armEndpoint = defaultARMEndpoint
	}
	return &ARMDeploymentPolicyEvaluator{
		pipeline:    pipeline,
		armEndpoint: strings.TrimRight(strings.TrimSpace(armEndpoint), "/"),
	}
}

type armDeploymentModel struct {
	Format  string `json:"format"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

type armGetDeploymentResponse struct {
	Properties *struct {
		Model *armDeploymentModel `json:"model,omitempty"`
	} `json:"properties,omitempty"`
}

type evaluateDeploymentPoliciesItem struct {
	Name       string `json:"name"`
	Properties struct {
		Model armDeploymentModel `json:"model"`
	} `json:"properties"`
}

type evaluateDeploymentPoliciesRequest struct {
	Deployments []evaluateDeploymentPoliciesItem `json:"deployments"`
}

type evaluateDeploymentPoliciesResponse struct {
	Results map[string]struct {
		EvaluationOutcome       string `json:"evaluationOutcome"`
		ErrorMessage            string `json:"errorMessage"`
		NonCompliantAssignments []struct {
			AssignmentID string `json:"assignmentId"`
			Effect       string `json:"effect"`
		} `json:"nonCompliantAssignments"`
	} `json:"results"`
}

// EvaluateModelDeployments evaluates model deployments against the Cognitive Services account's
// `evaluateDeploymentPolicies` endpoint. When a ModelCandidate only carries a DeploymentName
// (e.g. an existing deployment on the account), its model format/name/version are fetched first.
func (e *ARMDeploymentPolicyEvaluator) EvaluateModelDeployments(
	ctx context.Context,
	projectResourceID string,
	models []ModelCandidate,
) ([]Violation, error) {
	if e == nil || len(models) == 0 {
		return nil, nil
	}
	parsed, ok := ParseFoundryProjectID(projectResourceID)
	if !ok {
		return nil, fmt.Errorf("invalid Foundry project resource ID: %s", projectResourceID)
	}
	accountID := fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.CognitiveServices/accounts/%s",
		parsed.SubscriptionID,
		parsed.ResourceGroup,
		parsed.ProjectResourceName,
	)

	var items []evaluateDeploymentPoliciesItem
	seen := map[string]bool{}
	for _, m := range models {
		depName := strings.TrimSpace(m.DeploymentName)
		if depName == "" {
			depName = strings.TrimSpace(m.Name)
		}
		if depName == "" || seen[depName] {
			continue
		}
		seen[depName] = true

		modelMeta := armDeploymentModel{
			Format:  strings.TrimSpace(m.Format),
			Name:    strings.TrimSpace(m.Name),
			Version: strings.TrimSpace(m.Version),
		}
		if modelMeta.Format == "" || modelMeta.Name == "" || modelMeta.Version == "" {
			fetched, err := e.getDeploymentModel(ctx, accountID, depName)
			if err != nil || fetched == nil || fetched.Format == "" || fetched.Name == "" || fetched.Version == "" {
				// Best-effort: if deployment metadata cannot be resolved (e.g. unprovisioned
				// deployment without full format/version), skip ARM deployment policy evaluation.
				continue
			}
			modelMeta = *fetched
		}

		item := evaluateDeploymentPoliciesItem{Name: depName}
		item.Properties.Model = modelMeta
		items = append(items, item)
	}

	if len(items) == 0 {
		return nil, nil
	}

	reqURL := fmt.Sprintf(
		"%s%s/evaluateDeploymentPolicies?api-version=%s",
		e.armEndpoint,
		accountID,
		modelPolicyAPIVersion,
	)
	bodyBytes, err := json.Marshal(evaluateDeploymentPoliciesRequest{Deployments: items})
	if err != nil {
		return nil, fmt.Errorf("marshal evaluateDeploymentPolicies request: %w", err)
	}

	req, err := runtime.NewRequest(ctx, http.MethodPost, reqURL)
	if err != nil {
		return nil, fmt.Errorf("create evaluateDeploymentPolicies request: %w", err)
	}
	req.Raw().Header.Set("Accept", "application/json")
	if err := req.SetBody(streaming.NopCloser(bytes.NewReader(bodyBytes)), "application/json"); err != nil {
		return nil, fmt.Errorf("set evaluateDeploymentPolicies body: %w", err)
	}

	resp, err := e.pipeline.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send evaluateDeploymentPolicies request: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read evaluateDeploymentPolicies response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("evaluateDeploymentPolicies returned HTTP %d", resp.StatusCode)
	}

	var parsedResp evaluateDeploymentPoliciesResponse
	if err := json.Unmarshal(respBytes, &parsedResp); err != nil {
		return nil, fmt.Errorf("decode evaluateDeploymentPolicies response: %w", err)
	}

	var violations []Violation
	for _, item := range items {
		res, exists := parsedResp.Results[item.Name]
		if !exists {
			continue
		}
		var denyAssignmentID string
		hasDeny := false
		for _, a := range res.NonCompliantAssignments {
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(a.Effect)), "deny") {
				hasDeny = true
				if denyAssignmentID == "" && strings.TrimSpace(a.AssignmentID) != "" {
					denyAssignmentID = strings.TrimSpace(a.AssignmentID)
				}
			}
		}
		if !hasDeny && !strings.EqualFold(res.EvaluationOutcome, "NonCompliant") {
			continue
		}
		if denyAssignmentID == "" {
			for _, a := range res.NonCompliantAssignments {
				if strings.TrimSpace(a.AssignmentID) != "" {
					denyAssignmentID = strings.TrimSpace(a.AssignmentID)
					break
				}
			}
		}

		policyName := "Azure Policy assignment"
		if denyAssignmentID != "" {
			parts := strings.Split(strings.TrimRight(denyAssignmentID, "/"), "/")
			if last := parts[len(parts)-1]; last != "" {
				policyName = last
			}
		}
		targetVal := item.Properties.Model.Name
		if item.Properties.Model.Version != "" {
			targetVal = fmt.Sprintf("%s (version %s)", item.Properties.Model.Name, item.Properties.Model.Version)
		}
		msg := fmt.Sprintf(
			"Your organization's policy (%q) blocks deployment or use of model %q.",
			policyName,
			targetVal,
		)
		if strings.TrimSpace(res.ErrorMessage) != "" {
			msg = strings.TrimSpace(res.ErrorMessage)
		}
		violations = append(violations, Violation{
			Target:       TargetModel,
			TargetValue:  targetVal,
			AssignmentID: NormalizeScope(denyAssignmentID),
			DisplayName:  policyName,
			PortalURL:    BuildPolicyAssignmentPortalURL(projectResourceID, denyAssignmentID),
			Message:      msg,
		})
	}
	return violations, nil
}

func (e *ARMDeploymentPolicyEvaluator) getDeploymentModel(
	ctx context.Context,
	accountID string,
	deploymentName string,
) (*armDeploymentModel, error) {
	reqURL := fmt.Sprintf(
		"%s%s/deployments/%s?api-version=%s",
		e.armEndpoint,
		accountID,
		url.PathEscape(deploymentName),
		deploymentAPIVersion,
	)
	req, err := runtime.NewRequest(ctx, http.MethodGet, reqURL)
	if err != nil {
		return nil, err
	}
	req.Raw().Header.Set("Accept", "application/json")
	resp, err := e.pipeline.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GET deployment returned HTTP %d", resp.StatusCode)
	}
	var body armGetDeploymentResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	if body.Properties == nil {
		return nil, nil
	}
	return body.Properties.Model, nil
}
