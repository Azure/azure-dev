// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package contracts

type EnvListEnvironment struct {
	Name       string `json:"Name"`
	IsDefault  bool   `json:"IsDefault"`
	DotEnvPath string `json:"DotEnvPath"`
	ConfigPath string `json:"ConfigPath"`
	IsValid    bool   `json:"IsValid"`
}
