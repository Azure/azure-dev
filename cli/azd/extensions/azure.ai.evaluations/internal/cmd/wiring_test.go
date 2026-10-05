// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The reconciler's guard is worth nothing if the commands do not arm it, and a
// unit test that calls ReserveDeclared itself proves only the method. Read from
// the source because reaching either call site needs a project and a service.
func TestBothCommandsReserveBeforeTheyReconcile(t *testing.T) {
	for file, call := range map[string]string{
		"eval_group.go":                     "ReserveDeclared(ctx, declared)",
		"../project/service_target_eval.go": "ReserveDeclared(ctx, cfg.Evals)",
	} {
		t.Run(file, func(t *testing.T) {
			body, err := os.ReadFile(file)
			require.NoError(t, err)

			assert.Contains(t, string(body), call,
				"an eval another declaration owns must not be adopted here")
		})
	}
}

// And the pin has to be handed to the read, not merely read from the config.
func TestTheRunHandsThePinToTheDatasetRead(t *testing.T) {
	body, err := os.ReadFile("run.go")
	require.NoError(t, err)

	assert.Contains(t, string(body), "ctx, group.Dataset, decl.Version, localPath != \"\")",
		"reading the declaration and not using it leaves the run on the recorded version")
	assert.Contains(t, string(body), "metadata[metaDatasetVersion] = datasetVersion",
		"metadata must use the version resolved with the source, not look it up again")
}
