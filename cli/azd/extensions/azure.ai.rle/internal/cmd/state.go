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
	rleStateFile = ".azd-rle.json"
)

type rleState struct {
	EnvironmentName     string `json:"environmentName"`
	ProjectEndpoint     string `json:"projectEndpoint,omitempty"`
	EnvironmentId       string `json:"environmentId,omitempty"`
	EnvironmentVersion  string `json:"environmentVersion,omitempty"`
	runtimeRouteVersion string
}

func defaultRleState(name string) rleState {
	return rleState{
		EnvironmentName: name,
	}
}

func (s *rleState) UnmarshalJSON(data []byte) error {
	var persisted struct {
		EnvironmentName    string `json:"environmentName"`
		LegacyName         string `json:"name"`
		ProjectEndpoint    string `json:"projectEndpoint"`
		EnvironmentId      string `json:"environmentId"`
		EnvironmentVersion string `json:"environmentVersion"`
	}
	if err := json.Unmarshal(data, &persisted); err != nil {
		return err
	}

	s.EnvironmentName = firstNonEmpty(persisted.EnvironmentName, persisted.LegacyName)
	s.ProjectEndpoint = persisted.ProjectEndpoint
	s.EnvironmentId = persisted.EnvironmentId
	s.EnvironmentVersion = persisted.EnvironmentVersion
	return nil
}

func loadRleState() (rleState, error) {
	data, err := os.ReadFile(stateFilePath("."))
	if errors.Is(err, os.ErrNotExist) {
		return rleState{}, &azdext.LocalError{
			Message:    "RLE session has not been initialized.",
			Code:       "rle_project_not_initialized",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Run azd ai rle init first, then run commands from the created session folder.",
		}
	}
	if err != nil {
		return rleState{}, err
	}

	var state rleState
	if err := json.Unmarshal(data, &state); err != nil {
		return rleState{}, err
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
