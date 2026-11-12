// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore nextgen noncompliant managementgroups

package provisioning

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"azure.ai.projects/internal/azure"
	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/google/uuid"
)

const (
	// SkipModelPolicyChecksEnvKey opts out of client-side Azure Policy pre-checks.
	SkipModelPolicyChecksEnvKey = "AZD_AI_AGENT_SKIP_POLICY_CHECKS"
	// LegacySkipModelPolicyChecksEnvKey is the legacy opt-out environment variable.
	LegacySkipModelPolicyChecksEnvKey = "AZD_AGENT_SKIP_POLICY_CHECKS"

	defaultARMEndpoint                    = "https://management.azure.com"
	cognitiveServicesDeploymentAPIVersion = "2025-10-01-preview"
	evaluateDeploymentPoliciesAPIVersion  = "2026-03-15-preview"
	defaultFoundryGovernPortalURL         = "https://ai.azure.com/?flight=govern_ignite"
	modelPolicyEvaluationTimeout          = 30 * time.Second
	maxDeploymentsPerPolicyBatch          = 100
)

var (
	foundryProjectResourceIDPattern = regexp.MustCompile(
		`(?i)^/subscriptions/([^/?#\\\s@]+)/resourceGroups/([^/?#\\\s@]+)/` +
			`providers/Microsoft\.CognitiveServices/accounts/([^/?#\\\s@]+)/projects/([^/?#\\\s@]+)$`,
	)
	policyAssignmentSuffixPattern = regexp.MustCompile(
		`(?i)/providers/Microsoft\.Authorization/policyAssignments/[^/]+$`,
	)
	managementGroupScopePattern = regexp.MustCompile(
		`(?i)^/providers/Microsoft\.Management/managementGroups/[^/]+$`,
	)
	subscriptionScopeHierarchyPattern = regexp.MustCompile(
		`(?i)^/subscriptions/([^/]+)(?:/resourceGroups/[^/]+(?:/providers/[^/]+(?:/[^/]+/[^/]+)+)?)?$`,
	)

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
	keys := []string{SkipModelPolicyChecksEnvKey, LegacySkipModelPolicyChecksEnvKey}
	for _, key := range keys {
		if val := strings.TrimSpace(env[key]); val != "" {
			if skip, err := strconv.ParseBool(val); err == nil {
				return skip
			}
		}
	}
	for _, key := range keys {
		if val := strings.TrimSpace(os.Getenv(key)); val != "" {
			if skip, err := strconv.ParseBool(val); err == nil {
				return skip
			}
		}
	}
	return false
}

// ParseFoundryProjectResourceID parses and validates a Microsoft Foundry project ARM resource ID.
func ParseFoundryProjectResourceID(projectID string) (subID, rg, account, project string, ok bool) {
	raw := strings.TrimSpace(projectID)
	if raw == "" || !strings.HasPrefix(raw, "/") || strings.Contains(raw, "//") {
		return "", "", "", "", false
	}
	trimmed := strings.TrimSuffix(raw, "/")
	matches := foundryProjectResourceIDPattern.FindStringSubmatch(trimmed)
	if len(matches) != 5 {
		return "", "", "", "", false
	}
	if _, err := uuid.Parse(matches[1]); err != nil {
		return "", "", "", "", false
	}
	return matches[1], matches[2], matches[3], matches[4], true
}

func isValidModelPolicyAssignmentID(assignmentID string) bool {
	raw := strings.TrimSpace(assignmentID)
	if raw == "" || !strings.HasPrefix(raw, "/") ||
		strings.Contains(raw, "//") ||
		strings.ContainsAny(raw, "?#@\\ \t\r\n") {
		return false
	}
	trimmed := strings.TrimSuffix(raw, "/")
	resID, err := arm.ParseResourceID(trimmed)
	if err != nil || resID == nil ||
		!strings.EqualFold(resID.ResourceType.String(), "Microsoft.Authorization/policyAssignments") ||
		strings.TrimSpace(resID.Name) == "" {
		return false
	}
	loc := policyAssignmentSuffixPattern.FindStringIndex(trimmed)
	if loc == nil || loc[0] == 0 {
		return false
	}
	scope := trimmed[:loc[0]]
	if strings.HasPrefix(strings.ToLower(scope), "/providers/microsoft.management/managementgroups/") {
		return managementGroupScopePattern.MatchString(scope)
	}
	subMatch := subscriptionScopeHierarchyPattern.FindStringSubmatch(scope)
	if len(subMatch) != 2 || uuid.Validate(subMatch[1]) != nil {
		return false
	}
	if strings.EqualFold(scope, "/subscriptions/"+subMatch[1]) {
		return true
	}
	scopeRes, err := arm.ParseResourceID(scope)
	return err == nil && scopeRes != nil && scopeRes.ResourceGroupName != ""
}

func isValidPolicyAssignmentResourceID(assignmentID string) bool {
	return isValidModelPolicyAssignmentID(assignmentID)
}

func lastSegment(resourceID string) string {
	trimmed := strings.TrimSuffix(strings.TrimSpace(resourceID), "/")
	if trimmed == "" {
		return ""
	}
	parts := strings.Split(trimmed, "/")
	return parts[len(parts)-1]
}

// BuildModelPolicyAssignmentPortalURL constructs the Foundry Portal Manage -> Govern deep link
// for a blocking Azure Policy assignment. Falls back to the generic Foundry Govern URL when
// projectResourceID or assignmentID cannot be validated.
func BuildModelPolicyAssignmentPortalURL(projectResourceID, assignmentID string) string {
	subID, rg, account, project, ok := ParseFoundryProjectResourceID(projectResourceID)
	rawAssignment := strings.TrimSpace(assignmentID)
	if !ok || !isValidPolicyAssignmentResourceID(rawAssignment) {
		return defaultFoundryGovernPortalURL
	}
	trimmedAssignment := strings.TrimSuffix(rawAssignment, "/")

	parsedUUID, err := uuid.Parse(subID)
	if err != nil {
		return defaultFoundryGovernPortalURL
	}
	base64Sub := url.PathEscape(base64.RawURLEncoding.EncodeToString(parsedUUID[:]))
	route := fmt.Sprintf(
		"%s%%2C%s%%2C%%2C%s%%2C%s",
		base64Sub,
		url.PathEscape(rg),
		url.PathEscape(account),
		url.PathEscape(project),
	)
	assignmentKey := base64.RawURLEncoding.EncodeToString([]byte(trimmedAssignment))

	trimmedProjectID := strings.TrimSuffix(strings.TrimSpace(projectResourceID), "/")
	query := url.Values{}
	query.Set("subscriptionId", subID)
	query.Set(
		"resourceGroupId",
		fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", subID, rg),
	)
	query.Set("projectId", trimmedProjectID)
	query.Set("flight", "govern_ignite")

	rawPath := fmt.Sprintf("/nextgen/r/%s/manage/govern/assignments/%s", route, assignmentKey)
	encodedQuery := strings.ReplaceAll(query.Encode(), "+", "%20")
	return fmt.Sprintf("https://ai.azure.com%s?%s", rawPath, encodedQuery)
}

func defaultModelPolicyPipeline(cred azcore.TokenCredential) runtime.Pipeline {
	armOpts := azure.NewArmClientOptions()
	clientOpts := armOpts.ClientOptions
	if cred != nil {
		clientOpts.PerCallPolicies = append(
			[]policy.Policy{
				runtime.NewBearerTokenPolicy(cred, []string{defaultARMEndpoint + "/.default"}, nil),
			},
			clientOpts.PerCallPolicies...,
		)
	}
	return runtime.NewPipeline("azure-ai-projects-policy", "v1.0.0", runtime.PipelineOptions{}, &clientOpts)
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

type armGetDeploymentResponse struct {
	Properties *struct {
		Model *evaluateDeploymentPoliciesModel `json:"model,omitempty"`
	} `json:"properties,omitempty"`
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
	subID string,
	rg string,
	account string,
	deployments []synthesis.Deployment,
) ([]ModelPolicyViolation, error) {
	evalCtx, cancel := context.WithTimeout(ctx, modelPolicyEvaluationTimeout)
	defer cancel()

	if strings.TrimSpace(armEndpoint) == "" {
		armEndpoint = defaultARMEndpoint
	}
	armEndpoint = strings.TrimRight(strings.TrimSpace(armEndpoint), "/")

	accountPath := fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.CognitiveServices/accounts/%s",
		url.PathEscape(subID),
		url.PathEscape(rg),
		url.PathEscape(account),
	)

	items := make([]evaluateDeploymentPoliciesItem, 0, len(deployments))
	for _, dep := range deployments {
		format := strings.TrimSpace(dep.Model.Format)
		name := strings.TrimSpace(dep.Model.Name)
		version := strings.TrimSpace(dep.Model.Version)
		if format == "" || name == "" || version == "" {
			fetched, err := fetchDeploymentModelMetadata(evalCtx, pipeline, armEndpoint, accountPath, dep.Name)
			if err != nil {
				log.Printf("[FoundryPolicy] Could not resolve model metadata for deployment %q: %v", dep.Name, err)
				continue
			}
			if fetched != nil {
				fetchedFormat := strings.TrimSpace(fetched.Format)
				fetchedName := strings.TrimSpace(fetched.Name)
				fetchedVersion := strings.TrimSpace(fetched.Version)
				if (format == "" || strings.EqualFold(format, fetchedFormat)) &&
					(name == "" || strings.EqualFold(name, fetchedName)) {
					if format == "" {
						format = fetchedFormat
					}
					if name == "" {
						name = fetchedName
					}
					if version == "" {
						version = fetchedVersion
					}
				}
			}
		}
		if format == "" || name == "" || version == "" {
			log.Printf("[FoundryPolicy] Skipping deployment %q with incomplete model metadata", dep.Name)
			continue
		}
		item := evaluateDeploymentPoliciesItem{Name: dep.Name}
		item.Properties.Model = evaluateDeploymentPoliciesModel{
			Format:  format,
			Name:    name,
			Version: version,
		}
		items = append(items, item)
	}

	if len(items) == 0 {
		return nil, nil
	}

	var violations []ModelPolicyViolation
	for start := 0; start < len(items); start += maxDeploymentsPerPolicyBatch {
		batch := items[start:min(start+maxDeploymentsPerPolicyBatch, len(items))]
		batchViolations, err := evaluateDeploymentBatch(
			evalCtx,
			pipeline,
			armEndpoint,
			accountPath,
			projectResourceID,
			batch,
		)
		if err != nil {
			return violations, err
		}
		violations = append(violations, batchViolations...)
	}
	return violations, nil
}

func fetchDeploymentModelMetadata(
	ctx context.Context,
	pipeline runtime.Pipeline,
	armEndpoint string,
	accountPath string,
	deploymentName string,
) (*evaluateDeploymentPoliciesModel, error) {
	reqURL := fmt.Sprintf(
		"%s%s/deployments/%s?api-version=%s",
		armEndpoint,
		accountPath,
		url.PathEscape(deploymentName),
		cognitiveServicesDeploymentAPIVersion,
	)
	req, err := runtime.NewRequest(ctx, http.MethodGet, reqURL)
	if err != nil {
		return nil, err
	}
	req.Raw().Header.Set("Accept", "application/json")
	resp, err := pipeline.Do(req)
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

func evaluateDeploymentBatch(
	ctx context.Context,
	pipeline runtime.Pipeline,
	armEndpoint string,
	accountPath string,
	projectResourceID string,
	batch []evaluateDeploymentPoliciesItem,
) ([]ModelPolicyViolation, error) {
	reqURL := fmt.Sprintf(
		"%s%s/evaluateDeploymentPolicies?api-version=%s",
		armEndpoint,
		accountPath,
		evaluateDeploymentPoliciesAPIVersion,
	)
	bodyBytes, err := json.Marshal(evaluateDeploymentPoliciesRequest{Deployments: batch})
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

	resp, err := pipeline.Do(req)
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

	var violations []ModelPolicyViolation
	for _, item := range batch {
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
		if !exists || !strings.EqualFold(strings.TrimSpace(res.EvaluationOutcome), "NonCompliant") {
			continue
		}

		modelLabel := item.Properties.Model.Name
		if item.Properties.Model.Version != "" {
			modelLabel = fmt.Sprintf("%s (version %s)", item.Properties.Model.Name, item.Properties.Model.Version)
		}

		seenAssignments := map[string]bool{}
		for _, a := range res.NonCompliantAssignments {
			if !strings.EqualFold(strings.TrimSpace(a.Effect), "deny") {
				continue
			}
			rawAssignmentID := strings.TrimSpace(a.AssignmentID)
			trimmedAssignmentID := strings.TrimSuffix(rawAssignmentID, "/")
			normalizedID := strings.ToLower(trimmedAssignmentID)
			if normalizedID != "" && seenAssignments[normalizedID] {
				continue
			}
			if normalizedID != "" {
				seenAssignments[normalizedID] = true
			}

			displayName := "Azure Policy"
			validAssignmentID := ""
			if isValidModelPolicyAssignmentID(rawAssignmentID) {
				validAssignmentID = trimmedAssignmentID
				if seg := lastSegment(trimmedAssignmentID); seg != "" {
					displayName = seg
				}
			}

			msg := fmt.Sprintf(
				"The %q policy doesn't allow model %q for deployment %q in this project.",
				displayName,
				modelLabel,
				item.Name,
			)
			violations = append(violations, ModelPolicyViolation{
				DeploymentName: item.Name,
				ModelFormat:    item.Properties.Model.Format,
				ModelName:      item.Properties.Model.Name,
				ModelVersion:   item.Properties.Model.Version,
				AssignmentID:   validAssignmentID,
				DisplayName:    displayName,
				PortalURL:      BuildModelPolicyAssignmentPortalURL(projectResourceID, validAssignmentID),
				Message:        msg,
			})
		}
	}
	return violations, nil
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
