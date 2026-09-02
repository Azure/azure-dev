// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeploymentCapacityStructRoundTripSupportsEnvironmentReference(t *testing.T) {
	original := ServiceTargetAgentConfig{
		Deployments: []Deployment{{
			Name:  "${DEPLOYMENT_NAME}",
			Model: DeploymentModel{Name: "${MODEL_NAME}", Format: "OpenAI", Version: "1"},
			Sku:   DeploymentSku{Name: "GlobalStandard", Capacity: "${MODEL_CAPACITY}"},
		}},
	}

	value, err := MarshalStruct(&original)
	require.NoError(t, err)
	var roundTripped ServiceTargetAgentConfig
	require.NoError(t, UnmarshalStruct(value, &roundTripped))
	require.Len(t, roundTripped.Deployments, 1)
	assert.Equal(t, "${MODEL_CAPACITY}", roundTripped.Deployments[0].Sku.Capacity)
}
