// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"encoding/json"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

type ActionResult struct {
	SuccessMessage string
	FollowUp       string
	Err            error
}

func (ar *ActionResult) ToString(currentIndentation string) (result string) {
	if ar.Err != nil {
		return output.WithErrorFormat("\n%s: %s", "ERROR", ar.Err.Error())
	}
	if ar.SuccessMessage != "" {
		result = output.WithSuccessFormat("\n%s: %s", "SUCCESS", ar.SuccessMessage)
	}
	if ar.FollowUp != "" {
		result += fmt.Sprintf("\n%s", ar.FollowUp)
	}
	return result
}

func (ar *ActionResult) MarshalJSON() ([]byte, error) {
	if ar.Err != nil {
		return json.Marshal(output.EventForMessage(ar.Err.Error()))
	}
	result := ""
	if ar.SuccessMessage != "" {
		result = fmt.Sprintf("SUCCESS: %s", ar.SuccessMessage)
	}
	if ar.FollowUp != "" {
		result += fmt.Sprintf(". FOLLOW UP: %s", ar.FollowUp)
	}
	return json.Marshal(output.EventForMessage(result))
}
