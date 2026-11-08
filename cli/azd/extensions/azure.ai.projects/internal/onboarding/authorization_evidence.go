// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
)

// passReads shares raw evidence, including errors, only within one read-only pass.
// OnceValues also coalesces concurrent misses; callers must not mutate returned evidence.
type passReads[T any] struct {
	mu     sync.Mutex
	values map[string]func() (T, error)
}

func (c *passReads[T]) get(key string, read func() (T, error)) (T, error) {
	c.mu.Lock()
	if c.values == nil {
		c.values = map[string]func() (T, error){}
	}
	load, ok := c.values[key]
	if !ok {
		load = sync.OnceValues(read)
		c.values[key] = load
	}
	c.mu.Unlock()
	return load()
}

// AuthorizationEvidence shares authorization and owning-subscription reads within one provisioning preparation.
type AuthorizationEvidence struct {
	credential        azcore.TokenCredential
	options           *arm.ClientOptions
	armClients        passReads[*arm.Client]
	definitionClients passReads[*armauthorization.RoleDefinitionsClient]
	assignments       passReads[[]*armauthorization.RoleAssignment]
	roleNames         passReads[string]
	subscriptions     passReads[subscriptionResponse]
}

type subscriptionResponse struct {
	response *http.Response
	payload  []byte
}

// NewAuthorizationEvidence creates a request-local, concurrency-safe evidence cache.
func NewAuthorizationEvidence(credential azcore.TokenCredential, options *arm.ClientOptions) *AuthorizationEvidence {
	return &AuthorizationEvidence{credential: credential, options: onboardingARMOptions(options)}
}

func (e *AuthorizationEvidence) armClient() (*arm.Client, error) {
	return e.armClients.get("", func() (*arm.Client, error) {
		return arm.NewClient("azure.ai.projects.onboarding", "v1.0.0", e.credential, e.options)
	})
}

// GetSubscription shares raw responses while each consumer retains its own decoding and validation.
func (e *AuthorizationEvidence) GetSubscription(ctx context.Context, subscription string) (*http.Response, error) {
	cached, err := e.subscriptions.get(strings.ToLower(subscription), func() (subscriptionResponse, error) {
		client, err := e.armClient()
		if err != nil {
			return subscriptionResponse{}, err
		}
		request, err := runtime.NewRequest(ctx, http.MethodGet,
			strings.TrimRight(client.Endpoint(), "/")+"/subscriptions/"+url.PathEscape(subscription)+
				"?api-version=2022-12-01")
		if err != nil {
			return subscriptionResponse{}, err
		}
		request.Raw().Header.Set("Accept", "application/json")
		response, err := client.Pipeline().Do(request)
		if err == nil && !runtime.HasStatusCode(response, http.StatusOK) {
			err = runtime.NewResponseError(response)
		}
		if err != nil {
			return subscriptionResponse{}, err
		}
		payload, err := runtime.Payload(response)
		return subscriptionResponse{response: response, payload: payload}, err
	})
	if err != nil {
		return nil, err
	}
	response := *cached.response
	response.Header = cached.response.Header.Clone()
	response.Body = io.NopCloser(bytes.NewReader(cached.payload))
	return &response, nil
}

func (e *AuthorizationEvidence) definitionClient() (*armauthorization.RoleDefinitionsClient, error) {
	return e.definitionClients.get("", func() (*armauthorization.RoleDefinitionsClient, error) {
		return armauthorization.NewRoleDefinitionsClient(e.credential, e.options)
	})
}

func (e *AuthorizationEvidence) roleAssignments(
	ctx context.Context, scope string, allowMissing bool,
) ([]*armauthorization.RoleAssignment, error) {
	return e.assignments.get(strings.ToLower(scope)+"|"+strconv.FormatBool(allowMissing),
		func() ([]*armauthorization.RoleAssignment, error) {
			return e.readAssignments(ctx, scope, allowMissing)
		})
}

func (e *AuthorizationEvidence) readAssignments(
	ctx context.Context, scope string, allowMissing bool,
) ([]*armauthorization.RoleAssignment, error) {
	id, err := arm.ParseResourceID(scope)
	if err != nil || id.SubscriptionID == "" {
		return nil, fmt.Errorf("authorization requires a complete subscription scope: %s", scope)
	}
	client, err := e.armClient()
	if err != nil {
		return nil, err
	}
	uri := strings.TrimRight(client.Endpoint(), "/") + scope +
		"/providers/Microsoft.Authorization/roleAssignments?api-version=2022-04-01&$filter=atScope()"
	var assignments []*armauthorization.RoleAssignment
	firstPage := true
	visited := map[string]bool{}
	for uri != "" {
		if visited[uri] {
			return nil, fmt.Errorf("role assignment pagination repeated a page at %s", scope)
		}
		visited[uri] = true
		request, err := runtime.NewRequest(ctx, http.MethodGet, uri)
		if err != nil {
			return nil, fmt.Errorf("construct role assignment request at %s: %w", scope, err)
		}
		response, err := client.Pipeline().Do(request)
		if err == nil && !runtime.HasStatusCode(response, http.StatusOK) {
			err = runtime.NewResponseError(response)
		}
		if err != nil {
			if response, ok := errors.AsType[*azcore.ResponseError](err); ok &&
				allowMissing && firstPage && response.StatusCode == http.StatusNotFound {
				return nil, nil
			}
			return nil, fmt.Errorf("read applicable role assignments at %s: %w", scope, err)
		}
		firstPage = false
		var page struct {
			Value    *[]*armauthorization.RoleAssignment `json:"value"`
			NextLink json.RawMessage                     `json:"nextLink"`
		}
		if err := runtime.UnmarshalAsJSON(response, &page); err != nil {
			return nil, fmt.Errorf("decode role assignments at %s: %w", scope, err)
		}
		if page.Value == nil {
			return nil, fmt.Errorf("role assignment list is incomplete at %s", scope)
		}
		for _, assignment := range *page.Value {
			if assignment == nil || assignment.Properties == nil ||
				authString(assignment.Properties.PrincipalID) == "" || authString(assignment.Properties.Scope) == "" ||
				authString(assignment.Properties.RoleDefinitionID) == "" {
				return nil, fmt.Errorf("role assignment evidence is incomplete at %s", scope)
			}
		}
		assignments = append(assignments, (*page.Value)...)
		uri = ""
		if len(page.NextLink) > 0 {
			if string(page.NextLink) == "null" {
				return nil, fmt.Errorf("role assignment nextLink is not a string at %s", scope)
			}
			if err := json.Unmarshal(page.NextLink, &uri); err != nil {
				return nil, fmt.Errorf("decode role assignment nextLink at %s: %w", scope, err)
			}
		}
	}
	return assignments, nil
}

// ExistingRoleAssignmentKeys deduplicates unconditional grants and reports conflicting required conditional assignments.
func (e *AuthorizationEvidence) ExistingRoleAssignmentKeys(
	ctx context.Context, plan Plan, facts Facts, federationRoleID string,
) ([]string, Report, error) {
	keys := []string{}
	var skipped Report
	var conditional []*armauthorization.RoleAssignment
	if plan.Hosting.Supplied && plan.Workload.Supplied && plan.Storage.Supplied {
		return keys, skipped, nil
	}
	for _, resource := range []Resource{plan.Cluster, plan.Workload, plan.Storage} {
		if missing := resource.Missing; missing != "" {
			skipped = append(skipped, SkippedCheck("role-assignment-keys", resource.diagnosticScope(),
				"existing role assignment deduplication", missing))
			continue
		}
		if resource.ID == "" {
			return nil, skipped, fmt.Errorf("role assignment target is absent at %s", resource.diagnosticScope())
		}
		allowMissing := !resource.Supplied || resource.LifecycleMissing != ""
		assignments, err := e.roleAssignments(ctx, resource.ID, allowMissing)
		if err != nil {
			return nil, skipped, err
		}
		for _, assignment := range assignments {
			p := assignment.Properties
			if authString(p.Condition) != "" {
				conditional = append(conditional, assignment)
				continue
			}
			keys = append(keys, roleAssignmentKey(authString(p.Scope), authString(p.PrincipalID),
				authString(p.RoleDefinitionID)))
		}
	}
	slices.Sort(keys)
	keys = slices.Compact(keys)
	grants := grantRequests(plan, facts.HostingIdentity, facts.WorkloadIdentity)
	for i := range grants {
		if grants[i].checkID == "hosting-federation" && federationRoleID != "" {
			grants[i].roleID = roleGUID(federationRoleID)
		}
	}
	for _, assignment := range conditional {
		p := assignment.Properties
		key := roleAssignmentKey(authString(p.Scope), authString(p.PrincipalID), authString(p.RoleDefinitionID))
		if slices.Contains(keys, key) {
			continue
		}
		for _, grant := range grants {
			if grant.principalID == "" || key != roleAssignmentKey(grant.scope, grant.principalID, grant.roleID) {
				continue
			}
			if authString(assignment.ID) == "" {
				return nil, skipped, fmt.Errorf(
					"conditional role assignment ID is missing at %s for principal %s and role %s",
					grant.scope, grant.principalID, grant.roleID)
			}
			identity := plan.Hosting
			if grant.checkID == "workload-blob" {
				identity = plan.Workload
			}
			check := resourceCheck("role-assignment-keys", grant.scope,
				"a matching role assignment without a condition before suppressing the template grant")
			check.Status = Fail
			check.Observed = fmt.Sprintf(
				"existing role assignment %s for identity %s (principalId %s), scope %s, role %s (roleDefinitionId %s) "+
					"has condition %q (conditionVersion %q); preflight cannot establish the required unconditional grant",
				authString(assignment.ID), identity.ID, grant.principalID, grant.scope, grant.roleName,
				authString(p.RoleDefinitionID), authString(p.Condition), authString(p.ConditionVersion))
			check.Missing = "a matching role assignment without a condition"
			check.Remediation = fmt.Sprintf(
				"Have the resource administrator review role assignment %s and, if approved, remove its condition "+
					"or replace it with an unconditional %s assignment (roleDefinitionId %s) for identity %s "+
					"(principalId %s) at scope %s. Creating a second assignment with the same principal, scope and role "+
					"under a different ID conflicts with the existing assignment. azd does not modify existing conditions.",
				authString(assignment.ID), grant.roleName, authString(p.RoleDefinitionID),
				identity.ID, grant.principalID, grant.scope)
			skipped = append(skipped, check)
		}
	}
	return keys, skipped, skipped.Err()
}
