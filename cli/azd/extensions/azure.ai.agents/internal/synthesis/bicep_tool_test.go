// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
)

func lookupBicep(t *testing.T) string {
	t.Helper()
	override := os.Getenv("AZD_BICEP_TOOL_PATH")
	path, err := findBicep(t.Context(), override)
	if err != nil {
		if override != "" {
			t.Fatal(err)
		}
		t.Skip(err)
	}
	return path
}

func findBicep(ctx context.Context, override string) (string, error) {
	var candidates []string
	if override != "" {
		candidates = append(candidates, override)
	} else {
		if path, err := exec.LookPath("bicep"); err == nil {
			candidates = append(candidates, path)
		}
		if home, err := os.UserHomeDir(); err == nil {
			for _, name := range []string{"bicep", "bicep.exe"} {
				path := filepath.Join(home, ".azure", "bin", name)
				if _, err := os.Stat(path); err == nil {
					candidates = append(candidates, path)
				}
			}
		}
	}
	var rejected []string
	for _, path := range candidates {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		//nolint:gosec // Explicit override or locally discovered Bicep; never installs or upgrades tools.
		output, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
		cancel()
		if err != nil {
			err = fmt.Errorf("read Bicep version: %w: %s", err, strings.TrimSpace(string(output)))
		} else {
			err = validateBicepVersion(string(output))
		}
		if err == nil {
			return path, nil
		}
		rejected = append(rejected, fmt.Sprintf("%s: %v", path, err))
	}
	return "", fmt.Errorf("no supported Bicep CLI found (azd SDK minimum %s); "+
		"set AZD_BICEP_TOOL_PATH to a supported binary or add it to PATH; rejected: %s",
		bicep.Version, strings.Join(rejected, "; "))
}

func validateBicepVersion(output string) error {
	match := regexp.MustCompile(`^Bicep CLI version (\d+\.\d+\.\d+)(?:\s|$)`).FindStringSubmatch(strings.TrimSpace(output))
	if len(match) != 2 {
		return fmt.Errorf("unrecognized Bicep version output %q", strings.TrimSpace(output))
	}
	version, err := tools.ExtractVersion(match[1])
	if err != nil {
		return fmt.Errorf("parse Bicep version: %w", err)
	}
	if version.LT(bicep.Version) {
		return fmt.Errorf("Bicep version %s is below azd SDK minimum %s", version, bicep.Version)
	}
	return nil
}
