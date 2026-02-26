// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"encoding/json"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

// authModeAzCli is the display string for Azure CLI delegated auth mode.
// This must match the value of auth.AzDelegated.
const authModeAzCli = "az cli"

// AuthStatusView renders a contracts.StatusResult for console output.
type AuthStatusView struct {
	Result *contracts.StatusResult
	// AuthMode indicates the current authentication mode.
	// When set to a non-built-in mode, the unauthenticated message adjusts guidance accordingly.
	AuthMode string
}

func (v *AuthStatusView) ToString(currentIndentation string) string {
	if v.Result.Status == contracts.AuthStatusUnauthenticated {
		if v.AuthMode == authModeAzCli {
			return fmt.Sprintf(
				"%sNot logged in to Azure. Run `az login` to log in or authenticate using `azd`.", currentIndentation)
		}
		return fmt.Sprintf("%sNot logged in, run `azd auth login` to login to Azure", currentIndentation)
	}

	switch v.Result.Type {
	case contracts.AccountTypeUser:
		return fmt.Sprintf("%sLogged in to Azure as %s",
			currentIndentation,
			output.WithBold("%s", v.Result.Email))
	case contracts.AccountTypeServicePrincipal:
		return fmt.Sprintf("%sLogged in to Azure as (%s)",
			currentIndentation,
			output.WithGrayFormat("%s", v.Result.ClientID))
	}

	return fmt.Sprintf("%sLogged in to Azure", currentIndentation)
}

func (v *AuthStatusView) MarshalJSON() ([]byte, error) {
	return json.Marshal(v.Result)
}
