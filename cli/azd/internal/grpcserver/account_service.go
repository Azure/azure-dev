// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/azureutil"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type accountService struct {
	azdext.UnimplementedAccountServiceServer
	subscriptionsManager interface {
		account.SubscriptionResolver
		GetSubscriptions(context.Context) ([]account.Subscription, error)
		LookupTenant(context.Context, string) (string, error)
	}
	userProfileService    *azapi.UserProfileService
	principalTypeProvider interface {
		CurrentPrincipalType(context.Context) (auth.PrincipalType, error)
	}
}

func NewAccountService(
	subscriptionsManager *account.SubscriptionsManager,
	userProfileService *azapi.UserProfileService,
	authManager *auth.Manager,
) azdext.AccountServiceServer {
	return &accountService{
		subscriptionsManager:  subscriptionsManager,
		userProfileService:    userProfileService,
		principalTypeProvider: authManager,
	}
}

var _ BetaAccountServiceGetCurrentPrincipalOverride = (*accountService)(nil)

func (s *accountService) GetCurrentPrincipal(
	ctx context.Context,
	req *v1beta.GetCurrentPrincipalRequest,
) (*v1beta.GetCurrentPrincipalResponse, error) {
	if strings.TrimSpace(req.GetSubscriptionId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "subscription id is required")
	}

	principalType, err := s.principalTypeProvider.CurrentPrincipalType(ctx)
	if err != nil {
		return nil, err
	}

	var protoType v1beta.PrincipalType
	switch principalType {
	case auth.UserPrincipalType:
		protoType = v1beta.PrincipalType_PRINCIPAL_TYPE_USER
	case auth.ServicePrincipalType:
		protoType = v1beta.PrincipalType_PRINCIPAL_TYPE_SERVICE_PRINCIPAL
	default:
		return nil, status.Error(codes.Internal, "unsupported current principal type")
	}

	subscription, err := s.subscriptionsManager.GetSubscription(ctx, req.SubscriptionId)
	if err != nil {
		return nil, fmt.Errorf("getting subscription %s: %w", req.SubscriptionId, err)
	}

	// Role assignments need the object ID in the resource tenant, even when access uses another tenant.
	objectID, err := azureutil.GetCurrentPrincipalId(ctx, s.userProfileService, subscription.TenantId)
	if err != nil {
		return nil, fmt.Errorf("fetching current principal information: %w", err)
	}

	return &v1beta.GetCurrentPrincipalResponse{
		ObjectId:      objectID,
		PrincipalType: protoType,
	}, nil
}

func (s *accountService) ListSubscriptions(
	ctx context.Context,
	req *azdext.ListSubscriptionsRequest,
) (*azdext.ListSubscriptionsResponse, error) {
	// Use GetSubscriptions for caching semantics
	subscriptions, err := s.subscriptionsManager.GetSubscriptions(ctx)
	if err != nil {
		return nil, err
	}

	// Filter by tenant ID if requested
	if req.TenantId != nil && *req.TenantId != "" {
		filtered := make([]account.Subscription, 0, len(subscriptions))
		for _, sub := range subscriptions {
			if sub.UserAccessTenantId == *req.TenantId {
				filtered = append(filtered, sub)
			}
		}
		subscriptions = filtered
	}

	// Convert to proto subscriptions
	protoSubscriptions := make([]*azdext.Subscription, len(subscriptions))
	for i, sub := range subscriptions {
		protoSubscriptions[i] = &azdext.Subscription{
			Id:           sub.Id,
			Name:         sub.Name,
			TenantId:     sub.TenantId,
			UserTenantId: sub.UserAccessTenantId,
			IsDefault:    sub.IsDefault,
		}
	}

	return &azdext.ListSubscriptionsResponse{
		Subscriptions: protoSubscriptions,
	}, nil
}

func (s *accountService) LookupTenant(
	ctx context.Context,
	req *azdext.LookupTenantRequest,
) (*azdext.LookupTenantResponse, error) {
	if req.SubscriptionId == "" {
		return nil, status.Error(codes.InvalidArgument, "subscription id is required")
	}

	tenantId, err := s.subscriptionsManager.LookupTenant(ctx, req.SubscriptionId)
	if err != nil {
		return nil, err
	}

	return &azdext.LookupTenantResponse{
		TenantId: tenantId,
	}, nil
}
