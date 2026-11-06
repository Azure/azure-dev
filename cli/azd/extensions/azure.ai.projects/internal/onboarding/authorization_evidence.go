// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
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
	assignmentClients passReads[*armauthorization.RoleAssignmentsClient]
	definitionClients passReads[*armauthorization.RoleDefinitionsClient]
	assignments       passReads[[]*armauthorization.RoleAssignment]
	definitions       passReads[armauthorization.RoleDefinitionsClientGetByIDResponse]
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

func (e *AuthorizationEvidence) roleDefinition(
	ctx context.Context, id string,
) (armauthorization.RoleDefinitionsClientGetByIDResponse, error) {
	return e.definitions.get(strings.ToLower(id), func() (armauthorization.RoleDefinitionsClientGetByIDResponse, error) {
		client, err := e.definitionClient()
		if err != nil {
			return armauthorization.RoleDefinitionsClientGetByIDResponse{}, err
		}
		return client.GetByID(ctx, id, nil)
	})
}

func (e *AuthorizationEvidence) filteredAssignments(
	ctx context.Context, scope, filter string,
) ([]*armauthorization.RoleAssignment, error) {
	return e.assignments.get(strings.ToLower(scope)+"|"+filter, func() ([]*armauthorization.RoleAssignment, error) {
		return e.readAssignments(ctx, scope, filter)
	})
}

func (e *AuthorizationEvidence) readAssignments(
	ctx context.Context, scope, filter string,
) ([]*armauthorization.RoleAssignment, error) {
	id, err := arm.ParseResourceID(scope)
	if err != nil || id.SubscriptionID == "" {
		return nil, fmt.Errorf("authorization requires a complete subscription scope: %s", scope)
	}
	client, err := e.assignmentClients.get(strings.ToLower(id.SubscriptionID),
		func() (*armauthorization.RoleAssignmentsClient, error) {
			return armauthorization.NewRoleAssignmentsClient(id.SubscriptionID, e.credential, e.options)
		})
	if err != nil {
		return nil, err
	}
	pager := client.NewListForScopePager(scope,
		&armauthorization.RoleAssignmentsClientListForScopeOptions{Filter: new(filter)})
	var assignments []*armauthorization.RoleAssignment
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("read applicable role assignments at %s: %w", scope, err)
		}
		assignments = append(assignments, page.Value...)
	}
	return assignments, nil
}

// assignmentApplies consumes only assignments returned by the target's ARM scope query.
// ARM establishes management-group ancestry; its resource IDs are not subscription path prefixes.
func assignmentApplies(assignment *armauthorization.RoleAssignment, scope string) bool {
	p := assignment.Properties
	if scopeContains(authString(p.Scope), scope) {
		return true
	}
	parent := authString(p.Scope)
	parts := strings.Split(parent, "/")
	if len(parts) != 5 || parts[0] != "" || !strings.EqualFold(parts[1], "providers") ||
		!strings.EqualFold(parts[2], "Microsoft.Management") || !strings.EqualFold(parts[3], "managementGroups") ||
		parts[4] == "" || strings.ContainsAny(parts[4], "?#%\\@:\x00 \t\r\n") {
		return false
	}
	id, err := arm.ParseResourceID(authString(assignment.ID))
	return err == nil && id.Name != "" && strings.EqualFold(id.ResourceType.String(),
		"Microsoft.Authorization/roleAssignments") && strings.EqualFold(id.Parent.String(), parent)
}
