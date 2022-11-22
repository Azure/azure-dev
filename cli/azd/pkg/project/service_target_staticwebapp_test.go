// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/stretchr/testify/require"
)

func TestNewStaticWebAppTargetTypeValidation(t *testing.T) {
	t.Parallel()

	t.Run("ValidateTypeSuccess", func(t *testing.T) {
		_, err := NewStaticWebAppTarget(
			nil,
			nil,
			environment.NewTargetResource("SUB_ID", "RG_ID", "res", string(infra.AzureResourceTypeStaticWebSite)),
			nil,
			nil,
		)

		require.NoError(t, err)
	})

	t.Run("ValidateTypeLowerCaseSuccess", func(t *testing.T) {
		_, err := NewStaticWebAppTarget(
			nil,
			nil,
			environment.NewTargetResource(
				"SUB_ID", "RG_ID", "res", strings.ToLower(string(infra.AzureResourceTypeStaticWebSite)),
			),
			nil,
			nil,
		)

		require.NoError(t, err)
	})

	t.Run("ValidateTypeFail", func(t *testing.T) {
		_, err := NewStaticWebAppTarget(
			nil,
			nil,
			environment.NewTargetResource("SUB_ID", "RG_ID", "res", "BadType"),
			nil,
			nil,
		)

		require.Error(t, err)
	})
}
