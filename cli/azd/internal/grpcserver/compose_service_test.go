// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_ListResourceTypes_ReturnsResources(t *testing.T) {
	// Setup a mock context.
	mockContext := mocks.NewMockContext(context.Background())
	lazyAzdContext := lazy.NewLazy(func() (*azdcontext.AzdContext, error) {
		return nil, azdcontext.ErrNoProject
	})

	// Create the service and call ListResourceTypes
	service := NewComposeService(lazyAzdContext)
	response, err := service.ListResourceTypes(*mockContext.Context, &azdext.EmptyRequest{})
	require.NoError(t, err)
	require.NotNil(t, response)
	require.NotEmpty(t, response.ResourceTypes)

	// Verify a resource type.
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	randomIndex := r.Intn(len(response.ResourceTypes) - 1)
	randomResource := response.ResourceTypes[randomIndex]
	require.NotNil(t, randomResource)
	require.NotEmpty(t, randomResource.Name)
	require.NotEmpty(t, randomResource.DisplayName)
	require.NotEmpty(t, randomResource.Type)
}
