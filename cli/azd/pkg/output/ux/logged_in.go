// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"encoding/json"
	"fmt"

	"github.com/azure/azure-dev/pkg/output"
)

const cLoginSuccessMessage = "Logged in to Azure"
const (
	EmailLoginType    LoginType = "email"
	ClientIdLoginType LoginType = "clientId"
)

type LoginType string

type LoggedIn struct {
	LoggedInAs string
	LoginType  LoginType
}

func (cr *LoggedIn) ToString(currentIndentation string) string {
	switch cr.LoginType {
	case EmailLoginType:
		return fmt.Sprintf(
			"%s%s as %s",
			currentIndentation,
			cLoginSuccessMessage,
			output.WithBold("%s", cr.LoggedInAs))
	case ClientIdLoginType:
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
