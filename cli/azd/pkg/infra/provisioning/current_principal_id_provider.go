package provisioning

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azureutil"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
)

type CurrentPrincipalIdProvider interface {
	// CurrentPrincipalId returns the object id of the current logged in principal, or an error if it can not be
	// determined.
	CurrentPrincipalId(ctx context.Context) (string, error)
}

func NewPrincipalIdProvider(
	env *environment.Environment,
	userProfileService *azapi.UserProfileService,
	subResolver account.SubscriptionTenantResolver,
) CurrentPrincipalIdProvider {
	return &principalIDProvider{
		env:                env,
		userProfileService: userProfileService,
		subResolver:        subResolver,
	}
}

type principalIDProvider struct {
	env                *environment.Environment
	userProfileService *azapi.UserProfileService
	subResolver        account.SubscriptionTenantResolver
}

func (p *principalIDProvider) CurrentPrincipalId(ctx context.Context) (string, error) {
	tenantId, err := p.subResolver.LookupTenant(ctx, p.env.GetSubscriptionId())
	if err != nil {
		return "", fmt.Errorf("getting tenant id for subscription %s. Error: %w", p.env.GetSubscriptionId(), err)
	}

	principalId, err := azureutil.GetCurrentPrincipalId(ctx, p.userProfileService, tenantId)
	if err != nil {
		return "", fmt.Errorf("fetching current user information: %w", err)
	}

	return principalId, nil
}
