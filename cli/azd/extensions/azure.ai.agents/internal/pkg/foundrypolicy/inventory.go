// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package foundrypolicy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"azureaiagent/internal/pkg/azure"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
)

const (
	resourceGraphAPIVersion       = "2024-04-01"
	authorizationScopeFilter      = "AtScopeAboveAndBelow"
	defaultARMEndpoint            = "https://management.azure.com"
	policyExemptionMarker         = "/providers/microsoft.authorization/policyexemptions/"
	DefaultPolicyCacheTTL         = 5 * time.Minute
	DefaultPolicyPreflightTimeout = 30 * time.Second
)

// ResourceGraphOptions holds query options for Azure Resource Graph requests.
type ResourceGraphOptions struct {
	AuthorizationScopeFilter string `json:"authorizationScopeFilter,omitempty"`
	SkipToken                string `json:"$skipToken,omitempty"`
}

// ResourceGraphRequest represents the POST payload sent to Azure Resource Graph.
type ResourceGraphRequest struct {
	Subscriptions []string              `json:"subscriptions"`
	Query         string                `json:"query"`
	Options       *ResourceGraphOptions `json:"options,omitempty"`
}

type resourceGraphResponse struct {
	Data            []json.RawMessage `json:"data"`
	DollarSkipToken string            `json:"$skipToken"`
	SkipToken       string            `json:"skipToken"`
	ResultTruncated any               `json:"resultTruncated"`
	Error           *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// ResourceGraphQueryFunc executes a Resource Graph query and returns raw JSON rows.
type ResourceGraphQueryFunc func(ctx context.Context, req ResourceGraphRequest) ([]json.RawMessage, error)

func escapeKustoString(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}

// BuildManagementGroupQuery builds the Kusto query for the subscription's management group ancestors.
func BuildManagementGroupQuery(subscriptionID string) string {
	escaped := escapeKustoString(strings.ToLower(strings.TrimSpace(subscriptionID)))
	return fmt.Sprintf(`
resourcecontainers
| where type =~ 'microsoft.resources/subscriptions'
| where subscriptionId =~ '%s'
| mv-expand ancestor = properties.managementGroupAncestorsChain
| project scope = tolower(strcat('/providers/Microsoft.Management/managementGroups/', tostring(ancestor.name)))
| distinct scope
| order by scope asc`, escaped)
}

// BuildPolicyAssignmentsQuery builds the Kusto query that joins direct policy assignments
// with the built-in Foundry policy definitions for agent kinds, container images, tools, and models.
func BuildPolicyAssignmentsQuery() string {
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
            '%s',
            '%s',
            '%s'
        )
        or tolower(definitionDisplayName) endswith '%s'
        or tolower(definitionDisplayName) endswith '%s'
        or tolower(definitionDisplayName) endswith '%s'
        or tolower(definitionDisplayName) endswith '%s'
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
		DeniedAgentKindsPolicyID,
		AllowedAgentKindsPolicyID,
		DeniedContainerImagesPolicyID,
		AllowedContainerImagesPolicyID,
		DeniedAgentKindsDisplaySuffix,
		AllowedAgentKindsDisplaySuffix,
		DeniedContainerImagesDisplaySuffix,
		AllowedContainerImagesDisplaySuffix,
		DeniedToolsDisplaySuffix,
		AllowedToolsDisplaySuffix,
		DeniedModelsDisplaySuffix,
		AllowedModelsDisplaySuffix,
	)
}

// BuildPolicyExemptionsQuery builds the Kusto query for active policy exemptions
// targeting the given policy assignment IDs.
func BuildPolicyExemptionsQuery(assignmentIDs []string) string {
	quoted := make([]string, 0, len(assignmentIDs))
	for _, id := range assignmentIDs {
		quoted = append(quoted, fmt.Sprintf("'%s'", escapeKustoString(NormalizeScope(id))))
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

type managementGroupScopeRow struct {
	Scope string `json:"scope"`
}

type policyExemptionRow struct {
	ExemptionID        string `json:"exemptionId"`
	PolicyAssignmentID string `json:"policyAssignmentId"`
	ExpiresOn          string `json:"expiresOn,omitempty"`
}

func getExemptionScope(exemptionID string) (string, bool) {
	normalized := NormalizeScope(exemptionID)
	before, _, ok := strings.Cut(normalized, policyExemptionMarker)
	if !ok {
		return "", false
	}
	return before, true
}

func isActiveExemption(exemption policyExemptionRow, now time.Time) bool {
	if strings.TrimSpace(exemption.ExpiresOn) == "" {
		return true
	}
	expiresAt, err := time.Parse(time.RFC3339, strings.TrimSpace(exemption.ExpiresOn))
	if err != nil {
		return false
	}
	return expiresAt.After(now)
}

// NewARMPipeline creates a runtime.Pipeline configured for Azure Resource Manager requests.
func NewARMPipeline(cred azcore.TokenCredential) runtime.Pipeline {
	armOpts := azure.NewArmClientOptions()
	clientOpts := armOpts.ClientOptions
	clientOpts.PerCallPolicies = append(
		[]policy.Policy{
			runtime.NewBearerTokenPolicy(cred, []string{defaultARMEndpoint + "/.default"}, nil),
		},
		clientOpts.PerCallPolicies...,
	)
	return runtime.NewPipeline("azure-ai-agents-policy", "v1.0.0", runtime.PipelineOptions{}, &clientOpts)
}

// NewResourceGraphQueryFunc creates a ResourceGraphQueryFunc that handles continuation tokens
// and diagnostic logging while returning safe user-facing errors.
func NewResourceGraphQueryFunc(
	pipeline runtime.Pipeline,
	armEndpoint string,
	logError func(string),
) ResourceGraphQueryFunc {
	if strings.TrimSpace(armEndpoint) == "" {
		armEndpoint = defaultARMEndpoint
	}
	if logError == nil {
		logError = func(msg string) { log.Printf("[FoundryPolicy] %s", msg) }
	}
	resourceGraphURL := fmt.Sprintf(
		"%s/providers/Microsoft.ResourceGraph/resources?api-version=%s",
		strings.TrimRight(strings.TrimSpace(armEndpoint), "/"),
		resourceGraphAPIVersion,
	)

	return func(ctx context.Context, request ResourceGraphRequest) ([]json.RawMessage, error) {
		var rows []json.RawMessage
		seenSkipTokens := map[string]bool{}
		var skipToken string

		for {
			pageReq := request
			if skipToken != "" {
				opts := ResourceGraphOptions{SkipToken: skipToken}
				if request.Options != nil {
					opts.AuthorizationScopeFilter = request.Options.AuthorizationScopeFilter
				}
				pageReq.Options = &opts
			}

			bodyBytes, err := json.Marshal(pageReq)
			if err != nil {
				return nil, fmt.Errorf("marshal Resource Graph request: %w", err)
			}

			httpReq, err := runtime.NewRequest(ctx, http.MethodPost, resourceGraphURL)
			if err != nil {
				return nil, fmt.Errorf("create Resource Graph request: %w", err)
			}
			httpReq.Raw().Header.Set("Accept", "application/json")
			if err := httpReq.SetBody(streaming.NopCloser(bytes.NewReader(bodyBytes)), "application/json"); err != nil {
				return nil, fmt.Errorf("set Resource Graph request body: %w", err)
			}

			resp, err := pipeline.Do(httpReq)
			if err != nil {
				logError(fmt.Sprintf("Foundry Azure Policy Resource Graph request failed: %v", err))
				return nil, fmt.Errorf("unable to evaluate Azure Policy for the selected project")
			}

			respBytes, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if readErr != nil {
				logError(fmt.Sprintf("Foundry Azure Policy Resource Graph response read failed: %v", readErr))
				return nil, fmt.Errorf("unable to evaluate Azure Policy for the selected project")
			}

			var parsed resourceGraphResponse
			_ = json.Unmarshal(respBytes, &parsed)

			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				code := fmt.Sprintf("%d", resp.StatusCode)
				detail := resp.Status
				if parsed.Error != nil {
					if parsed.Error.Code != "" {
						code = parsed.Error.Code
					}
					if parsed.Error.Message != "" {
						detail = parsed.Error.Message
					}
				}
				logError(fmt.Sprintf("Foundry Azure Policy Resource Graph query failed (%s): %s", code, detail))
				return nil, fmt.Errorf("unable to evaluate Azure Policy for the selected project")
			}

			rows = append(rows, parsed.Data...)

			nextToken := parsed.DollarSkipToken
			if nextToken == "" {
				nextToken = parsed.SkipToken
			}
			truncated := isResultTruncated(parsed.ResultTruncated)
			if nextToken == "" {
				if truncated {
					logError(
						"Foundry Azure Policy Resource Graph query returned a truncated result " +
							"without a continuation token.",
					)
					return nil, fmt.Errorf("unable to evaluate Azure Policy for the selected project")
				}
				return rows, nil
			}
			if seenSkipTokens[nextToken] {
				logError("Foundry Azure Policy Resource Graph query repeated a continuation token.")
				return nil, fmt.Errorf("unable to evaluate Azure Policy for the selected project")
			}
			seenSkipTokens[nextToken] = true
			skipToken = nextToken
		}
	}
}

func isResultTruncated(v any) bool {
	switch typed := v.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true")
	default:
		return false
	}
}

func queryResourceGraphTyped[T any](
	ctx context.Context,
	queryFn ResourceGraphQueryFunc,
	req ResourceGraphRequest,
) ([]T, error) {
	rawRows, err := queryFn(ctx, req)
	if err != nil {
		return nil, err
	}
	out := make([]T, 0, len(rawRows))
	for _, raw := range rawRows {
		var item T
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, fmt.Errorf("unmarshal Resource Graph row: %w", err)
		}
		out = append(out, item)
	}
	return out, nil
}

// LoadInventory queries Azure Resource Graph for management group ancestors, applicable
// policy assignments, and active exemptions for the given Foundry project.
func LoadInventory(
	ctx context.Context,
	projectID string,
	subscriptionID string,
	queryFn ResourceGraphQueryFunc,
) (Inventory, error) {
	mgRows, err := queryResourceGraphTyped[managementGroupScopeRow](ctx, queryFn, ResourceGraphRequest{
		Subscriptions: []string{subscriptionID},
		Query:         BuildManagementGroupQuery(subscriptionID),
	})
	if err != nil {
		return Inventory{}, err
	}
	mgScopes := make([]string, 0, len(mgRows))
	for _, r := range mgRows {
		mgScopes = append(mgScopes, NormalizeScope(r.Scope))
	}

	assignments, err := queryResourceGraphTyped[PolicyAssignment](ctx, queryFn, ResourceGraphRequest{
		Subscriptions: []string{subscriptionID},
		Query:         BuildPolicyAssignmentsQuery(),
		Options: &ResourceGraphOptions{
			AuthorizationScopeFilter: authorizationScopeFilter,
		},
	})
	if err != nil {
		return Inventory{}, err
	}

	var applicable []PolicyAssignment
	for _, a := range assignments {
		a.AssignmentID = NormalizeScope(a.AssignmentID)
		a.ManagementGroupScopes = mgScopes
		if a.AssignmentScope == "" || IsScopeApplicable(a.AssignmentScope, projectID, mgScopes) {
			applicable = append(applicable, a)
		}
	}

	if len(applicable) > 0 {
		assignmentIDs := make([]string, 0, len(applicable))
		for _, a := range applicable {
			assignmentIDs = append(assignmentIDs, a.AssignmentID)
		}
		exemptions, err := queryResourceGraphTyped[policyExemptionRow](ctx, queryFn, ResourceGraphRequest{
			Subscriptions: []string{subscriptionID},
			Query:         BuildPolicyExemptionsQuery(assignmentIDs),
			Options: &ResourceGraphOptions{
				AuthorizationScopeFilter: authorizationScopeFilter,
			},
		})
		if err != nil {
			return Inventory{}, err
		}
		now := time.Now()
		exemptIDs := map[string]bool{}
		for _, ex := range exemptions {
			if !isActiveExemption(ex, now) {
				continue
			}
			exScope, ok := getExemptionScope(ex.ExemptionID)
			if ok && IsScopeApplicable(exScope, projectID, mgScopes) {
				exemptIDs[NormalizeScope(ex.PolicyAssignmentID)] = true
			}
		}
		for i := range applicable {
			applicable[i].IsExempt = exemptIDs[applicable[i].AssignmentID]
		}
	}

	return Inventory{
		ProjectID:   projectID,
		Assignments: applicable,
	}, nil
}

// EvaluateCandidate evaluates all populated fields on candidate (agent kind, container image/registry,
// tools, and models) against the loaded inventory and optional ARM deployment policy evaluator.
func EvaluateCandidate(
	ctx context.Context,
	inventory Inventory,
	candidate Candidate,
	modelEvaluator DeploymentPolicyEvaluator,
) EvaluationResult {
	var violations []Violation
	hasIndeterminate := false

	if strings.TrimSpace(candidate.AgentKind) != "" {
		res := EvaluateAgentKindPolicy(inventory, candidate.AgentKind)
		violations = append(violations, res.Violations...)
	}

	if img := strings.TrimSpace(candidate.ContainerImage); img != "" {
		res := EvaluateContainerImagePolicy(inventory, ContainerImageCandidate{
			Type:  CandidateTypeImage,
			Value: img,
		})
		violations = append(violations, res.Violations...)
		if res.Outcome == OutcomeIndeterminate {
			hasIndeterminate = true
		}
	} else if reg := strings.TrimSpace(candidate.ContainerRegistry); reg != "" {
		res := EvaluateContainerImagePolicy(inventory, ContainerImageCandidate{
			Type:  CandidateTypeRegistry,
			Value: reg,
		})
		violations = append(violations, res.Violations...)
		if res.Outcome == OutcomeIndeterminate {
			hasIndeterminate = true
		}
	}

	if len(candidate.Tools) > 0 {
		res := EvaluateToolPolicy(inventory, candidate.Tools)
		violations = append(violations, res.Violations...)
	}

	if len(candidate.Models) > 0 {
		invModelViolations := EvaluateInventoryModelPolicy(inventory, candidate.Models)
		violations = append(violations, invModelViolations...)
		if modelEvaluator != nil {
			if armViolations, err := modelEvaluator.EvaluateModelDeployments(
				ctx, inventory.ProjectID, candidate.Models,
			); err == nil {
				for _, v := range armViolations {
					// Deduplicate if the same assignment & target were already reported by inventory.
					duplicate := false
					for _, existing := range violations {
						if existing.Target == v.Target &&
							existing.AssignmentID != "" &&
							existing.AssignmentID == v.AssignmentID {
							duplicate = true
							break
						}
					}
					if !duplicate {
						violations = append(violations, v)
					}
				}
			} else {
				log.Printf("[FoundryPolicy] ARM model policy evaluation failed (failing open): %v", err)
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
		return EvaluationResult{Outcome: OutcomeIndeterminate}
	}
	return EvaluationResult{Outcome: OutcomeAllowed}
}

type cacheEntry struct {
	inventory  Inventory
	cachedAt   time.Time
	retryAfter time.Time
}

type inFlightCall struct {
	done chan struct{}
	inv  Inventory
	err  error
}

// InventoryCache caches policy inventories per normalized Foundry project ID with
// configurable TTL, request timeout, in-flight deduplication, and stale-while-revalidate.
type InventoryCache struct {
	mu             sync.Mutex
	entries        map[string]*cacheEntry
	inFlight       map[string]*inFlightCall
	ttl            time.Duration
	retryThrottle  time.Duration
	requestTimeout time.Duration
	now            func() time.Time
	logWarning     func(string)
}

// NewInventoryCache constructs a new InventoryCache.
func NewInventoryCache(
	ttl time.Duration,
	now func() time.Time,
	logWarning func(string),
	retryThrottle time.Duration,
	requestTimeout time.Duration,
) *InventoryCache {
	if ttl <= 0 {
		ttl = DefaultPolicyCacheTTL
	}
	if now == nil {
		now = time.Now
	}
	if logWarning == nil {
		logWarning = func(msg string) { log.Printf("[FoundryPolicy] %s", msg) }
	}
	if retryThrottle <= 0 {
		retryThrottle = ttl
	}
	if requestTimeout <= 0 {
		requestTimeout = DefaultPolicyPreflightTimeout
	}
	return &InventoryCache{
		entries:        map[string]*cacheEntry{},
		inFlight:       map[string]*inFlightCall{},
		ttl:            ttl,
		retryThrottle:  retryThrottle,
		requestTimeout: requestTimeout,
		now:            now,
		logWarning:     logWarning,
	}
}

// DefaultCache is the shared process-wide policy inventory cache.
var DefaultCache = NewInventoryCache(
	DefaultPolicyCacheTTL,
	time.Now,
	nil,
	DefaultPolicyCacheTTL,
	DefaultPolicyPreflightTimeout,
)

// Get returns the cached or freshly fetched inventory for projectID.
func (c *InventoryCache) Get(
	ctx context.Context,
	projectID string,
	fetch func(context.Context) (Inventory, error),
) (Inventory, error) {
	res, err := c.GetWithFreshness(ctx, projectID, fetch)
	if err != nil {
		return Inventory{}, err
	}
	return res.Inventory, nil
}

// GetWithFreshness returns the inventory for projectID along with its freshness state.
// If a cached entry has expired, it returns the stale entry immediately and triggers
// a background refresh (unless throttled).
func (c *InventoryCache) GetWithFreshness(
	ctx context.Context,
	projectID string,
	fetch func(context.Context) (Inventory, error),
) (InventoryResult, error) {
	key := NormalizeScope(projectID)
	now := c.now()

	c.mu.Lock()
	cached := c.entries[key]
	if cached != nil && now.Sub(cached.cachedAt) < c.ttl {
		inv := cached.inventory
		c.mu.Unlock()
		return InventoryResult{Inventory: inv, Freshness: FreshnessFresh}, nil
	}

	existing := c.inFlight[key]
	if cached != nil {
		inv := cached.inventory
		if existing == nil && (cached.retryAfter.IsZero() || !now.Before(cached.retryAfter)) {
			call := c.startRefreshLocked(context.Background(), key, fetch, cached)
			_ = call
		}
		c.mu.Unlock()
		return InventoryResult{Inventory: inv, Freshness: FreshnessStale}, nil
	}

	call := existing
	if call == nil {
		call = c.startRefreshLocked(ctx, key, fetch, nil)
	}
	c.mu.Unlock()

	<-call.done
	if call.err != nil {
		return InventoryResult{}, call.err
	}
	return InventoryResult{Inventory: call.inv, Freshness: FreshnessFresh}, nil
}

// GetFresh forces a refresh when the cached entry for projectID is stale (unless throttled).
func (c *InventoryCache) GetFresh(
	ctx context.Context,
	projectID string,
	fetch func(context.Context) (Inventory, error),
) (InventoryResult, error) {
	key := NormalizeScope(projectID)
	now := c.now()

	c.mu.Lock()
	cached := c.entries[key]
	if cached != nil && now.Sub(cached.cachedAt) < c.ttl {
		inv := cached.inventory
		c.mu.Unlock()
		return InventoryResult{Inventory: inv, Freshness: FreshnessFresh}, nil
	}
	if cached != nil && !cached.retryAfter.IsZero() && now.Before(cached.retryAfter) {
		inv := cached.inventory
		c.mu.Unlock()
		return InventoryResult{Inventory: inv, Freshness: FreshnessStale}, nil
	}

	call := c.inFlight[key]
	if call == nil {
		call = c.startRefreshLocked(ctx, key, fetch, cached)
	}
	c.mu.Unlock()

	<-call.done
	if call.err != nil {
		return InventoryResult{}, call.err
	}

	c.mu.Lock()
	refreshed := c.entries[key]
	freshness := FreshnessStale
	if refreshed != nil && c.now().Sub(refreshed.cachedAt) < c.ttl {
		freshness = FreshnessFresh
	}
	c.mu.Unlock()

	return InventoryResult{Inventory: call.inv, Freshness: freshness}, nil
}

func (c *InventoryCache) startRefreshLocked(
	parentCtx context.Context,
	key string,
	fetch func(context.Context) (Inventory, error),
	staleEntry *cacheEntry,
) *inFlightCall {
	call := &inFlightCall{done: make(chan struct{})}
	c.inFlight[key] = call

	go func() {
		defer close(call.done)

		timeoutCtx, cancel := context.WithTimeout(parentCtx, c.requestTimeout)
		defer cancel()

		type fetchOutcome struct {
			inv Inventory
			err error
		}
		ch := make(chan fetchOutcome, 1)
		go func() {
			inv, err := fetch(timeoutCtx)
			ch <- fetchOutcome{inv: inv, err: err}
		}()

		var inv Inventory
		var fetchErr error
		timedOut := false

		select {
		case res := <-ch:
			inv = res.inv
			fetchErr = res.err
		case <-timeoutCtx.Done():
			timedOut = true
			fetchErr = fmt.Errorf(
				"Foundry policy preflight for %s timed out after %d ms.",
				key,
				c.requestTimeout.Milliseconds(),
			)
			c.logWarning(fetchErr.Error())
		}

		c.mu.Lock()
		defer c.mu.Unlock()
		delete(c.inFlight, key)

		if fetchErr == nil {
			c.entries[key] = &cacheEntry{
				inventory: inv,
				cachedAt:  c.now(),
			}
			call.inv = inv
			return
		}

		if staleEntry != nil {
			staleEntry.retryAfter = c.now().Add(c.retryThrottle)
			if !timedOut {
				c.logWarning(fmt.Sprintf(
					"Refresh failed for %s; retaining the last successful policy inventory for diagnostics. %v",
					key,
					fetchErr,
				))
			}
			call.inv = staleEntry.inventory
			call.err = nil
			return
		}

		call.err = fetchErr
	}()

	return call
}
