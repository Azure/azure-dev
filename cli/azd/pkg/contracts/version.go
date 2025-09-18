// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
package contracts

// VersionResult is the contract for the output of `azd version`
type VersionResult struct {
	Azd struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
	} `json:"azd"`
}
