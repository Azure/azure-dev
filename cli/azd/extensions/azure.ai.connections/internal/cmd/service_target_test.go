// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDeploy_IsNoOp verifies the connection service target does not create the
// connection at deploy time. Connections declared as host: azure.ai.connection
// services are provisioned by the microsoft.foundry provider (synthesis) at
// provision time, so Deploy must return an empty result without any ARM call.
func TestDeploy_IsNoOp(t *testing.T) {
	t.Parallel()

	target := &connectionServiceTarget{}
	svc := &azdext.ServiceConfig{Name: "search-conn", Host: aiConnectionHost}

	var progressMsgs []string
	progress := func(msg string) { progressMsgs = append(progressMsgs, msg) }

	// A nil azdClient would panic if Deploy tried to reach the environment or
	// ARM; the no-op must not touch either.
	res, err := target.Deploy(t.Context(), svc, nil, nil, progress)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Len(t, progressMsgs, 1)
	assert.Contains(t, progressMsgs[0], "search-conn")
	assert.Contains(t, progressMsgs[0], "provisioned by infrastructure")
}

// TestPackagePublish_AreNoOps verifies the remaining lifecycle methods a
// connection has no build/publish artifact for return empty results.
func TestPackagePublish_AreNoOps(t *testing.T) {
	t.Parallel()

	target := &connectionServiceTarget{}
	svc := &azdext.ServiceConfig{Name: "search-conn", Host: aiConnectionHost}

	pkg, err := target.Package(t.Context(), svc, nil, nil)
	require.NoError(t, err)
	assert.NotNil(t, pkg)

	pub, err := target.Publish(t.Context(), svc, nil, nil, nil, nil)
	require.NoError(t, err)
	assert.NotNil(t, pub)

	endpoints, err := target.Endpoints(t.Context(), svc, nil)
	require.NoError(t, err)
	assert.Nil(t, endpoints)
}
