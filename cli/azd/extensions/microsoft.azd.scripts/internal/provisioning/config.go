// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ProviderConfig is the top-level configuration parsed from infra.config in azure.yaml.
type ProviderConfig struct {
	Provision []*ScriptConfig `json:"provision,omitempty"`
	Destroy   []*ScriptConfig `json:"destroy,omitempty"`
}

// ScriptConfig defines the configuration for a single script entry.
// Field names align with HookConfig (pkg/ext/models.go) for user familiarity.
type ScriptConfig struct {
	Kind            string            `json:"kind,omitempty"`
	Shell           string            `json:"shell,omitempty"` // deprecated alias for Kind
	Run             string            `json:"run"`
	Name            string            `json:"name,omitempty"`
	Env             map[string]string `json:"env,omitempty"`
	Secrets         map[string]string `json:"secrets,omitempty"`
	ContinueOnError bool              `json:"continueOnError,omitempty"`
	Windows         *ScriptConfig     `json:"windows,omitempty"`
	Posix           *ScriptConfig     `json:"posix,omitempty"`
}

// ParseProviderConfig converts a raw map (from protobuf Struct) into a typed ProviderConfig.
// Uses the JSON re-marshal pattern established in azd (tools.UnmarshalHookConfig).
func ParseProviderConfig(raw map[string]any) (*ProviderConfig, error) {
	if len(raw) == 0 {
		return &ProviderConfig{}, nil
	}

	data, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("marshalling provider config to JSON: %w", err)
	}

	var cfg ProviderConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("unmarshalling provider config: %w", err)
	}

	// Apply platform-specific overrides and normalize all scripts.
	for i, sc := range cfg.Provision {
		applyPlatformOverride(sc)
		normalizeKind(sc)
		if sc.Name == "" {
			sc.Name = fmt.Sprintf("provision[%d]", i)
		}
	}
	for i, sc := range cfg.Destroy {
		applyPlatformOverride(sc)
		normalizeKind(sc)
		if sc.Name == "" {
			sc.Name = fmt.Sprintf("destroy[%d]", i)
		}
	}

	return &cfg, nil
}

// Validate checks all script entries for correctness.
func (c *ProviderConfig) Validate(projectPath string) error {
	if len(c.Provision) == 0 && len(c.Destroy) == 0 {
		return fmt.Errorf(
			"invalid script provider configuration: at least one 'provision' or 'destroy' script entry is required",
		)
	}

	for i, sc := range c.Provision {
		if err := validateScriptConfig(sc, projectPath, i, "provision"); err != nil {
			return err
		}
	}
	for i, sc := range c.Destroy {
		if err := validateScriptConfig(sc, projectPath, i, "destroy"); err != nil {
			return err
		}
	}

	return nil
}

func validateScriptConfig(sc *ScriptConfig, projectPath string, index int, section string) error {
	if strings.TrimSpace(sc.Run) == "" {
		return fmt.Errorf("invalid script configuration in %s[%d]: 'run' is required", section, index)
	}

	if filepath.IsAbs(sc.Run) {
		return fmt.Errorf(
			"invalid script configuration in %s[%d]: script path must be relative, got %q",
			section, index, sc.Run,
		)
	}

	// Reject path traversal
	cleaned := filepath.Clean(sc.Run)
	if strings.HasPrefix(cleaned, "..") {
		return fmt.Errorf(
			"invalid script configuration in %s[%d]: path traversal is not allowed: %q",
			section, index, sc.Run,
		)
	}

	// Verify script file exists
	fullPath := filepath.Join(projectPath, sc.Run)
	if _, err := os.Stat(fullPath); err != nil {
		return fmt.Errorf(
			"invalid script configuration in %s[%d]:\n  Script file not found: %s\n  Resolved path: %s",
			section, index, sc.Run, fullPath,
		)
	}

	// Validate kind
	if sc.Kind != "" && sc.Kind != "sh" && sc.Kind != "pwsh" {
		return fmt.Errorf(
			"invalid script configuration in %s[%d]: unsupported kind %q (expected 'sh' or 'pwsh')",
			section, index, sc.Kind,
		)
	}

	// If kind is still empty after normalization, we can't determine the script type
	if sc.Kind == "" {
		return fmt.Errorf(
			"invalid script configuration in %s[%d]: unable to determine script type for %q; "+
				"set 'kind' to 'sh' or 'pwsh', or use a .sh or .ps1 file extension",
			section, index, sc.Run,
		)
	}

	return nil
}

// applyPlatformOverride merges the platform-specific override into the base config.
func applyPlatformOverride(sc *ScriptConfig) {
	var override *ScriptConfig
	if runtime.GOOS == "windows" {
		override = sc.Windows
	} else {
		override = sc.Posix
	}

	if override == nil {
		return
	}

	if override.Run != "" {
		sc.Run = override.Run
	}
	if override.Kind != "" {
		sc.Kind = override.Kind
	}
	if override.Shell != "" {
		sc.Shell = override.Shell
	}
	if override.Name != "" {
		sc.Name = override.Name
	}
	if override.Env != nil {
		if sc.Env == nil {
			sc.Env = make(map[string]string)
		}
		maps.Copy(sc.Env, override.Env)
	}
	if override.Secrets != nil {
		if sc.Secrets == nil {
			sc.Secrets = make(map[string]string)
		}
		maps.Copy(sc.Secrets, override.Secrets)
	}
	sc.ContinueOnError = override.ContinueOnError
}

// normalizeKind resolves the script kind from the Kind, Shell, or file extension.
func normalizeKind(sc *ScriptConfig) {
	// Kind takes precedence
	if sc.Kind != "" {
		return
	}

	// Shell is a deprecated alias for Kind
	if sc.Shell != "" {
		sc.Kind = sc.Shell
		sc.Shell = ""
		return
	}

	// Auto-detect from file extension
	ext := strings.ToLower(filepath.Ext(sc.Run))
	switch ext {
	case ".sh":
		sc.Kind = "sh"
	case ".ps1":
		sc.Kind = "pwsh"
	}
}
