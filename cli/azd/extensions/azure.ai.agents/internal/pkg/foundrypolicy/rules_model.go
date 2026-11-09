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
	// AllowedModelsPolicyID is the built-in Azure Policy definition ID for approved Foundry model deployments.
	AllowedModelsPolicyID = "/providers/microsoft.authorization/policydefinitions/aafe3651-cb78-4f68-9f81-e7e41509110f"

	// DeniedModelsDisplaySuffix is the display-name suffix for denied Foundry models in Resource Graph.
	DeniedModelsDisplaySuffix = "microsoft foundry agents should not use denied models"
	// AllowedModelsDisplaySuffix is the display-name suffix for approved Foundry model deployments in Resource Graph.
	AllowedModelsDisplaySuffix = "foundry model deployments should only use approved models"
	// LegacyAllowedModelsDisplaySuffix is the legacy display-name suffix for allowed Foundry models in Resource Graph.
	LegacyAllowedModelsDisplaySuffix = "microsoft foundry agents should only use allowed models"

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
		DefinitionID:      AllowedModelsPolicyID,
		DisplayNameSuffix: AllowedModelsDisplaySuffix,
		ParameterName:     "allowedAssetIds",
		IsDenyList:        false,
	}
	legacyAllowedModelsSpec = PolicyDefinitionSpec{
		DisplayNameSuffix: LegacyAllowedModelsDisplaySuffix,
		ParameterName:     "allowedModels",
		IsDenyList:        false,
	}
)

// MergeModelCandidates deduplicates and merges model candidates that share the same
// normalized deployment name (or model name when deployment name is empty), preserving
// non-empty Name, Format, Version, Publisher, and AssetID fields across entries.
func MergeModelCandidates(models []ModelCandidate) []ModelCandidate {
	if len(models) == 0 {
		return nil
	}
	var merged []ModelCandidate
	for _, m := range models {
		dep := strings.TrimSpace(m.DeploymentName)
		name := strings.TrimSpace(m.Name)
		format := strings.TrimSpace(m.Format)
		version := strings.TrimSpace(m.Version)
		publisher := strings.TrimSpace(m.Publisher)
		assetID := strings.TrimSpace(m.AssetID)
		if dep == "" && name == "" {
			continue
		}

		matchedIdx := -1
		for i := range merged {
			exDep := merged[i].DeploymentName
			exName := merged[i].Name
			switch {
			case dep != "" && exDep != "":
				if strings.EqualFold(dep, exDep) {
					matchedIdx = i
				}
			case dep != "" && exDep == "" && exName != "":
				if strings.EqualFold(dep, exName) {
					matchedIdx = i
				}
			case dep == "" && name != "" && exDep != "" && exName == "":
				if strings.EqualFold(name, exDep) {
					matchedIdx = i
				}
			case dep == "" && exDep == "" && name != "" && exName != "":
				if strings.EqualFold(name, exName) {
					matchedIdx = i
				}
			}
			if matchedIdx >= 0 {
				break
			}
		}

		if matchedIdx >= 0 {
			if merged[matchedIdx].DeploymentName == "" && dep != "" {
				merged[matchedIdx].DeploymentName = dep
			}
			if merged[matchedIdx].Name == "" && name != "" {
				merged[matchedIdx].Name = name
			}
			if merged[matchedIdx].Format == "" && format != "" {
				merged[matchedIdx].Format = format
			}
			if merged[matchedIdx].Version == "" && version != "" {
				merged[matchedIdx].Version = version
			}
			if merged[matchedIdx].Publisher == "" && publisher != "" {
				merged[matchedIdx].Publisher = publisher
			}
			if merged[matchedIdx].AssetID == "" && assetID != "" {
				merged[matchedIdx].AssetID = assetID
			}
			continue
		}

		merged = append(merged, ModelCandidate{
			DeploymentName: dep,
			Name:           name,
			Format:         format,
			Version:        version,
			Publisher:      publisher,
			AssetID:        assetID,
		})
	}
	return merged
}

// EvaluateModelInventoryAssignment evaluates a single Resource Graph policy assignment against model.
func EvaluateModelInventoryAssignment(
	assignment PolicyAssignment,
	projectID string,
	model ModelCandidate,
) EvaluationOutcome {
	if !IsPolicyAssignmentApplicable(assignment, projectID) {
		return OutcomeNotApplicable
	}

	modelName := strings.ToLower(strings.TrimSpace(model.Name))
	depName := strings.ToLower(strings.TrimSpace(model.DeploymentName))
	publisher := strings.TrimSpace(model.Publisher)
	assetID := strings.TrimSpace(model.AssetID)
	if modelName == "" && depName == "" && publisher == "" && assetID == "" {
		return OutcomeNotApplicable
	}

	if MatchesPolicyDefinition(assignment, deniedModelsSpec) {
		patterns, ok := EffectiveStringSliceParam(assignment, deniedModelsSpec.ParameterName)
		if !ok {
			return OutcomeNotApplicable
		}
		// deniedModels evaluates underlying model names (e.g. "gpt-4o"), not deployment aliases.
		if modelName == "" {
			return OutcomeIndeterminate
		}
		blocked := slices.ContainsFunc(patterns, func(pattern string) bool {
			return MatchesAzurePolicyLike(modelName, pattern)
		})
		if blocked {
			return OutcomeBlocked
		}
		return OutcomeAllowed
	}

	if MatchesPolicyDefinition(assignment, allowedModelsSpec) ||
		MatchesPolicyDefinition(assignment, legacyAllowedModelsSpec) {
		allowedPublishers, hasPublishers := EffectiveStringSliceParam(assignment, "allowedPublishers")
		allowedAssetIDs, hasAssetIDs := EffectiveStringSliceParam(assignment, "allowedAssetIds")
		isBuiltInApprove := hasPublishers || hasAssetIDs ||
			NormalizeScope(assignment.PolicyDefinitionID) == NormalizeScope(AllowedModelsPolicyID)

		if isBuiltInApprove {
			if !hasPublishers && !hasAssetIDs {
				return OutcomeNotApplicable
			}
			if publisher != "" && slices.ContainsFunc(allowedPublishers, func(p string) bool {
				return strings.EqualFold(strings.TrimSpace(p), publisher)
			}) {
				return OutcomeAllowed
			}
			if assetID != "" && slices.ContainsFunc(allowedAssetIDs, func(allowed string) bool {
				return strings.Contains(strings.ToLower(assetID), strings.ToLower(allowed))
			}) {
				return OutcomeAllowed
			}
			if (len(allowedPublishers) > 0 && publisher == "") ||
				(len(allowedAssetIDs) > 0 && assetID == "") ||
				(len(allowedPublishers) == 0 && len(allowedAssetIDs) == 0 && (publisher == "" || assetID == "")) {
				return OutcomeIndeterminate
			}
			return OutcomeBlocked
		}

		patterns, ok := EffectiveStringSliceParam(assignment, legacyAllowedModelsSpec.ParameterName)
		if !ok {
			return OutcomeNotApplicable
		}
		// allowedModels evaluates underlying model names (e.g. "gpt-4o"), not deployment aliases.
		// When only the deployment alias is known locally, defer to ARM evaluateDeploymentPolicies.
		if modelName == "" {
			return OutcomeIndeterminate
		}
		matched := slices.ContainsFunc(patterns, func(pattern string) bool {
			return MatchesAzurePolicyLike(modelName, pattern)
		})
		if !matched {
			return OutcomeBlocked
		}
		return OutcomeAllowed
	}

	return OutcomeNotApplicable
}

// IsModelBlockedByInventory reports whether a Resource Graph policy assignment blocks model.
func IsModelBlockedByInventory(assignment PolicyAssignment, projectID string, model ModelCandidate) bool {
	return EvaluateModelInventoryAssignment(assignment, projectID, model) == OutcomeBlocked
}

// EvaluateModelInventoryPolicy evaluates Resource-Graph-backed model policy assignments
// and preserves OutcomeIndeterminate when an allowedModels policy cannot be verified
// without resolving the deployment's underlying model name.
func EvaluateModelInventoryPolicy(inventory Inventory, models []ModelCandidate) EvaluationResult {
	merged := MergeModelCandidates(models)
	if len(merged) == 0 {
		return EvaluationResult{Outcome: OutcomeAllowed}
	}
	sorted := SortAssignmentsByID(inventory.Assignments)
	var violations []Violation
	hasIndeterminate := false
	for _, model := range merged {
		targetLabel := strings.TrimSpace(model.Name)
		if targetLabel == "" {
			targetLabel = strings.TrimSpace(model.DeploymentName)
		}
		for _, assignment := range sorted {
			outcome := EvaluateModelInventoryAssignment(assignment, inventory.ProjectID, model)
			switch outcome {
			case OutcomeBlocked:
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
			case OutcomeIndeterminate:
				hasIndeterminate = true
			}
		}
	}
	if len(violations) > 0 {
		return EvaluationResult{
			Outcome:    OutcomeBlocked,
			Violations: violations,
		}
	}
	if hasIndeterminate {
		return EvaluationResult{
			Outcome:          OutcomeIndeterminate,
			IncompleteReason: "model deployment underlying model name could not be verified locally against allowed-models policy",
		}
	}
	return EvaluationResult{Outcome: OutcomeAllowed}
}

// EvaluateInventoryModelPolicy evaluates Resource-Graph-backed model policy assignments.
func EvaluateInventoryModelPolicy(inventory Inventory, models []ModelCandidate) []Violation {
	return EvaluateModelInventoryPolicy(inventory, models).Violations
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
	Format    string `json:"format"`
	Name      string `json:"name"`
	Version   string `json:"version"`
	Publisher string `json:"publisher,omitempty"`
	AssetID   string `json:"assetId,omitempty"`
	Source    string `json:"source,omitempty"`
}

type armGetDeploymentResponse struct {
	Properties *struct {
		Model *armDeploymentModel `json:"model,omitempty"`
	} `json:"properties,omitempty"`
}

type evaluateDeploymentPoliciesModel struct {
	Format  string `json:"format"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

type evaluateDeploymentPoliciesItem struct {
	Name       string `json:"name"`
	Properties struct {
		Model evaluateDeploymentPoliciesModel `json:"model"`
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
) (ModelPolicyEvaluation, error) {
	models = MergeModelCandidates(models)
	if e == nil || len(models) == 0 {
		return ModelPolicyEvaluation{}, nil
	}
	parsed, ok := ParseFoundryProjectID(projectResourceID)
	if !ok {
		return ModelPolicyEvaluation{}, fmt.Errorf("invalid Foundry project resource ID: %s", projectResourceID)
	}
	accountID := fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.CognitiveServices/accounts/%s",
		parsed.SubscriptionID,
		parsed.ResourceGroup,
		parsed.ProjectResourceName,
	)

	var items []evaluateDeploymentPoliciesItem
	var resolvedModels []ModelCandidate
	var incompleteReasons []string
	addReason := func(reason string) {
		reason = strings.TrimSpace(reason)
		if reason != "" && !slices.Contains(incompleteReasons, reason) {
			incompleteReasons = append(incompleteReasons, reason)
		}
	}

	seen := map[string]bool{}
	for _, m := range models {
		depName := strings.TrimSpace(m.DeploymentName)
		if depName == "" {
			depName = strings.TrimSpace(m.Name)
		}
		depKey := strings.ToLower(depName)
		if depName == "" || seen[depKey] {
			continue
		}
		seen[depKey] = true

		modelMeta := armDeploymentModel{
			Format:    strings.TrimSpace(m.Format),
			Name:      strings.TrimSpace(m.Name),
			Version:   strings.TrimSpace(m.Version),
			Publisher: strings.TrimSpace(m.Publisher),
			AssetID:   strings.TrimSpace(m.AssetID),
		}
		if modelMeta.Format == "" || modelMeta.Name == "" || modelMeta.Version == "" {
			fetched, err := e.getDeploymentModel(ctx, accountID, depName)
			if err != nil || fetched == nil ||
				strings.TrimSpace(fetched.Format) == "" ||
				strings.TrimSpace(fetched.Name) == "" ||
				strings.TrimSpace(fetched.Version) == "" {
				addReason(fmt.Sprintf("could not resolve model metadata for deployment %q", depName))
				resolvedModels = append(resolvedModels, m)
				continue
			}
			fetchedAssetID := strings.TrimSpace(fetched.AssetID)
			if fetchedAssetID == "" {
				fetchedAssetID = strings.TrimSpace(fetched.Source)
			}
			if fetchedAssetID == "" {
				fetchedAssetID = modelMeta.AssetID
			}
			fetchedPublisher := strings.TrimSpace(fetched.Publisher)
			if fetchedPublisher == "" {
				fetchedPublisher = modelMeta.Publisher
			}
			modelMeta = armDeploymentModel{
				Format:    strings.TrimSpace(fetched.Format),
				Name:      strings.TrimSpace(fetched.Name),
				Version:   strings.TrimSpace(fetched.Version),
				Publisher: fetchedPublisher,
				AssetID:   fetchedAssetID,
			}
		}

		resolvedModels = append(resolvedModels, ModelCandidate{
			DeploymentName: depName,
			Name:           modelMeta.Name,
			Format:         modelMeta.Format,
			Version:        modelMeta.Version,
			Publisher:      modelMeta.Publisher,
			AssetID:        modelMeta.AssetID,
		})
		item := evaluateDeploymentPoliciesItem{Name: depName}
		item.Properties.Model = evaluateDeploymentPoliciesModel{
			Format:  modelMeta.Format,
			Name:    modelMeta.Name,
			Version: modelMeta.Version,
		}
		items = append(items, item)
	}

	if len(items) == 0 {
		return ModelPolicyEvaluation{
			ResolvedModels:   resolvedModels,
			IncompleteReason: strings.Join(incompleteReasons, "; "),
		}, nil
	}

	reqURL := fmt.Sprintf(
		"%s%s/evaluateDeploymentPolicies?api-version=%s",
		e.armEndpoint,
		accountID,
		modelPolicyAPIVersion,
	)
	bodyBytes, err := json.Marshal(evaluateDeploymentPoliciesRequest{Deployments: items})
	if err != nil {
		return ModelPolicyEvaluation{ResolvedModels: resolvedModels},
			fmt.Errorf("marshal evaluateDeploymentPolicies request: %w", err)
	}

	req, err := runtime.NewRequest(ctx, http.MethodPost, reqURL)
	if err != nil {
		return ModelPolicyEvaluation{ResolvedModels: resolvedModels},
			fmt.Errorf("create evaluateDeploymentPolicies request: %w", err)
	}
	req.Raw().Header.Set("Accept", "application/json")
	if err := req.SetBody(streaming.NopCloser(bytes.NewReader(bodyBytes)), "application/json"); err != nil {
		return ModelPolicyEvaluation{ResolvedModels: resolvedModels},
			fmt.Errorf("set evaluateDeploymentPolicies body: %w", err)
	}

	resp, err := e.pipeline.Do(req)
	if err != nil {
		return ModelPolicyEvaluation{ResolvedModels: resolvedModels},
			fmt.Errorf("send evaluateDeploymentPolicies request: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return ModelPolicyEvaluation{ResolvedModels: resolvedModels},
			fmt.Errorf("read evaluateDeploymentPolicies response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ModelPolicyEvaluation{ResolvedModels: resolvedModels},
			fmt.Errorf("evaluateDeploymentPolicies returned HTTP %d", resp.StatusCode)
	}

	var parsedResp evaluateDeploymentPoliciesResponse
	if err := json.Unmarshal(respBytes, &parsedResp); err != nil {
		return ModelPolicyEvaluation{ResolvedModels: resolvedModels},
			fmt.Errorf("decode evaluateDeploymentPolicies response: %w", err)
	}

	var violations []Violation
	for _, item := range items {
		res, exists := parsedResp.Results[item.Name]
		if !exists {
			for k, v := range parsedResp.Results {
				if strings.EqualFold(k, item.Name) {
					res = v
					exists = true
					break
				}
			}
		}
		if !exists {
			addReason(fmt.Sprintf("evaluateDeploymentPolicies response omitted deployment %q", item.Name))
			continue
		}
		targetVal := item.Properties.Model.Name
		if item.Properties.Model.Version != "" {
			targetVal = fmt.Sprintf("%s (version %s)", item.Properties.Model.Name, item.Properties.Model.Version)
		}
		seenAssignments := map[string]bool{}
		denyCount := 0
		auditCount := 0
		for _, a := range res.NonCompliantAssignments {
			effect := strings.TrimSpace(a.Effect)
			if strings.EqualFold(effect, "audit") {
				auditCount++
				continue
			}
			if !strings.EqualFold(effect, "deny") {
				continue
			}
			denyCount++
			rawAssignmentID := strings.TrimSpace(a.AssignmentID)
			normalizedID := NormalizeScope(rawAssignmentID)
			if normalizedID != "" && seenAssignments[normalizedID] {
				continue
			}
			if normalizedID != "" {
				seenAssignments[normalizedID] = true
			}

			policyName := "Azure Policy assignment"
			if rawAssignmentID != "" {
				parts := strings.Split(strings.TrimRight(rawAssignmentID, "/"), "/")
				if last := parts[len(parts)-1]; last != "" {
					policyName = last
				}
			}
			msg := fmt.Sprintf(
				"The %q policy doesn't allow model %q in this project.",
				policyName,
				targetVal,
			)
			if errMsg := strings.TrimSpace(res.ErrorMessage); errMsg != "" &&
				(len(res.NonCompliantAssignments) == 1 ||
					(rawAssignmentID != "" &&
						strings.Contains(strings.ToLower(errMsg), strings.ToLower(rawAssignmentID)))) {
				msg = errMsg
			}
			violations = append(violations, Violation{
				Target:       TargetModel,
				TargetValue:  targetVal,
				AssignmentID: normalizedID,
				DisplayName:  policyName,
				PortalURL:    BuildPolicyAssignmentPortalURL(projectResourceID, rawAssignmentID),
				Message:      msg,
			})
		}

		outcome := strings.TrimSpace(res.EvaluationOutcome)
		switch {
		case strings.EqualFold(outcome, "Compliant"):
			// Explicitly compliant.
		case strings.EqualFold(outcome, "NonCompliant"):
			if denyCount == 0 && auditCount == 0 {
				addReason(fmt.Sprintf(
					"evaluateDeploymentPolicies returned NonCompliant for deployment %q without a recognized Deny or Audit assignment",
					item.Name,
				))
			}
		default:
			if denyCount == 0 {
				addReason(fmt.Sprintf(
					"evaluateDeploymentPolicies returned unrecognized outcome %q for deployment %q",
					outcome,
					item.Name,
				))
			}
		}
	}
	return ModelPolicyEvaluation{
		Violations:       violations,
		ResolvedModels:   resolvedModels,
		IncompleteReason: strings.Join(incompleteReasons, "; "),
	}, nil
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
