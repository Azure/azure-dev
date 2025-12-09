// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

type accountService struct {
	azdext.UnimplementedAccountServiceServer
	subscriptionsManager *account.SubscriptionsManager
}

func NewAccountService(subscriptionsManager *account.SubscriptionsManager) azdext.AccountServiceServer {
	return &accountService{
		subscriptionsManager: subscriptionsManager,
	}
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
	tenantId, err := s.subscriptionsManager.LookupTenant(ctx, req.SubscriptionId)
	if err != nil {
		return nil, err
	}

	return &azdext.LookupTenantResponse{
		TenantId: tenantId,
	}, nil
}
