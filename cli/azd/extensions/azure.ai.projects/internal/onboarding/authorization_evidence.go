// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
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

type authorizationEvidence struct {
	credential        azcore.TokenCredential
	options           *arm.ClientOptions
	assignmentClients passReads[*armauthorization.RoleAssignmentsClient]
	definitionClients passReads[*armauthorization.RoleDefinitionsClient]
	assignments       passReads[[]*armauthorization.RoleAssignment]
	definitions       passReads[armauthorization.RoleDefinitionsClientGetByIDResponse]
	roleNames         passReads[string]
}

func newAuthorizationEvidence(credential azcore.TokenCredential, options *arm.ClientOptions) *authorizationEvidence {
	return &authorizationEvidence{credential: credential, options: options}
}

func (a *scopedAuthorizer) evidence() *authorizationEvidence {
	if a.reads != nil {
		return a.reads
	}
	return newAuthorizationEvidence(a.credential, a.options)
}

func (e *authorizationEvidence) definitionClient() (*armauthorization.RoleDefinitionsClient, error) {
	return e.definitionClients.get("", func() (*armauthorization.RoleDefinitionsClient, error) {
		return armauthorization.NewRoleDefinitionsClient(e.credential, e.options)
	})
}

func (e *authorizationEvidence) roleDefinition(
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
