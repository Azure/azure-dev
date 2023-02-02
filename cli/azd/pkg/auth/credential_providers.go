package auth

import (
	"context"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

type DefaultTenantCredentialProvider struct {
	auth *Manager
}

func NewDefaultTenantCredentialProvider(auth *Manager) *DefaultTenantCredentialProvider {
	return &DefaultTenantCredentialProvider{
		auth: auth,
	}
}

// Gets an authenticated token credential
func (c *DefaultTenantCredentialProvider) GetTokenCredential(ctx context.Context) (azcore.TokenCredential, error) {
	credential, err := c.auth.CredentialForCurrentUser(ctx, nil)
	if err != nil {
		return nil, err
	}

	if _, err := EnsureLoggedInCredential(ctx, credential); err != nil {
		return nil, err
	}

	return credential, nil
}

type TenantCredentialProvider interface {
	// Gets an authenticated token credential for the given tenant. If tenantId is empty, uses the default home tenant.
	GetTokenCredential(ctx context.Context, tenantId string) (azcore.TokenCredential, error)
}

type MultiTenantCredentialProvider struct {
	auth *Manager

	// In-memory store for tenant credentials. Since azcore.TokenCredential is usually backed by a publicClient
	// that holds an in-memory cache, we need to hold on to azcore.TokenCredential instances to maintain that cache.
	// It also allows us to call EnsureLoggedInCredential once.
	tenantCredentials sync.Map
}

func NewMultiTenantCredentialProvider(auth *Manager) *MultiTenantCredentialProvider {
	return &MultiTenantCredentialProvider{
		auth: auth,
	}
}

// Gets an authenticated token credential for the given tenant. If tenantId is empty, uses the default home tenant.
func (t *MultiTenantCredentialProvider) GetTokenCredential(ctx context.Context, tenantId string) (azcore.TokenCredential, error) {
	if val, ok := t.tenantCredentials.Load(tenantId); ok {
		return val.(azcore.TokenCredential), nil
	}

	credential, err := t.auth.CredentialForCurrentUser(ctx, &CredentialForCurrentUserOptions{tenantId})
	if err != nil {
		return nil, err
	}

	//TODO: unsure if this is needed per-tenant
	if _, err := EnsureLoggedInCredential(ctx, credential); err != nil {
		return nil, err
	}

	t.tenantCredentials.Store(tenantId, credential)
	return credential, nil
}
