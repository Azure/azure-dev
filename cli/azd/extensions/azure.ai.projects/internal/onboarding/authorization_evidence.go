// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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

// AuthorizationEvidence shares authorization reads within one provisioning preparation.
type AuthorizationEvidence struct {
	credential        azcore.TokenCredential
	options           *arm.ClientOptions
	assignmentClients passReads[*arm.Client]
	definitionClients passReads[*armauthorization.RoleDefinitionsClient]
	assignments       passReads[[]*armauthorization.RoleAssignment]
	roleNames         passReads[string]
}

// NewAuthorizationEvidence creates a request-local, concurrency-safe evidence cache.
func NewAuthorizationEvidence(credential azcore.TokenCredential, options *arm.ClientOptions) *AuthorizationEvidence {
	return &AuthorizationEvidence{credential: credential, options: onboardingARMOptions(options)}
}

func (a *scopedAuthorizer) evidence() *AuthorizationEvidence {
	if a.reads != nil {
		return a.reads
	}
	return NewAuthorizationEvidence(a.credential, a.options)
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
	client, err := e.assignmentClients.get("",
		func() (*arm.Client, error) {
			return arm.NewClient("azure.ai.projects.onboarding", "v1.0.0", e.credential, e.options)
		})
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

// ExistingRoleAssignmentKeys supplies exact scope/principal/role keys for mixed-resource deployment deduplication.
func (e *AuthorizationEvidence) ExistingRoleAssignmentKeys(ctx context.Context, plan Plan) ([]string, Report, error) {
	keys := []string{}
	var skipped Report
	if plan.Hosting.Supplied && plan.Workload.Supplied && plan.Storage.Supplied {
		return keys, skipped, nil
	}
	for _, resource := range []Resource{plan.Cluster, plan.Workload, plan.Storage} {
		if missing := resource.Missing + resource.LifecycleMissing; missing != "" {
			skipped = append(skipped, SkippedCheck("role-assignment-keys", resource.diagnosticScope(),
				"existing role assignment deduplication", missing))
			continue
		}
		if resource.ID == "" {
			return nil, skipped, fmt.Errorf("role assignment target is absent at %s", resource.diagnosticScope())
		}
		assignments, err := e.roleAssignments(ctx, resource.ID, !resource.Supplied)
		if err != nil {
			return nil, skipped, err
		}
		for _, assignment := range assignments {
			p := assignment.Properties
			keys = append(keys, strings.ToLower(authString(p.Scope)+"|"+authString(p.PrincipalID)+"|"+
				roleGUID(authString(p.RoleDefinitionID))))
		}
	}
	slices.Sort(keys)
	return slices.Compact(keys), skipped, nil
}
