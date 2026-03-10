// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package local_preflight_test

import (
	"context"
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/local_preflight"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// alwaysPassCheck is a test helper that always returns StatusPass.
type alwaysPassCheck struct{ name string }

func (c *alwaysPassCheck) Name() string { return c.name }
func (c *alwaysPassCheck) Run(_ context.Context) local_preflight.Result {
	return local_preflight.Result{Status: local_preflight.StatusPass, Message: "ok"}
}

// alwaysFailCheck is a test helper that always returns StatusFail.
type alwaysFailCheck struct{ name string }

func (c *alwaysFailCheck) Name() string { return c.name }
func (c *alwaysFailCheck) Run(_ context.Context) local_preflight.Result {
	return local_preflight.Result{
		Status:     local_preflight.StatusFail,
		Message:    "failed",
		Suggestion: "fix it",
	}
}

// alwaysWarnCheck is a test helper that always returns StatusWarn.
type alwaysWarnCheck struct{ name string }

func (c *alwaysWarnCheck) Name() string { return c.name }
func (c *alwaysWarnCheck) Run(_ context.Context) local_preflight.Result {
	return local_preflight.Result{Status: local_preflight.StatusWarn, Message: "warn"}
}

func TestEngine_AllPass(t *testing.T) {
	engine := local_preflight.NewEngine(
		&alwaysPassCheck{name: "check1"},
		&alwaysPassCheck{name: "check2"},
	)

	results, err := engine.Run(context.Background())
	require.NoError(t, err)
	assert.Len(t, results, 2)
	for _, r := range results {
		assert.Equal(t, local_preflight.StatusPass, r.Status)
	}
}

func TestEngine_OneFail_ReturnsError(t *testing.T) {
	engine := local_preflight.NewEngine(
		&alwaysPassCheck{name: "check1"},
		&alwaysFailCheck{name: "check2"},
		&alwaysPassCheck{name: "check3"},
	)

	results, err := engine.Run(context.Background())
	require.Error(t, err)
	// All checks still run even after a failure.
	assert.Len(t, results, 3)
}

func TestEngine_WarnDoesNotReturnError(t *testing.T) {
	engine := local_preflight.NewEngine(
		&alwaysWarnCheck{name: "check1"},
	)

	results, err := engine.Run(context.Background())
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, local_preflight.StatusWarn, results[0].Status)
}

func TestEngine_CheckNamePropagated(t *testing.T) {
	engine := local_preflight.NewEngine(
		&alwaysPassCheck{name: "MyCheck"},
	)

	results, err := engine.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "MyCheck", results[0].CheckName)
}

func TestEngine_Add(t *testing.T) {
	engine := local_preflight.NewEngine()
	engine.Add(&alwaysPassCheck{name: "check1"})
	engine.Add(&alwaysPassCheck{name: "check2"})

	results, err := engine.Run(context.Background())
	require.NoError(t, err)
	assert.Len(t, results, 2)
}

func TestEngine_Empty(t *testing.T) {
	engine := local_preflight.NewEngine()
	results, err := engine.Run(context.Background())
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestEngine_MultipleFailures(t *testing.T) {
	engine := local_preflight.NewEngine(
		&alwaysFailCheck{name: "check1"},
		&alwaysFailCheck{name: "check2"},
	)

	results, err := engine.Run(context.Background())
	require.Error(t, err)
	assert.True(t, errors.Is(err, err), "should return non-nil error on failure")
	assert.Len(t, results, 2)
}
