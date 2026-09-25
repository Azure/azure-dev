// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent

import (
	"fmt"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	configDir, err := os.MkdirTemp("", "azd-agent-tests-*")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("AZD_CONFIG_DIR", configDir); err != nil {
		panic(err)
	}
	if err := os.Setenv("AZURE_DEV_COLLECT_TELEMETRY", "no"); err != nil {
		panic(err)
	}

	code := m.Run()
	if err := os.RemoveAll(configDir); err != nil {
		fmt.Fprintf(os.Stderr, "failed to remove temporary azd config directory %q: %v\n", configDir, err)
	}
	os.Exit(code)
}
