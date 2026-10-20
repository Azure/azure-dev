// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTerraformVariablesUseTopLevelRegistryInputs(t *testing.T) {
	data, err := TerraformTemplatesFS().ReadFile("templates/terraform/variables.tf")
	require.NoError(t, err)
	for _, name := range []string{"agent_hosting", "container_registry_resource_id", "container_registry_abac"} {
		assert.Contains(t, string(data), "\nvariable \""+name+"\" {")
	}

	terraform, err := exec.LookPath("terraform")
	if err != nil {
		t.Skip("Terraform CLI not found on PATH; skipping variable schema validation")
	}

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "variables.tf"), data, 0o600))
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	//nolint:gosec // terraform comes from PATH; validation uses only test-owned input variables.
	cmd := exec.CommandContext(ctx, terraform, "validate", "-json", "-no-color")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"NO_COLOR=1",
		"TF_IN_AUTOMATION=1",
		"CHECKPOINT_DISABLE=1",
		"TF_CLI_ARGS=",
		"TF_CLI_ARGS_validate=",
		"TF_DATA_DIR="+filepath.Join(dir, ".terraform"),
	)
	result, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "Terraform variable schema validation failed: %s", result)

	var report struct {
		Valid      bool `json:"valid"`
		ErrorCount int  `json:"error_count"`
	}
	require.NoError(t, json.Unmarshal(result, &report))
	assert.True(t, report.Valid, "%s", result)
	assert.Zero(t, report.ErrorCount, "%s", result)
}
