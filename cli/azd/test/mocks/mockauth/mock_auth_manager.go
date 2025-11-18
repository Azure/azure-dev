// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package mockauth

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/stretchr/testify/mock"
)

type MockAuthManager struct {
	mock.Mock
}

func (m *MockAuthManager) ClaimsForCurrentUser(
	ctx context.Context,
	options *auth.ClaimsForCurrentUserOptions,
) (auth.TokenClaims, error) {
	args := m.Called(ctx, options)
	return args.Get(0).(auth.TokenClaims), args.Error(1)
}
