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

func (ar *ActionResult) ToJson() (jsonBytes []byte) {
	if ar.Err != nil {
		jsonBytes, _ = json.Marshal(output.EventForMessage(ar.Err.Error()))
		return jsonBytes
	}
	result := ""
	if ar.SuccessMessage != "" {
		result = fmt.Sprintf("SUCCESS: %s", ar.SuccessMessage)
	}
	if ar.FollowUp != "" {
		result += fmt.Sprintf(". FOLLOW UP: %s", ar.FollowUp)
	}
	jsonBytes, _ = json.Marshal(output.EventForMessage(result))
	return jsonBytes
}
