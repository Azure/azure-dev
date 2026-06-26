// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdo

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/entraid"
)

func TestCreateAzureRMServiceEndPointArgs_ServicePrincipalKey(t *testing.T) {
	t.Parallel()

	projectId := uuid.New().String()
	projectName := "demo-project"
	creds := &entraid.AzureCredentials{
		SubscriptionId: "sub-id",
		TenantId:       "tenant-id",
		ClientId:       "client-id",
		ClientSecret:   "shh-secret",
	}

	args, err := createAzureRMServiceEndPointArgs(&projectId, &projectName, creds)
	require.NoError(t, err)

	ep := args.Endpoint
	require.NotNil(t, ep.Authorization.Scheme)
	assert.Equal(t, "ServicePrincipal", *ep.Authorization.Scheme)
	params := *ep.Authorization.Parameters
	assert.Equal(t, "shh-secret", params["serviceprincipalkey"])
	assert.Equal(t, "spnKey", params["authenticationType"])
}
