// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/stretchr/testify/require"
)

func TestNewFrameworkService(t *testing.T) {
	t.Parallel()
	container := ioc.NewNestedContainer(nil)
	svc := NewFrameworkService(container, nil)
	require.NotNil(t, svc)
}

func TestNewServiceTargetService(t *testing.T) {
	t.Parallel()
	container := ioc.NewNestedContainer(nil)
	svc := NewServiceTargetService(container, nil, nil)
	require.NotNil(t, svc)
}
