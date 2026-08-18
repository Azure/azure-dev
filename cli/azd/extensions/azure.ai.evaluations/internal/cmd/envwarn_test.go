// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// The atomic commands are meant to work standalone against the data plane, so
// running outside a project is ordinary. Warning about nowhere to persist would
// be noise on every standalone invocation.
func TestNoAzdEnvironmentIsRecognizable(t *testing.T) {
	err := fmt.Errorf("%w to write %s into", errNoAzdEnvironment, "EVAL_RUN_ID")

	require.ErrorIs(t, err, errNoAzdEnvironment,
		"callers rely on telling this apart from a failed write")
	require.Contains(t, err.Error(), "EVAL_RUN_ID",
		"the key is still named when the message is shown")
}

// A write that fails for any other reason stays reportable.
func TestOtherEnvironmentFailuresStayReportable(t *testing.T) {
	err := fmt.Errorf("writing %s to the azd environment: %w", "EVAL_RUN_ID", errors.New("rpc failed"))

	require.NotErrorIs(t, err, errNoAzdEnvironment)
	require.Contains(t, err.Error(), "rpc failed")
}
