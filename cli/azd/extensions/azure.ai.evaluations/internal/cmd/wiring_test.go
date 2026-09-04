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
	for _, file := range []string{
		"eval_group.go",
		"../project/service_target_eval.go",
	} {
		t.Run(file, func(t *testing.T) {
			body, err := os.ReadFile(file)
			require.NoError(t, err)

			assert.Contains(t, string(body), "ReserveDeclared(ctx, cfg.Evals)",
				"an eval another declaration owns must not be adopted here")
		})
	}
}

// And the pin has to be handed to the read, not merely read from the config.
func TestTheRunHandsThePinToTheDatasetRead(t *testing.T) {
	body, err := os.ReadFile("run.go")
	require.NoError(t, err)

	assert.Contains(t, string(body), "declaredDatasetVersion(configPath, group), maxSamples)",
		"reading the declaration and not using it leaves the run on the recorded version")
}
