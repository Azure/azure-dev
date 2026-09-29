// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
)

// ConfigHelper provides typed, ergonomic access to azd configuration through
// the gRPC UserConfig and Environment services. It eliminates the boilerplate
// of raw gRPC calls and JSON marshaling that extension authors otherwise need.
//
// Configuration sources (in merge priority, lowest to highest):
//  1. User config (global azd config) — via UserConfigService
//  2. Environment config (per-env) — via EnvironmentService
//
// Usage:
//
//	ch := azdext.NewConfigHelper(client)
//	port, err := ch.GetUserString(ctx, "extensions.myext.port")
//	var cfg MyConfig
//	err = ch.GetUserJSON(ctx, "extensions.myext", &cfg)
type ConfigHelper struct {
	client *AzdClient
}

// NewConfigHelper creates a [ConfigHelper] for the given AZD client.
func NewConfigHelper(client *AzdClient) (*ConfigHelper, error) {
	if client == nil {
		return nil, errors.New("azdext.NewConfigHelper: client must not be nil")
	}

	return &ConfigHelper{client: client}, nil
}

// --- User Config (global) ---

// GetUserString retrieves a string value from the global user config at the
// given dot-separated path. Returns ("", false, nil) when the path does not
// exist, and ("", false, err) on gRPC errors.
func (ch *ConfigHelper) GetUserString(ctx context.Context, path string) (string, bool, error) {
	if err := validatePath(path); err != nil {
		return "", false, err
	}

	resp, err := ch.client.UserConfig().GetString(ctx, &GetUserConfigStringRequest{Path: path})
	if err != nil {
		return "", false, fmt.Errorf("azdext.ConfigHelper.GetUserString: gRPC call failed for path %q: %w", path, err)
	}

	return resp.GetValue(), resp.GetFound(), nil
}

// GetUserJSON retrieves a value from the global user config and unmarshals it
// into out. Returns (false, nil) when the path does not exist.
func (ch *ConfigHelper) GetUserJSON(ctx context.Context, path string, out any) (bool, error) {
	if err := validatePath(path); err != nil {
		return false, err
	}

	if out == nil {
		return false, errors.New("azdext.ConfigHelper.GetUserJSON: out must not be nil")
	}

	resp, err := ch.client.UserConfig().Get(ctx, &GetUserConfigRequest{Path: path})
	if err != nil {
		return false, fmt.Errorf("azdext.ConfigHelper.GetUserJSON: gRPC call failed for path %q: %w", path, err)
	}

	if !resp.GetFound() {
		return false, nil
	}

	data := resp.GetValue()
	if len(data) == 0 {
		return false, nil
	}

	if err := json.Unmarshal(data, out); err != nil {
		return true, &ConfigError{
			Path:   path,
			Reason: ConfigReasonInvalidFormat,
			Err:    fmt.Errorf("failed to unmarshal config at path %q: %w", path, err),
		}
	}

	return true, nil
}

// SetUserJSON marshals value as JSON and writes it to the global user config
// at the given path.
func (ch *ConfigHelper) SetUserJSON(ctx context.Context, path string, value any) error {
	if err := validatePath(path); err != nil {
		return err
	}

	if value == nil {
		return errors.New("azdext.ConfigHelper.SetUserJSON: value must not be nil")
	}

	data, err := json.Marshal(value)
	if err != nil {
		return &ConfigError{
			Path:   path,
			Reason: ConfigReasonInvalidFormat,
			Err:    fmt.Errorf("failed to marshal value for path %q: %w", path, err),
		}
	}

	_, err = ch.client.UserConfig().Set(ctx, &SetUserConfigRequest{
		Path:  path,
		Value: data,
	})
	if err != nil {
		return fmt.Errorf("azdext.ConfigHelper.SetUserJSON: gRPC call failed for path %q: %w", path, err)
	}

	return nil
}

// UnsetUser removes a value from the global user config.
func (ch *ConfigHelper) UnsetUser(ctx context.Context, path string) error {
	if err := validatePath(path); err != nil {
		return err
	}

	_, err := ch.client.UserConfig().Unset(ctx, &UnsetUserConfigRequest{Path: path})
	if err != nil {
		return fmt.Errorf("azdext.ConfigHelper.UnsetUser: gRPC call failed for path %q: %w", path, err)
	}

	return nil
}

// --- Environment Config (per-environment) ---

// GetEnvString retrieves a string config value from the current environment.
// Returns ("", false, nil) when the path does not exist.
func (ch *ConfigHelper) GetEnvString(ctx context.Context, path string) (string, bool, error) {
	if err := validatePath(path); err != nil {
		return "", false, err
	}

	resp, err := ch.client.Environment().GetConfigString(ctx, &GetConfigStringRequest{Path: path})
	if err != nil {
		return "", false, fmt.Errorf("azdext.ConfigHelper.GetEnvString: gRPC call failed for path %q: %w", path, err)
	}

	return resp.GetValue(), resp.GetFound(), nil
}

// GetEnvJSON retrieves a value from the current environment's config and
// unmarshals it into out. Returns (false, nil) when the path does not exist.
func (ch *ConfigHelper) GetEnvJSON(ctx context.Context, path string, out any) (bool, error) {
	if err := validatePath(path); err != nil {
		return false, err
	}

	if out == nil {
		return false, errors.New("azdext.ConfigHelper.GetEnvJSON: out must not be nil")
	}

	resp, err := ch.client.Environment().GetConfig(ctx, &GetConfigRequest{Path: path})
	if err != nil {
		return false, fmt.Errorf("azdext.ConfigHelper.GetEnvJSON: gRPC call failed for path %q: %w", path, err)
	}

	if !resp.GetFound() {
		return false, nil
	}

	data := resp.GetValue()
	if len(data) == 0 {
		return false, nil
	}

	if err := json.Unmarshal(data, out); err != nil {
		return true, &ConfigError{
			Path:   path,
			Reason: ConfigReasonInvalidFormat,
			Err:    fmt.Errorf("failed to unmarshal env config at path %q: %w", path, err),
		}
	}

	return true, nil
}

// SetEnvJSON marshals value as JSON and writes it to the current environment's config.
func (ch *ConfigHelper) SetEnvJSON(ctx context.Context, path string, value any) error {
	if err := validatePath(path); err != nil {
		return err
	}

	if value == nil {
		return errors.New("azdext.ConfigHelper.SetEnvJSON: value must not be nil")
	}

	data, err := json.Marshal(value)
	if err != nil {
		return &ConfigError{
			Path:   path,
			Reason: ConfigReasonInvalidFormat,
			Err:    fmt.Errorf("failed to marshal value for env config path %q: %w", path, err),
		}
	}

	_, err = ch.client.Environment().SetConfig(ctx, &SetConfigRequest{
		Path:  path,
		Value: data,
	})
	if err != nil {
		return fmt.Errorf("azdext.ConfigHelper.SetEnvJSON: gRPC call failed for path %q: %w", path, err)
	}

	return nil
}

// UnsetEnv removes a value from the current environment's config.
func (ch *ConfigHelper) UnsetEnv(ctx context.Context, path string) error {
	if err := validatePath(path); err != nil {
		return err
	}

	_, err := ch.client.Environment().UnsetConfig(ctx, &UnsetConfigRequest{Path: path})
	if err != nil {
		return fmt.Errorf("azdext.ConfigHelper.UnsetEnv: gRPC call failed for path %q: %w", path, err)
	}

	return nil
}

// --- Merge ---

// MergeJSON performs a shallow merge of override into base, returning a new map.
// Both inputs must be JSON-compatible maps (map[string]any). Keys in override
// take precedence over keys in base.
//
// This is NOT a deep merge — nested maps are replaced entirely by the override
// value. For predictable extension config behavior, keep config structures flat
// or use explicit path-based Set operations for nested values.
func MergeJSON(base, override map[string]any) map[string]any {
	merged := make(map[string]any, len(base)+len(override))

	maps.Copy(merged, base)

	maps.Copy(merged, override)

	return merged
}

// deepMergeMaxDepth is the maximum recursion depth for [DeepMergeJSON].
// This prevents stack overflow from deeply nested or adversarial JSON
// structures. 32 levels is far deeper than any legitimate config hierarchy.
const deepMergeMaxDepth = 32

// DeepMergeJSON performs a recursive merge of override into base.
// When both base and override have a map value for the same key, those maps
// are merged recursively. Otherwise the override value replaces the base value.
//
// Recursion is bounded to [deepMergeMaxDepth] levels to prevent stack overflow
// from deeply nested or adversarial inputs. Beyond the limit, the override
// value replaces the base value (merge degrades to shallow at that level).
func DeepMergeJSON(base, override map[string]any) map[string]any {
	return deepMergeJSON(base, override, 0)
}

func deepMergeJSON(base, override map[string]any, depth int) map[string]any {
	merged := make(map[string]any, len(base)+len(override))

	maps.Copy(merged, base)

	for k, v := range override {
		baseVal, exists := merged[k]
		if !exists {
			merged[k] = v
			continue
		}

		baseMap, baseIsMap := baseVal.(map[string]any)
		overMap, overIsMap := v.(map[string]any)

		if baseIsMap && overIsMap && depth < deepMergeMaxDepth {
			merged[k] = deepMergeJSON(baseMap, overMap, depth+1)
		} else {
			merged[k] = v
		}
	}

	return merged
}

// --- Validation ---

// ConfigValidator defines a function that validates a config value.
// It returns nil if valid, or an error describing the validation failure.
type ConfigValidator func(value any) error

// ValidateConfig unmarshals the raw JSON data and runs all supplied validators.
// Returns the first validation error encountered, wrapped in a [*ConfigError].
func ValidateConfig(path string, data []byte, validators ...ConfigValidator) error {
	if len(data) == 0 {
		return &ConfigError{
			Path:   path,
			Reason: ConfigReasonMissing,
			Err:    fmt.Errorf("config at path %q is empty", path),
		}
	}

	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return &ConfigError{
			Path:   path,
			Reason: ConfigReasonInvalidFormat,
			Err:    fmt.Errorf("config at path %q is not valid JSON: %w", path, err),
		}
	}

	for _, v := range validators {
		if err := v(value); err != nil {
			return &ConfigError{
				Path:   path,
				Reason: ConfigReasonValidationFailed,
				Err:    fmt.Errorf("config validation failed at path %q: %w", path, err),
			}
		}
	}

	return nil
}

// RequiredKeys returns a [ConfigValidator] that checks for the presence of
// the specified keys in a map value.
func RequiredKeys(keys ...string) ConfigValidator {
	return func(value any) error {
		m, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("expected object, got %T", value)
		}

		for _, key := range keys {
			if _, exists := m[key]; !exists {
				return fmt.Errorf("required key %q is missing", key)
			}
		}

		return nil
	}
}

// --- Error types ---

// ConfigReason classifies the cause of a [ConfigError].
type ConfigReason int

const (
	// ConfigReasonMissing indicates the config path does not exist or is empty.
	ConfigReasonMissing ConfigReason = iota

	// ConfigReasonInvalidFormat indicates the config value is not valid JSON
	// or cannot be unmarshaled into the target type.
	ConfigReasonInvalidFormat

	// ConfigReasonValidationFailed indicates a validator rejected the config value.
	ConfigReasonValidationFailed
)

// String returns a human-readable label.
func (r ConfigReason) String() string {
	switch r {
	case ConfigReasonMissing:
		return "missing"
	case ConfigReasonInvalidFormat:
		return "invalid_format"
	case ConfigReasonValidationFailed:
		return "validation_failed"
	default:
		return "unknown"
	}
}

// ConfigError is returned by [ConfigHelper] methods on domain-level failures.
type ConfigError struct {
	// Path is the config path that was being accessed.
	Path string

	// Reason classifies the failure.
	Reason ConfigReason

	// Err is the underlying error.
	Err error
}

func (e *ConfigError) Error() string {
	return fmt.Sprintf("azdext.ConfigHelper: %s (path=%s): %v", e.Reason, e.Path, e.Err)
}

func (e *ConfigError) Unwrap() error {
	return e.Err
}

// configPathRe validates config path segments. Paths must start with an alphanumeric
// character and may contain alphanumeric characters, dots, underscores, and hyphens.
// Note: Dotted path segments (e.g., ".hidden") are intentionally rejected because
// config paths are logical keys, not file system paths. Leading dots could collide
// with hidden-file conventions and cause confusion in serialized config formats.
var configPathRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// validatePath checks that a config path is non-empty.
func validatePath(path string) error {
	if path == "" {
		return errors.New("azdext.ConfigHelper: config path must not be empty")
	}
	if !configPathRe.MatchString(path) {
		return errors.New(
			"azdext.ConfigHelper: config path must start with alphanumeric and contain only [a-zA-Z0-9._-]",
		)
	}

	return nil
}
