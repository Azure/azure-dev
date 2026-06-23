// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

const (
	rleStateFile    = ".azd-rle.json"
	rleManifestFile = "rle.yaml"
)

type rleState struct {
	Name               string `json:"name"`
	Recipe             string `json:"recipe"`
	Image              string `json:"image,omitempty"`
	Account            string `json:"account"`
	Project            string `json:"project"`
	Endpoint           string `json:"endpoint,omitempty"`
	EnvironmentId      string `json:"environmentId,omitempty"`
	EnvironmentVersion string `json:"environmentVersion,omitempty"`
	InstanceId         string `json:"instanceId,omitempty"`
	InstanceEndpoint   string `json:"instanceEndpoint,omitempty"`
}

func defaultRleState(name string, recipe string) rleState {
	return rleState{
		Name:    name,
		Recipe:  recipe,
		Account: defaultAccountName,
		Project: defaultProjectName,
	}
}

func loadRleState() (rleState, error) {
	data, err := os.ReadFile(stateFilePath("."))
	if errors.Is(err, os.ErrNotExist) {
		return rleState{}, &azdext.LocalError{
			Message:    "RLE session has not been initialized.",
			Code:       "rle_project_not_initialized",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Run azd ai rle init <env-name> first, then run commands from the created session folder.",
		}
	}
	if err != nil {
		return rleState{}, err
	}

	var state rleState
	if err := json.Unmarshal(data, &state); err != nil {
		return rleState{}, err
	}
	if state.Account == "" {
		state.Account = defaultAccountName
	}
	if state.Project == "" {
		state.Project = defaultProjectName
	}
	if state.Recipe == "" {
		state.Recipe = defaultRecipeName
	}
	return state, nil
}

func saveRleState(state rleState) error {
	return saveRleStateIn(".", state)
}

func saveRleStateIn(dir string, state rleState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(stateFilePath(dir), append(data, '\n'), 0600)
}

func stateFilePath(dir string) string {
	return filepath.Join(dir, rleStateFile)
}
