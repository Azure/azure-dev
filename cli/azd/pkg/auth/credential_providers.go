package auth

import (
	"context"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

// MultiTenantCredentialProvider provides token credentials for different tenants.
//
// Only use this if you need to perform multi-tenant operations.
type MultiTenantCredentialProvider interface {
	// Gets an authenticated token credential for the given tenant. If tenantId is empty, uses the default home tenant.
	GetTokenCredential(ctx context.Context, tenantId string) (azcore.TokenCredential, error)
}

type multiTenantCredentialProvider struct {
	auth *Manager

	// In-memory store for tenant credentials. Since azcore.TokenCredential is usually backed by a publicClient
	// that holds an in-memory cache, we need to hold on to azcore.TokenCredential instances to maintain that cache.
	// It also allows us to call EnsureLoggedInCredential once.
	tenantCredentials sync.Map
}

func NewMultiTenantCredentialProvider(auth *Manager) MultiTenantCredentialProvider {
	return &multiTenantCredentialProvider{
		auth: auth,
	}
}

// Gets an authenticated token credential for the given tenant. If tenantId is empty, uses the default home tenant.
func (t *multiTenantCredentialProvider) GetTokenCredential(
	ctx context.Context, tenantId string) (azcore.TokenCredential, error) {
	if val, ok := t.tenantCredentials.Load(tenantId); ok {
		return val.(azcore.TokenCredential), nil
	}

	credential, err := t.auth.CredentialForCurrentUser(ctx, &CredentialForCurrentUserOptions{
		TenantID: tenantId,
	})

	if err != nil {
		return nil, err
	}

	if _, err := EnsureLoggedInCredential(ctx, credential, t.auth.cloud); err != nil {
		return nil, err
	}

	t.tenantCredentials.Store(tenantId, credential)
	return credential, nil
}
