// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"encoding/json"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

const cLoginSuccessMessage = "Logged in to Azure"
const (
	UserLoginType             LoginType = "User"
	ServicePrincipalLoginType LoginType = "ServicePrincipal"
)

type LoginType string

type LoggedIn struct {
	LoggedInAs string
	LoginType  LoginType
}

func (cr *LoggedIn) ToString(currentIndentation string) string {
	switch cr.LoginType {
	case UserLoginType:
		return fmt.Sprintf(
			"%s%s as %s",
			currentIndentation,
			cLoginSuccessMessage,
			output.WithBold("%s", cr.LoggedInAs))
	case ServicePrincipalLoginType:
		return fmt.Sprintf(
			"%s%s as (%s)",
			currentIndentation,
			cLoginSuccessMessage,
			output.WithGrayFormat("%s", cr.LoggedInAs))
	default:
	}

	return fmt.Sprintf(
		"%s%s",
		currentIndentation,
		cLoginSuccessMessage)
}

func (cr *LoggedIn) MarshalJSON() ([]byte, error) {
	// reusing the same envelope from console messages
	return json.Marshal(output.EventForMessage(
		fmt.Sprintf("%s as %s", cLoginSuccessMessage, cr.LoggedInAs)))
}
