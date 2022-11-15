// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"encoding/json"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

type ActionResult struct {
	SuccessMessage string
	FollowUp       string
	Err            error
}

func (ar *ActionResult) ToString(currentIndentation string) string {
	if ar.Err != nil {
		return output.WithErrorFormat("\n%s: %s", "ERROR", ar.Err.Error())
	}

	return output.WithSuccessFormat("\n%s: %s", "SUCCESS", ar.SuccessMessage)
}

func (ar *ActionResult) ToJson() []byte {
	if ar.Err != nil {
		jsonBytes, _ := json.Marshal(output.EventForMessage(ar.Err.Error()))
		return jsonBytes
	}
	return nil
}
