// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"fmt"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/stretchr/testify/require"
)

func Test_pwshFailureGuidance(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantHint string
	}{
		{
			name:     "NonExitError",
			err:      fmt.Errorf("some error"),
			wantHint: "",
		},
		{
			name:     "NonPwshCommand",
			err:      exec.NewTestExitError("node", 1, "Import-Module: not loaded"),
			wantHint: "",
		},
		{
			name: "MissingModule",
			err: exec.NewTestExitError(
				"pwsh", 1,
				"Import-Module: The specified module 'Az.CognitiveServices' was not loaded"+
					" because no valid module file was found.",
			),
			wantHint: "A required PowerShell module could not be loaded. " +
				"Install the missing module with 'Install-Module <ModuleName> -Scope CurrentUser'.",
		},
		{
			name: "AzModuleNotFound",
			err: exec.NewTestExitError(
				"pwsh", 1,
				"The term 'Az.Accounts' is not recognized as a name of a cmdlet",
			),
			wantHint: "The Azure PowerShell module (Az) is required but not installed. " +
				"Install it with 'Install-Module Az -Scope CurrentUser -Repository PSGallery -Force'.",
		},
		{
			name: "ExecutionPolicy",
			err: exec.NewTestExitError(
				"powershell.exe", 1,
				"UnauthorizedAccess: File script.ps1 cannot be loaded.",
			),
			wantHint: "PowerShell execution policy is blocking the script. " +
				"Check your policy with 'Get-ExecutionPolicy' and consider setting it with " +
				"'Set-ExecutionPolicy -ExecutionPolicy RemoteSigned -Scope CurrentUser'.",
		},
		{
			name: "AuthExpired",
			err: exec.NewTestExitError(
				"pwsh", 1,
				"Please run Connect-AzAccount to set up your credentials.",
			),
			wantHint: "The Azure authentication session may have expired. " +
				"Try running 'azd auth login' again.",
		},
		{
			name: "ErrorActionPreference",
			err: exec.NewTestExitError(
				"pwsh", 1,
				"ErrorActionPreference is set incorrectly",
			),
			wantHint: "The hook script has an issue with error handling configuration. " +
				"Ensure '$ErrorActionPreference = \"Stop\"' is set at the top of the script.",
		},
		{
			name: "ConnectAzAccountNotRecognized",
			err: exec.NewTestExitError(
				"pwsh", 1,
				"Connect-AzAccount: The term 'Connect-AzAccount' is not recognized as a name of a cmdlet",
			),
			wantHint: "",
		},
		{
			name: "LoginExpired",
			err: exec.NewTestExitError(
				"pwsh", 1,
				"login token has expired",
			),
			wantHint: "The Azure authentication session may have expired. " +
				"Try running 'azd auth login' again.",
		},
		{
			name: "NonAzureAzPrefixedNotFound",
			err: exec.NewTestExitError(
				"pwsh", 1,
				"The term 'MyAz.Custom' is not recognized as a name of a cmdlet",
			),
			wantHint: "",
		},
		{
			name: "ConnectAzAccountSucceeded",
			err: exec.NewTestExitError(
				"pwsh", 1,
				"Connect-AzAccount succeeded but something else failed",
			),
			wantHint: "",
		},
		{
			name: "NoMatchingPattern",
			err: exec.NewTestExitError(
				"pwsh", 1,
				"Some random error occurred",
			),
			wantHint: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pwshFailureGuidance(tt.err)
			require.Equal(t, tt.wantHint, got)
		})
	}
}
