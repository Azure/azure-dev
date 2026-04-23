// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_springAppTarget_Initialize_ReturnsDeprecated(
	t *testing.T,
) {
	target := NewSpringAppTarget(nil, nil)
	err := target.Initialize(context.Background(), nil)

	require.Error(t, err)
	require.ErrorIs(t, err, errSpringAppDeprecated)
	require.Contains(t, err.Error(), "no longer supported")
}

func Test_springAppTarget_Package_ReturnsDeprecated(
	t *testing.T,
) {
	target := NewSpringAppTarget(nil, nil)
	result, err := target.Package(
		context.Background(), nil, nil, nil,
	)

	require.Nil(t, result)
	require.ErrorIs(t, err, errSpringAppDeprecated)
}

func Test_springAppTarget_Deploy_ReturnsDeprecated(
	t *testing.T,
) {
	target := NewSpringAppTarget(nil, nil)
	result, err := target.Deploy(
		context.Background(), nil, nil, nil, nil,
	)

	require.Nil(t, result)
	require.ErrorIs(t, err, errSpringAppDeprecated)
}

func Test_springAppTarget_Publish_ReturnsDeprecated(
	t *testing.T,
) {
	target := NewSpringAppTarget(nil, nil)
	result, err := target.Publish(
		context.Background(), nil, nil, nil, nil, nil,
	)

	require.Nil(t, result)
	require.ErrorIs(t, err, errSpringAppDeprecated)
}

func Test_springAppTarget_Endpoints_ReturnsDeprecated(
	t *testing.T,
) {
	target := NewSpringAppTarget(nil, nil)
	endpoints, err := target.Endpoints(
		context.Background(), nil, nil,
	)

	require.Nil(t, endpoints)
	require.ErrorIs(t, err, errSpringAppDeprecated)
}

func Test_springAppTarget_RequiredExternalTools_Empty(
	t *testing.T,
) {
	target := NewSpringAppTarget(nil, nil)
	tools := target.RequiredExternalTools(
		context.Background(), nil,
	)

	require.NotNil(t, tools)
	require.Empty(t, tools)
}
