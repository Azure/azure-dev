// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
)

// OutputParameter represents a single output value from a script.
type OutputParameter struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

// OutputCollector discovers and parses outputs.json files produced by scripts.
type OutputCollector struct {
	projectPath string
}

// NewOutputCollector creates a new OutputCollector rooted at the given project path.
func NewOutputCollector(projectPath string) *OutputCollector {
	return &OutputCollector{projectPath: projectPath}
}

// maxOutputFileSize is the maximum allowed size for an outputs.json file (10 MB).
const maxOutputFileSize = 10 * 1024 * 1024

// Collect looks for an outputs.json file in the same directory as the script
// and parses it into a map of output parameters. If no file is found, returns an empty map.
func (c *OutputCollector) Collect(sc *ScriptConfig) (map[string]OutputParameter, error) {
	scriptDir := filepath.Dir(filepath.Join(c.projectPath, sc.Run))
	outputsPath := filepath.Join(scriptDir, "outputs.json")

	fi, err := os.Stat(outputsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("checking outputs file %q: %w", outputsPath, err)
	}

	if fi.Size() > maxOutputFileSize {
		return nil, fmt.Errorf(
			"outputs file %q exceeds maximum allowed size of %d bytes (actual: %d bytes)",
			outputsPath, maxOutputFileSize, fi.Size(),
		)
	}

	data, err := os.ReadFile(outputsPath)
	if err != nil {
		return nil, fmt.Errorf("reading outputs file %q: %w", outputsPath, err)
	}

	var outputs map[string]OutputParameter
	if err := json.Unmarshal(data, &outputs); err != nil {
		return nil, fmt.Errorf("parsing outputs file %q: %w", outputsPath, err)
	}

	return outputs, nil
}

// MergeOutputs combines multiple output maps into a single map.
// Later entries override earlier ones.
func MergeOutputs(outputs ...map[string]OutputParameter) map[string]OutputParameter {
	merged := make(map[string]OutputParameter)
	for _, m := range outputs {
		maps.Copy(merged, m)
	}
	return merged
}
