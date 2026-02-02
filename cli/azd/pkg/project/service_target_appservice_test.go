// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/stretchr/testify/require"
)

type serviceTargetValidationTest struct {
	targetResource *environment.TargetResource
	expectError    bool
}

func TestParseTaskArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		taskArgs    string
		expectedSrc string
		expectedDst string
	}{
		{
			name:        "empty args",
			taskArgs:    "",
			expectedSrc: "",
			expectedDst: "",
		},
		{
			name:        "src only",
			taskArgs:    "src=staging",
			expectedSrc: "staging",
			expectedDst: "",
		},
		{
			name:        "dst only",
			taskArgs:    "dst=staging",
			expectedSrc: "",
			expectedDst: "staging",
		},
		{
			name:        "both src and dst",
			taskArgs:    "src=staging;dst=test",
			expectedSrc: "staging",
			expectedDst: "test",
		},
		{
			name:        "@main as dst normalizes to empty string",
			taskArgs:    "dst=@main;src=test",
			expectedSrc: "test",
			expectedDst: "",
		},
		{
			name:        "@main as src normalizes to empty string",
			taskArgs:    "src=@main;dst=staging",
			expectedSrc: "",
			expectedDst: "staging",
		},
		{
			name:        "@Main (capitalized) normalizes to empty string",
			taskArgs:    "src=@Main;dst=Staging",
			expectedSrc: "",
			expectedDst: "Staging",
		},
		{
			name:        "@MAIN (uppercase) normalizes to empty string",
			taskArgs:    "src=@MAIN;dst=test",
			expectedSrc: "",
			expectedDst: "test",
		},
		{
			name:        "with whitespace",
			taskArgs:    "src = staging ; dst = @main",
			expectedSrc: "staging",
			expectedDst: "",
		},
		{
			name:        "invalid key ignored",
			taskArgs:    "foo=bar;src=staging",
			expectedSrc: "staging",
			expectedDst: "",
		},
		{
			name:        "production is NOT normalized (not a reserved keyword)",
			taskArgs:    "src=production;dst=test",
			expectedSrc: "production",
			expectedDst: "test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src, dst := parseTaskArgs(tt.taskArgs)
			require.Equal(t, tt.expectedSrc, src, "source slot mismatch")
			require.Equal(t, tt.expectedDst, dst, "destination slot mismatch")
		})
	}
}

func TestNewAppServiceTargetTypeValidation(t *testing.T) {
	t.Parallel()

	tests := map[string]*serviceTargetValidationTest{
		"ValidateTypeSuccess": {
			targetResource: environment.NewTargetResource("SUB_ID", "RG_ID", "res", string(azapi.AzureResourceTypeWebSite)),
			expectError:    false,
		},
		"ValidateTypeLowerCaseSuccess": {
			targetResource: environment.NewTargetResource(
				"SUB_ID",
				"RG_ID",
				"res",
				strings.ToLower(string(azapi.AzureResourceTypeWebSite)),
			),
			expectError: false,
		},
		"ValidateTypeFail": {
			targetResource: environment.NewTargetResource("SUB_ID", "RG_ID", "res", "BadType"),
			expectError:    true,
		},
	}

	for test, data := range tests {
		t.Run(test, func(t *testing.T) {
			serviceTarget := &appServiceTarget{}

			err := serviceTarget.validateTargetResource(data.targetResource)
			if data.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
