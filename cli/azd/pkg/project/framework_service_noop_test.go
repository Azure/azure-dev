// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_noOpProject_Requirements(t *testing.T) {
	svc := NewNoOpProject(nil)
	reqs := svc.Requirements()

	require.False(t, reqs.Package.RequireRestore)
	require.False(t, reqs.Package.RequireBuild)
}

func Test_noOpProject_RequiredExternalTools(t *testing.T) {
	svc := NewNoOpProject(nil)
	tools := svc.RequiredExternalTools(
		context.Background(), nil,
	)

	require.NotNil(t, tools)
	require.Empty(t, tools)
}

func Test_noOpProject_Initialize(t *testing.T) {
	svc := NewNoOpProject(nil)
	err := svc.Initialize(context.Background(), nil)
	require.NoError(t, err)
}

func Test_noOpProject_Restore(t *testing.T) {
	svc := NewNoOpProject(nil)
	result, err := svc.Restore(
		context.Background(), nil, nil, nil,
	)

	require.NoError(t, err)
	require.NotNil(t, result)
}

func Test_noOpProject_Build(t *testing.T) {
	svc := NewNoOpProject(nil)
	result, err := svc.Build(
		context.Background(), nil, nil, nil,
	)

	require.NoError(t, err)
	require.NotNil(t, result)
}

func Test_noOpProject_Package(t *testing.T) {
	svc := NewNoOpProject(nil)
	result, err := svc.Package(
		context.Background(), nil, nil, nil,
	)

	require.NoError(t, err)
	require.NotNil(t, result)
}
