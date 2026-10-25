// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	configDir, err := os.MkdirTemp("", "azure-ai-agents-tests-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create test AZD_CONFIG_DIR: %v\n", err)
		os.Exit(1)
	}
	if err := os.Setenv("AZD_CONFIG_DIR", configDir); err != nil {
		fmt.Fprintf(os.Stderr, "set test AZD_CONFIG_DIR: %v\n", err)
		_ = os.RemoveAll(configDir)
		os.Exit(1)
	}

	code := m.Run()
	if err := os.RemoveAll(configDir); err != nil && code == 0 {
		fmt.Fprintf(os.Stderr, "remove test AZD_CONFIG_DIR: %v\n", err)
		code = 1
	}
	os.Exit(code)
}
