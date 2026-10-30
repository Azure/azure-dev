// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package featureflags

import (
	"errors"
	"testing"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func TestRequireUsesProcessSwitch(t *testing.T) {
	for _, feature := range All() {
		t.Run(feature.ID, func(t *testing.T) {
			for _, value := range []string{"", "false", "on", "true"} {
				t.Setenv(feature.EnvVar, value)
				err := Require(feature)
				if value == "true" {
					require.NoError(t, err)
					continue
				}
				localErr, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok)
				require.Equal(t, exterrors.CodeFeatureNotEnabled, localErr.Code)
				require.Contains(t, localErr.Message, feature.ID)
				require.Contains(t, localErr.Suggestion, feature.EnvVar+"=true")
			}
		})
	}
}

func TestRequirePreservesDeclarationErrors(t *testing.T) {
	require.ErrorContains(t, Require(Feature{}), "feature ID and EnvVar must not be empty")
}

func TestFoundryACRReuseIsIndependent(t *testing.T) {
	for _, feature := range All() {
		t.Setenv(feature.EnvVar, "")
	}
	t.Setenv(FoundryACRReuse.EnvVar, "true")
	require.NoError(t, Require(FoundryACRReuse))
	require.ErrorContains(t, Require(Voice), Voice.ID)
	t.Setenv(FoundryACRReuse.EnvVar, "")
	for _, feature := range All() {
		if feature != FoundryACRReuse {
			t.Setenv(feature.EnvVar, "true")
		}
	}
	require.ErrorContains(t, Require(FoundryACRReuse), FoundryACRReuse.EnvVar)
}
