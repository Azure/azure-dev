// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package resources

import (
	"fmt"

	"azure.resources/internal/resources/scaffold"
)

// Generate returns Bicep and its parameter file, without writing or invoking Azure.
func Generate(entry string, config Config) (map[string][]byte, error) {
	if err := ValidateEntryName(entry); err != nil {
		return nil, err
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}

	spec, err := generateInfraSpec(config)
	if err != nil {
		return nil, fmt.Errorf("generating infrastructure spec: %w", err)
	}
	spec.LayerName = entry
	files, err := scaffold.ExecInfraFs(*spec)
	if err != nil {
		return nil, fmt.Errorf("generating infrastructure files: %w", err)
	}
	return files, nil
}
