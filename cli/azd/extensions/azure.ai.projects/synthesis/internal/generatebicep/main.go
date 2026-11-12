// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"azure.ai.projects/synthesis"
)

func main() {
	if err := generate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func generate() error {
	for _, name := range []string{
		"templates/main.bicep", "templates/modules/resources.bicep", "templates/modules/acr.bicep",
	} {
		data, err := synthesis.RenderBicepTemplate(name, true)
		if err != nil {
			return err
		}
		//nolint:gosec // Repository-owned generated Bicep source.
		if err := os.WriteFile(filepath.FromSlash(name), data, 0o644); err != nil {
			return fmt.Errorf("write generated Bicep %s: %w", name, err)
		}
	}
	return nil
}
