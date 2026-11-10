// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore endswith policyresources policyassignments policydefinitions policyexemptions resourcecontainers
// cspell:ignore skipToken donotenforce policyeffect notScopes nextgen unpadded noncompliant tolower strcat tostring todatetime isempty

package cmd

import (
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
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

	"azure.ai.toolboxes/internal/exterrors"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
)

const (
	skipToolboxPolicyPreflightEnvKey       = "AZD_AI_AGENT_SKIP_POLICY_CHECKS"
	legacySkipToolboxPolicyPreflightEnvKey = "AZD_AGENT_SKIP_POLICY_CHECKS"

	toolboxDeniedToolsPolicyID       = "/providers/microsoft.authorization/policydefinitions/7740c22b-07ef-4bac-9f5a-e38d188f80b7"
	toolboxAllowedToolsPolicyID      = "/providers/microsoft.authorization/policydefinitions/e7352ad2-6c86-4a07-9573-47f67b557ba8"
	toolboxDeniedToolsDisplaySuffix  = "microsoft foundry agents and toolboxes should not use denied tools"
	toolboxLegacyDeniedToolsSuffix   = "microsoft foundry agents should not use denied tools"
	toolboxAllowedToolsDisplaySuffix = "microsoft foundry agents and toolboxes should only use approved tools"
	toolboxLegacyAllowedToolsSuffix  = "microsoft foundry agents should only use allowed tools"
	toolboxPolicyExemptionMarker     = "/providers/microsoft.authorization/policyexemptions/"
	toolboxResourceGraphAPIVersion   = "2024-04-01"
	toolboxAuthorizationScopeFilter  = "AtScopeAboveAndBelow"
	toolboxDefaultARMEndpoint        = "https://management.azure.com"
	toolboxDefaultGovernPortalURL    = "https://ai.azure.com/?flight=govern_ignite"
	toolboxPolicyPreflightTimeout    = 30 * time.Second
)

var (
	toolboxProjectIDRegex = regexp.MustCompile(
		`(?i)/subscriptions/([^/?#]+)/resourceGroups/([^/?#]+)/providers/[^/?#]+/accounts/([^/?#]+)/projects/([^/?#]+)$`,
	)
	toolboxHexGUIDRegex         = regexp.MustCompile(`(?i)^[0-9a-f]{32}$`)
	toolboxPolicyAssignmentIDRe = regexp.MustCompile(
		`(?i)^(?:/subscriptions/[^/?#\s]+(?:/[^/?#\s]+)*|` +
			`/providers/Microsoft\.Management/managementGroups/[^/?#\s]+)/providers/Microsoft\.Authorization/policyAssignments/[^/?#\s]+$`,
	)
	toolboxRuntimePolicyDeniedRe = regexp.MustCompile(`(?i)noncompliant with policy (/[^\s:]+):`)
)

type toolboxResourceGraphOptions struct {
	AuthorizationScopeFilter string `json:"authorizationScopeFilter,omitempty"`
	SkipToken                string `json:"$skipToken,omitempty"`
}

type toolboxResourceGraphRequest struct {
	Subscriptions []string                     `json:"subscriptions"`
	Query         string                       `json:"query"`
	Options       *toolboxResourceGraphOptions `json:"options,omitempty"`
}

type toolboxPolicyQueryFunc func(ctx context.Context, req toolboxResourceGraphRequest) ([]json.RawMessage, error)

type toolboxPolicyAssignment struct {
	AssignmentID          string         `json:"assignmentId"`
	AssignmentScope       string         `json:"assignmentScope,omitempty"`
	PolicyDefinitionID    string         `json:"policyDefinitionId,omitempty"`
	AssignmentDisplayName string         `json:"assignmentDisplayName,omitempty"`
	AssignmentName        string         `json:"assignmentName,omitempty"`
	AssignmentParameters  map[string]any `json:"assignmentParameters,omitempty"`
	AssignmentOverrides   []any          `json:"assignmentOverrides,omitempty"`
	ResourceSelectors     []any          `json:"resourceSelectors,omitempty"`
	DefinitionDisplayName string         `json:"definitionDisplayName,omitempty"`
	DefinitionParameters  map[string]any `json:"definitionParameters,omitempty"`
	DefinitionEffect      any            `json:"definitionEffect,omitempty"`
	EnforcementMode       string         `json:"enforcementMode,omitempty"`
	NotScopes             []string       `json:"notScopes,omitempty"`
	ManagementGroupScopes []string       `json:"-"`
	IsExempt              bool           `json:"-"`
}

type toolboxPolicyViolation struct {
	ToolLabel    string
	AssignmentID string
	DisplayName  string
	PortalURL    string
	Message      string
}

func shouldSkipToolboxPolicyChecks(env map[string]string) bool {
	keys := []string{skipToolboxPolicyPreflightEnvKey, legacySkipToolboxPolicyPreflightEnvKey}
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

func normalizeToolboxPolicyScope(scope string) string {
	return strings.ToLower(strings.TrimRight(strings.TrimSpace(scope), "/"))
}

func isValidToolboxPolicyAssignmentID(assignmentID string) bool {
	trimmed := strings.TrimRight(strings.TrimSpace(assignmentID), "/")
	return trimmed != "" && toolboxPolicyAssignmentIDRe.MatchString(trimmed)
}

func buildToolboxPolicyPortalURL(projectResourceID, assignmentID string) string {
	trimmedProject := strings.TrimSpace(projectResourceID)
	trimmedAssignment := strings.TrimRight(strings.TrimSpace(assignmentID), "/")
	if trimmedProject == "" || !isValidToolboxPolicyAssignmentID(trimmedAssignment) {
		return toolboxDefaultGovernPortalURL
	}
	matches := toolboxProjectIDRegex.FindStringSubmatch(trimmedProject)
	if len(matches) != 5 {
		return toolboxDefaultGovernPortalURL
	}
	subID, rg, account, project := matches[1], matches[2], matches[3], matches[4]

	cleanHex := strings.ReplaceAll(subID, "-", "")
	encodedSub := base64.RawURLEncoding.EncodeToString([]byte(subID))
	if toolboxHexGUIDRegex.MatchString(cleanHex) {
		if rawBytes, err := hex.DecodeString(cleanHex); err == nil {
			encodedSub = base64.RawURLEncoding.EncodeToString(rawBytes)
		}
	}

	route := fmt.Sprintf(
		"%s%%2C%s%%2C%%2C%s%%2C%s",
		url.PathEscape(encodedSub),
		url.PathEscape(rg),
		url.PathEscape(account),
		url.PathEscape(project),
	)
	assignmentKey := base64.RawURLEncoding.EncodeToString([]byte(trimmedAssignment))

	query := url.Values{}
	query.Set("subscriptionId", subID)
	query.Set("resourceGroupId", fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", subID, rg))
	query.Set("projectId", trimmedProject)
	query.Set("flight", "govern_ignite")

	rawPath := fmt.Sprintf("/nextgen/r/%s/manage/govern/assignments/%s", route, assignmentKey)
	encodedQuery := strings.ReplaceAll(query.Encode(), "+", "%20")
	return fmt.Sprintf("https://ai.azure.com%s?%s", rawPath, encodedQuery)
}

func escapeToolboxKustoString(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}

func buildToolboxMGQuery(subscriptionID string) string {
	escaped := escapeToolboxKustoString(strings.ToLower(strings.TrimSpace(subscriptionID)))
	return fmt.Sprintf(`
resourcecontainers
| where type =~ 'microsoft.resources/subscriptions'
| where subscriptionId =~ '%s'
| mv-expand ancestor = properties.managementGroupAncestorsChain
| project scope = tolower(strcat('/providers/Microsoft.Management/managementGroups/', tostring(ancestor.name)))
| distinct scope
| order by scope asc`, escaped)
}

func buildToolboxPolicyAssignmentsQuery() string {
	return fmt.Sprintf(`
policyresources
| where type =~ 'microsoft.authorization/policyassignments'
| extend
    assignmentId = tolower(id),
    assignmentScope = tolower(tostring(properties.scope)),
    policyDefinitionId = tolower(tostring(properties.policyDefinitionId))
| join kind=inner (
    policyresources
    | where type =~ 'microsoft.authorization/policydefinitions'
    | extend definitionDisplayName = tostring(properties.displayName)
    | extend policyDefinitionId = tolower(id)
    | where policyDefinitionId in~ (
            '%s',
            '%s'
        )
        or tolower(definitionDisplayName) endswith '%s'
        or tolower(definitionDisplayName) endswith '%s'
        or tolower(definitionDisplayName) endswith '%s'
        or tolower(definitionDisplayName) endswith '%s'
    | project
        policyDefinitionId,
        definitionDisplayName,
        definitionParameters = properties.parameters,
        definitionEffect = properties.policyRule.then.effect
) on policyDefinitionId
| project
    assignmentId,
    assignmentScope,
    policyDefinitionId,
    assignmentDisplayName = tostring(properties.displayName),
    assignmentName = name,
    assignmentParameters = properties.parameters,
    assignmentOverrides = properties.overrides,
    resourceSelectors = properties.resourceSelectors,
    definitionDisplayName,
    definitionParameters,
    definitionEffect,
    enforcementMode = tostring(properties.enforcementMode),
    notScopes = properties.notScopes
| order by assignmentId asc`,
		toolboxDeniedToolsPolicyID,
		toolboxAllowedToolsPolicyID,
		toolboxDeniedToolsDisplaySuffix,
		toolboxLegacyDeniedToolsSuffix,
		toolboxAllowedToolsDisplaySuffix,
		toolboxLegacyAllowedToolsSuffix,
	)
}

func buildToolboxExemptionsQuery(assignmentIDs []string) string {
	quoted := make([]string, 0, len(assignmentIDs))
	for _, id := range assignmentIDs {
		quoted = append(quoted, fmt.Sprintf("'%s'", escapeToolboxKustoString(normalizeToolboxPolicyScope(id))))
	}
	list := strings.Join(quoted, ",\n        ")
	return fmt.Sprintf(`
policyresources
| where type =~ 'microsoft.authorization/policyexemptions'
| extend
    policyAssignmentId = tolower(tostring(properties.policyAssignmentId)),
    expiresOnValue = tostring(properties.expiresOn)
| where policyAssignmentId in~ (
        %s
    )
| where isempty(expiresOnValue) or todatetime(expiresOnValue) > now()
| project
    exemptionId = tolower(id),
    policyAssignmentId,
    expiresOn = expiresOnValue
| order by exemptionId asc`, list)
}

func newToolboxARMPipeline(cred azcore.TokenCredential) runtime.Pipeline {
	clientOpts := policy.ClientOptions{
		PerCallPolicies: []policy.Policy{
			runtime.NewBearerTokenPolicy(cred, []string{toolboxDefaultARMEndpoint + "/.default"}, nil),
		},
	}
	return runtime.NewPipeline("azure-ai-toolboxes-policy", "v1.0.0", runtime.PipelineOptions{}, &clientOpts)
}

func newToolboxResourceGraphQueryFunc(pipeline runtime.Pipeline) toolboxPolicyQueryFunc {
	resourceGraphURL := fmt.Sprintf(
		"%s/providers/Microsoft.ResourceGraph/resources?api-version=%s",
		toolboxDefaultARMEndpoint,
		toolboxResourceGraphAPIVersion,
	)
	return func(ctx context.Context, request toolboxResourceGraphRequest) ([]json.RawMessage, error) {
		var rows []json.RawMessage
		seenSkipTokens := map[string]bool{}
		var skipToken string

		for {
			pageReq := request
			if skipToken != "" {
				opts := toolboxResourceGraphOptions{SkipToken: skipToken}
				if request.Options != nil {
					opts.AuthorizationScopeFilter = request.Options.AuthorizationScopeFilter
				}
				pageReq.Options = &opts
			}
			bodyBytes, err := json.Marshal(pageReq)
			if err != nil {
				return nil, err
			}
			httpReq, err := runtime.NewRequest(ctx, http.MethodPost, resourceGraphURL)
			if err != nil {
				return nil, err
			}
			httpReq.Raw().Header.Set("Accept", "application/json")
			if err := httpReq.SetBody(streaming.NopCloser(bytes.NewReader(bodyBytes)), "application/json"); err != nil {
				return nil, err
			}
			resp, err := pipeline.Do(httpReq)
			if err != nil {
				return nil, err
			}
			respBytes, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if readErr != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
				return nil, fmt.Errorf("Resource Graph query failed (HTTP %d)", resp.StatusCode)
			}
			if bytes.Equal(bytes.TrimSpace(respBytes), []byte("null")) {
				return nil, errors.New("Resource Graph response body is null")
			}

			var parsed struct {
				Data            json.RawMessage `json:"data"`
				DollarSkipToken string          `json:"$skipToken"`
				SkipToken       string          `json:"skipToken"`
				ResultTruncated any             `json:"resultTruncated"`
			}
			if err := json.Unmarshal(respBytes, &parsed); err != nil {
				return nil, err
			}
			pageRows, err := parseToolboxResourceGraphRows(parsed.Data)
			if err != nil {
				return nil, err
			}
			rows = append(rows, pageRows...)

			nextToken := strings.TrimSpace(parsed.DollarSkipToken)
			if nextToken == "" {
				nextToken = strings.TrimSpace(parsed.SkipToken)
			}
			if nextToken == "" {
				if isToolboxResourceGraphTruncated(parsed.ResultTruncated) {
					return nil, errors.New("Resource Graph response was truncated without a continuation token")
				}
				return rows, nil
			}
			if seenSkipTokens[nextToken] {
				return nil, errors.New("repeated Resource Graph continuation token")
			}
			seenSkipTokens[nextToken] = true
			skipToken = nextToken
		}
	}
}

func isToolboxResourceGraphTruncated(v any) bool {
	switch typed := v.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true")
	default:
		return false
	}
}

func parseToolboxResourceGraphRows(rawData json.RawMessage) ([]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(rawData)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, errors.New("Resource Graph response missing data array")
	}
	switch trimmed[0] {
	case '[':
		var rows []json.RawMessage
		if err := json.Unmarshal(trimmed, &rows); err != nil || rows == nil {
			return nil, errors.New("invalid Resource Graph data array")
		}
		return rows, nil
	case '{':
		var tabular struct {
			Rows *[]json.RawMessage `json:"rows"`
		}
		if err := json.Unmarshal(trimmed, &tabular); err != nil || tabular.Rows == nil || *tabular.Rows == nil {
			return nil, errors.New("invalid Resource Graph data.rows array")
		}
		return *tabular.Rows, nil
	default:
		return nil, errors.New("unexpected Resource Graph data JSON type")
	}
}

func queryToolboxResourceGraphTyped[T any](
	ctx context.Context,
	queryFn toolboxPolicyQueryFunc,
	req toolboxResourceGraphRequest,
) ([]T, error) {
	rawRows, err := queryFn(ctx, req)
	if err != nil {
		return nil, err
	}
	out := make([]T, 0, len(rawRows))
	for _, raw := range rawRows {
		var item T
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func isToolboxScopeApplicable(scope, projectID string, mgScopes []string) bool {
	normScope := normalizeToolboxPolicyScope(scope)
	normProj := normalizeToolboxPolicyScope(projectID)
	if normScope == "" || normProj == "" {
		return false
	}
	if normProj == normScope || strings.HasPrefix(normProj, normScope+"/") {
		return true
	}
	for _, mg := range mgScopes {
		if normalizeToolboxPolicyScope(mg) == normScope {
			return true
		}
	}
	return false
}

func getToolboxParamValue(parameters map[string]any, name, field string) any {
	if parameters == nil {
		return nil
	}
	raw, ok := parameters[name]
	if !ok {
		return nil
	}
	paramMap, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	return paramMap[field]
}

func getToolboxEffectiveEffect(a toolboxPolicyAssignment) string {
	val := getToolboxParamValue(a.AssignmentParameters, "effect", "value")
	if val == nil {
		val = getToolboxParamValue(a.DefinitionParameters, "effect", "defaultValue")
	}
	if val == nil {
		val = a.DefinitionEffect
	}
	s, ok := val.(string)
	if !ok {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(s))
}

func hasToolboxPolicyEffectOverride(a toolboxPolicyAssignment) bool {
	for _, item := range a.AssignmentOverrides {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if kind, ok := m["kind"].(string); ok && strings.EqualFold(strings.TrimSpace(kind), "policyeffect") {
			return true
		}
	}
	return false
}

func isToolboxAssignmentApplicable(a toolboxPolicyAssignment, projectID string) bool {
	if strings.EqualFold(strings.TrimSpace(a.EnforcementMode), "donotenforce") ||
		a.IsExempt ||
		hasToolboxPolicyEffectOverride(a) ||
		len(a.ResourceSelectors) > 0 ||
		getToolboxEffectiveEffect(a) != "deny" {
		return false
	}
	if a.AssignmentScope != "" && !isToolboxScopeApplicable(a.AssignmentScope, projectID, a.ManagementGroupScopes) {
		return false
	}
	for _, notScope := range a.NotScopes {
		if isToolboxScopeApplicable(notScope, projectID, a.ManagementGroupScopes) {
			return false
		}
	}
	return true
}

func toolboxEffectiveStringSliceParam(a toolboxPolicyAssignment, paramName string) ([]string, bool) {
	val := getToolboxParamValue(a.AssignmentParameters, paramName, "value")
	if val == nil {
		val = getToolboxParamValue(a.DefinitionParameters, paramName, "defaultValue")
	}
	if val == nil {
		return nil, false
	}
	switch typed := val.(type) {
	case []string:
		return slices.Clone(typed), true
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out, true
	default:
		return nil, false
	}
}

func matchesToolboxAzurePolicyLike(value, pattern string) bool {
	var expr strings.Builder
	expr.WriteString("(?i)^")
	for _, ch := range strings.TrimSpace(pattern) {
		if ch == '*' {
			expr.WriteString(".*")
		} else {
			expr.WriteString(regexp.QuoteMeta(string(ch)))
		}
	}
	expr.WriteString("$")
	re, err := regexp.Compile(expr.String())
	if err != nil {
		return false
	}
	return re.MatchString(strings.TrimSpace(value))
}

func toolboxAssignmentDisplayName(a toolboxPolicyAssignment) string {
	if s := strings.TrimSpace(a.AssignmentDisplayName); s != "" {
		return s
	}
	if s := strings.TrimSpace(a.AssignmentName); s != "" {
		return s
	}
	if s := strings.TrimSpace(a.DefinitionDisplayName); s != "" {
		return s
	}
	return "Azure Policy assignment"
}

func isDeniedToolsAssignment(a toolboxPolicyAssignment) bool {
	if normalizeToolboxPolicyScope(a.PolicyDefinitionID) == normalizeToolboxPolicyScope(toolboxDeniedToolsPolicyID) {
		return true
	}
	display := strings.ToLower(strings.TrimSpace(a.DefinitionDisplayName))
	return strings.HasSuffix(display, toolboxDeniedToolsDisplaySuffix) ||
		strings.HasSuffix(display, toolboxLegacyDeniedToolsSuffix)
}

func isAllowedToolsAssignment(a toolboxPolicyAssignment) bool {
	if normalizeToolboxPolicyScope(a.PolicyDefinitionID) == normalizeToolboxPolicyScope(toolboxAllowedToolsPolicyID) {
		return true
	}
	display := strings.ToLower(strings.TrimSpace(a.DefinitionDisplayName))
	return strings.HasSuffix(display, toolboxAllowedToolsDisplaySuffix) ||
		strings.HasSuffix(display, toolboxLegacyAllowedToolsSuffix)
}

func isToolboxToolBlocked(a toolboxPolicyAssignment, projectID string, tool map[string]any) bool {
	if !isToolboxAssignmentApplicable(a, projectID) {
		return false
	}
	toolType, _ := tool["type"].(string)
	toolType = strings.TrimSpace(toolType)
	if toolType == "" {
		return false
	}
	isMCP := strings.EqualFold(toolType, "mcp")
	serverURL, _ := tool["server_url"].(string)
	if serverURL == "" {
		serverURL, _ = tool["serverUrl"].(string)
	}
	serverURL = strings.TrimSpace(serverURL)
	connectorID, _ := tool["connector_id"].(string)
	if connectorID == "" {
		connectorID, _ = tool["connectorId"].(string)
	}
	connectorID = strings.TrimSpace(connectorID)

	if isDeniedToolsAssignment(a) {
		deniedTypes, hasDeniedTypes := toolboxEffectiveStringSliceParam(a, "deniedToolTypes")
		deniedEndpoints, hasDeniedEndpoints := toolboxEffectiveStringSliceParam(a, "deniedMcpEndpoints")
		deniedConnectors, hasDeniedConnectors := toolboxEffectiveStringSliceParam(a, "deniedMcpConnectors")
		if !hasDeniedTypes && !hasDeniedEndpoints && !hasDeniedConnectors {
			return false
		}
		if hasDeniedTypes && slices.ContainsFunc(deniedTypes, func(entry string) bool {
			return strings.EqualFold(toolType, strings.TrimSpace(entry))
		}) {
			return true
		}
		if isMCP {
			if serverURL != "" && hasDeniedEndpoints &&
				slices.ContainsFunc(deniedEndpoints, func(pattern string) bool {
					return matchesToolboxAzurePolicyLike(serverURL, pattern)
				}) {
				return true
			}
			if connectorID != "" && hasDeniedConnectors &&
				slices.ContainsFunc(deniedConnectors, func(entry string) bool {
					return strings.EqualFold(connectorID, strings.TrimSpace(entry))
				}) {
				return true
			}
		}
		return false
	}

	if isAllowedToolsAssignment(a) {
		approvedTypes, hasApprovedTypes := toolboxEffectiveStringSliceParam(a, "approvedToolTypes")
		approvedEndpoints, hasApprovedEndpoints := toolboxEffectiveStringSliceParam(a, "approvedMcpEndpoints")
		approvedConnectors, hasApprovedConnectors := toolboxEffectiveStringSliceParam(a, "approvedMcpConnectors")
		if !hasApprovedTypes && !hasApprovedEndpoints && !hasApprovedConnectors {
			return false
		}
		if hasApprovedTypes && slices.ContainsFunc(approvedTypes, func(entry string) bool {
			return strings.EqualFold(toolType, strings.TrimSpace(entry))
		}) {
			return false
		}
		if isMCP {
			if serverURL != "" && hasApprovedEndpoints &&
				slices.ContainsFunc(approvedEndpoints, func(pattern string) bool {
					return matchesToolboxAzurePolicyLike(serverURL, pattern)
				}) {
				return false
			}
			if connectorID != "" && hasApprovedConnectors &&
				slices.ContainsFunc(approvedConnectors, func(entry string) bool {
					return strings.EqualFold(connectorID, strings.TrimSpace(entry))
				}) {
				return false
			}
			if (len(approvedEndpoints) > 0 && serverURL == "") ||
				(len(approvedConnectors) > 0 && connectorID == "" && serverURL == "") {
				// Indeterminate locally; defer to service-side enforcement.
				return false
			}
		}
		return true
	}

	return false
}

func loadToolboxPolicyAssignments(
	ctx context.Context,
	projectID string,
	subscriptionID string,
	queryFn toolboxPolicyQueryFunc,
) ([]toolboxPolicyAssignment, error) {
	type mgRow struct {
		Scope string `json:"scope"`
	}
	mgRows, err := queryToolboxResourceGraphTyped[mgRow](ctx, queryFn, toolboxResourceGraphRequest{
		Subscriptions: []string{subscriptionID},
		Query:         buildToolboxMGQuery(subscriptionID),
	})
	if err != nil {
		return nil, err
	}
	mgScopes := make([]string, 0, len(mgRows))
	for _, r := range mgRows {
		mgScopes = append(mgScopes, normalizeToolboxPolicyScope(r.Scope))
	}

	assignments, err := queryToolboxResourceGraphTyped[toolboxPolicyAssignment](ctx, queryFn, toolboxResourceGraphRequest{
		Subscriptions: []string{subscriptionID},
		Query:         buildToolboxPolicyAssignmentsQuery(),
		Options: &toolboxResourceGraphOptions{
			AuthorizationScopeFilter: toolboxAuthorizationScopeFilter,
		},
	})
	if err != nil {
		return nil, err
	}

	var applicable []toolboxPolicyAssignment
	for _, a := range assignments {
		a.AssignmentID = normalizeToolboxPolicyScope(a.AssignmentID)
		a.ManagementGroupScopes = mgScopes
		if a.AssignmentScope == "" || isToolboxScopeApplicable(a.AssignmentScope, projectID, mgScopes) {
			applicable = append(applicable, a)
		}
	}

	if len(applicable) > 0 {
		ids := make([]string, 0, len(applicable))
		for _, a := range applicable {
			ids = append(ids, a.AssignmentID)
		}
		type exemptionRow struct {
			ExemptionID        string `json:"exemptionId"`
			PolicyAssignmentID string `json:"policyAssignmentId"`
			ExpiresOn          string `json:"expiresOn,omitempty"`
		}
		exemptions, err := queryToolboxResourceGraphTyped[exemptionRow](ctx, queryFn, toolboxResourceGraphRequest{
			Subscriptions: []string{subscriptionID},
			Query:         buildToolboxExemptionsQuery(ids),
			Options: &toolboxResourceGraphOptions{
				AuthorizationScopeFilter: toolboxAuthorizationScopeFilter,
			},
		})
		if err != nil {
			return nil, err
		}
		now := time.Now()
		exemptIDs := map[string]bool{}
		for _, ex := range exemptions {
			if strings.TrimSpace(ex.ExpiresOn) != "" {
				expiresAt, parseErr := time.Parse(time.RFC3339, strings.TrimSpace(ex.ExpiresOn))
				if parseErr != nil || !expiresAt.After(now) {
					continue
				}
			}
			normEx := normalizeToolboxPolicyScope(ex.ExemptionID)
			if before, _, ok := strings.Cut(normEx, toolboxPolicyExemptionMarker); ok &&
				isToolboxScopeApplicable(before, projectID, mgScopes) {
				exemptIDs[normalizeToolboxPolicyScope(ex.PolicyAssignmentID)] = true
			}
		}
		for i := range applicable {
			applicable[i].IsExempt = exemptIDs[applicable[i].AssignmentID]
		}
	}

	slices.SortFunc(applicable, func(a, b toolboxPolicyAssignment) int {
		return cmp.Compare(a.AssignmentID, b.AssignmentID)
	})
	return applicable, nil
}

func checkToolboxToolsPolicy(
	ctx context.Context,
	access *toolboxAccess,
	scope *toolboxAccessScope,
	toolboxName string,
	tools []map[string]any,
) error {
	if access == nil || scope == nil || scope.resource == nil || scope.tenantID == "" || len(tools) == 0 {
		return nil
	}

	var envValues map[string]string
	if access.environment != nil {
		if resp, err := access.environment.GetValues(ctx, nil); err == nil && resp != nil {
			envValues = make(map[string]string, len(resp.GetKeyValues()))
			for _, kv := range resp.GetKeyValues() {
				envValues[kv.GetKey()] = kv.GetValue()
			}
		}
	}
	if shouldSkipToolboxPolicyChecks(envValues) {
		return nil
	}

	evalCtx, cancel := context.WithTimeout(ctx, toolboxPolicyPreflightTimeout)
	defer cancel()

	queryFn := access.policyQuery
	if queryFn == nil {
		if access.newCredential == nil {
			return nil
		}
		cred, err := access.newCredential(scope.tenantID)
		if err != nil {
			log.Printf("toolbox policy check: failed to create credential (failing open): %v", err)
			return nil
		}
		queryFn = newToolboxResourceGraphQueryFunc(newToolboxARMPipeline(cred))
	}

	projectID := scope.resource.String()
	assignments, err := loadToolboxPolicyAssignments(evalCtx, projectID, scope.resource.SubscriptionID, queryFn)
	if err != nil {
		log.Printf("toolbox policy check: Resource Graph query failed for %q (failing open): %v", toolboxName, err)
		return nil
	}

	var violations []toolboxPolicyViolation
	for _, tool := range tools {
		toolType, _ := tool["type"].(string)
		toolType = strings.TrimSpace(toolType)
		toolName, _ := tool["name"].(string)
		toolName = strings.TrimSpace(toolName)
		label := toolType
		if toolName != "" && !strings.EqualFold(toolName, label) {
			label = fmt.Sprintf("%s (%s)", toolType, toolName)
		}
		for _, a := range assignments {
			if isToolboxToolBlocked(a, projectID, tool) {
				displayName := toolboxAssignmentDisplayName(a)
				violations = append(violations, toolboxPolicyViolation{
					ToolLabel:    label,
					AssignmentID: a.AssignmentID,
					DisplayName:  displayName,
					PortalURL:    buildToolboxPolicyPortalURL(projectID, a.AssignmentID),
					Message: fmt.Sprintf(
						"The %q policy doesn't allow tool %q in this project.",
						displayName,
						label,
					),
				})
			}
		}
	}

	if len(violations) == 0 {
		return nil
	}
	return toolboxPolicyViolationsToError(toolboxName, violations)
}

func toolboxPolicyViolationsToError(toolboxName string, violations []toolboxPolicyViolation) error {
	var messages []string
	var links []string
	for _, v := range violations {
		msg := strings.TrimSpace(v.Message)
		if msg == "" {
			msg = fmt.Sprintf("Blocked by Azure Policy %q (tool: %s).", v.DisplayName, v.ToolLabel)
		}
		if !slices.Contains(messages, msg) {
			messages = append(messages, msg)
		}
		if url := strings.TrimSpace(v.PortalURL); url != "" {
			linkLine := fmt.Sprintf("  View policy (%s): %s", v.DisplayName, url)
			if !slices.Contains(links, linkLine) {
				links = append(links, linkLine)
			}
		}
	}

	prefix := "toolbox deployment is blocked by Azure Policy"
	if strings.TrimSpace(toolboxName) != "" {
		prefix = fmt.Sprintf("toolbox %q is blocked by Azure Policy", strings.TrimSpace(toolboxName))
	}
	fullMessage := fmt.Sprintf("%s: %s", prefix, strings.Join(messages, " "))

	suggestion := "Update the toolbox tools to comply with your organization's Azure Policy " +
		"assignments, or select a different Microsoft Foundry project."
	if len(links) > 0 {
		suggestion += "\n" + strings.Join(links, "\n")
	}

	return exterrors.Validation(exterrors.CodeToolBlockedByPolicy, fullMessage, suggestion)
}

func tryClassifyToolboxPolicyError(
	err error,
	toolboxName string,
	scope *toolboxAccessScope,
) (error, bool) {
	respErr, ok := errors.AsType[*azcore.ResponseError](err)
	if !ok || respErr == nil || respErr.StatusCode != http.StatusForbidden {
		return nil, false
	}
	if respErr.RawResponse == nil || respErr.RawResponse.Body == nil {
		return nil, false
	}
	body, readErr := runtime.Payload(respErr.RawResponse)
	if readErr != nil || len(body) == 0 {
		return nil, false
	}

	type runtimePolicyErrorEnvelope struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Error   *struct {
			Code           string `json:"code"`
			Message        string `json:"message"`
			AdditionalInfo *struct {
				PolicyAssignmentID string `json:"policy_assignment_id"`
				PolicyDefinitionID string `json:"policy_definition_id"`
				Effect             string `json:"effect"`
			} `json:"additionalInfo"`
		} `json:"error"`
	}

	var parsed runtimePolicyErrorEnvelope
	if json.Unmarshal(body, &parsed) != nil {
		return nil, false
	}

	code := strings.TrimSpace(respErr.ErrorCode)
	rawMsg := strings.TrimSpace(parsed.Message)
	if strings.TrimSpace(parsed.Code) != "" {
		code = strings.TrimSpace(parsed.Code)
	}
	var policyAssignmentID string
	if parsed.Error != nil {
		if strings.TrimSpace(parsed.Error.Code) != "" {
			code = strings.TrimSpace(parsed.Error.Code)
		}
		if strings.TrimSpace(parsed.Error.Message) != "" {
			rawMsg = strings.TrimSpace(parsed.Error.Message)
		}
		if parsed.Error.AdditionalInfo != nil {
			policyAssignmentID = strings.TrimSpace(parsed.Error.AdditionalInfo.PolicyAssignmentID)
		}
	}

	msgLower := strings.ToLower(rawMsg)
	isPolicyDenied := strings.EqualFold(code, "agent_policy_denied") ||
		strings.EqualFold(respErr.ErrorCode, "agent_policy_denied") ||
		((strings.EqualFold(code, "forbidden") || strings.EqualFold(respErr.ErrorCode, "forbidden")) &&
			strings.Contains(msgLower, "noncompliant with policy ") &&
			strings.Contains(msgLower, "microsoft.cognitiveservices.data/accounts/projects/toolboxes/tools[*]"))
	if !isPolicyDenied {
		return nil, false
	}

	var assignmentIDs []string
	if isValidToolboxPolicyAssignmentID(policyAssignmentID) {
		assignmentIDs = []string{strings.TrimRight(policyAssignmentID, "/")}
	} else {
		for _, m := range toolboxRuntimePolicyDeniedRe.FindAllStringSubmatch(rawMsg, -1) {
			if len(m) >= 2 {
				id := strings.TrimRight(strings.TrimSpace(m[1]), "/")
				if isValidToolboxPolicyAssignmentID(id) &&
					!slices.ContainsFunc(assignmentIDs, func(existing string) bool {
						return strings.EqualFold(existing, id)
					}) {
					assignmentIDs = append(assignmentIDs, id)
				}
			}
		}
	}
	if len(assignmentIDs) == 0 {
		if !strings.EqualFold(code, "agent_policy_denied") &&
			!strings.EqualFold(respErr.ErrorCode, "agent_policy_denied") {
			return nil, false
		}
		assignmentIDs = []string{""}
	}

	projectID := ""
	if scope != nil && scope.resource != nil {
		projectID = scope.resource.String()
	}

	violations := make([]toolboxPolicyViolation, 0, len(assignmentIDs))
	for _, id := range assignmentIDs {
		displayName := "Azure Policy assignment"
		if trimmed := strings.TrimRight(strings.TrimSpace(id), "/"); trimmed != "" {
			parts := strings.Split(trimmed, "/")
			if last := parts[len(parts)-1]; last != "" {
				displayName = last
			}
		}
		// Never copy rawMsg or respErr.Error() into the user-visible violation message:
		// request URLs and service error text may contain connection credentials or tokens.
		safeMsg := fmt.Sprintf("The %q policy doesn't allow one or more tools in this toolbox.", displayName)
		violations = append(violations, toolboxPolicyViolation{
			AssignmentID: normalizeToolboxPolicyScope(id),
			DisplayName:  displayName,
			PortalURL:    buildToolboxPolicyPortalURL(projectID, id),
			Message:      safeMsg,
		})
	}

	return toolboxPolicyViolationsToError(toolboxName, violations), true
}
