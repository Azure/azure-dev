// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
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
	return nil
}

func (ar *ActionResult) ToTable() string {
	return ""
}
