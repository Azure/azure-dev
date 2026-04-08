// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestNewAccountService(t *testing.T) {
	t.Parallel()
	svc := NewAccountService(nil)
	require.NotNil(t, svc)
}

func TestAccountService_LookupTenant_EmptySubscriptionId(t *testing.T) {
	t.Parallel()
	svc := NewAccountService(nil)
	_, err := svc.LookupTenant(t.Context(), &azdext.LookupTenantRequest{
		SubscriptionId: "",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
	require.Contains(t, st.Message(), "subscription id is required")
}
