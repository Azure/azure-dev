// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"azureaieval/internal/messages"

	"go.yaml.in/yaml/v3"
)

// This file is the only place that knows how the configuration is stored: the
// directory it lives in, what the file is called, and how it is parsed and
// serialized. Everything else works with *EvalConfig, so changing the on-disk
// shape stays a local edit.

// DefaultEvalDir is where init writes the configuration and its artifacts.
const DefaultEvalDir = "evals"

// EvalConfigBase is the single configuration file inside that directory.
//
// Prefixed for azd, the way azure.yaml is: eval.yaml is generic enough to
// collide with an unrelated tool's file in the same folder, and the prefix says
// whose it is.
const EvalConfigBase = "azure.eval.yaml"

// LegacyEvalConfigBase is what the file was called before it was named for azd.
// Read, never written: a project that already has one keeps working, and does
// not silently grow a second configuration beside it.
const LegacyEvalConfigBase = "eval.yaml"

// EvalConfigPath is the configuration file inside an eval directory. It is
// exported for error messages and for the azure.yaml $ref; readers should
// prefer OpenEvalConfig.
func EvalConfigPath(evalDir string) string {
	return filepath.Join(evalDir, EvalConfigBase)
}

// ResolveEvalConfigPath is the configuration this directory actually holds:
// the current name, or the legacy one when that is the only file there.
func ResolveEvalConfigPath(evalDir string) string {
	current := EvalConfigPath(evalDir)
	if _, err := os.Stat(current); err == nil {
		return current
	}
	legacy := filepath.Join(evalDir, LegacyEvalConfigBase)
	if _, err := os.Stat(legacy); err == nil {
		return legacy
	}
	return current
}

// OpenEvalConfig reads the configuration under evalDir.
//
// A missing file returns (nil, nil): generate runs before init, so "no
// configuration yet" is an ordinary state rather than a failure.
func OpenEvalConfig(evalDir string) (*EvalConfig, error) {
	cfg, err := LoadEvalConfig(ResolveEvalConfigPath(evalDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return cfg, err
}

// LoadEvalConfig reads a configuration from an explicit path. The path is used
// verbatim, relative to the process working directory — never re-rooted.
func LoadEvalConfig(path string) (*EvalConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, messages.ReadingEvalConfig(path, err)
	}

	var cfg EvalConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, messages.ParsingEvalConfig(path, err)
	}
	return &cfg, nil
}

// SaveEvalConfig writes cfg as the configuration under evalDir, creating the
// directory when it does not exist yet.
//
// Writes back over a legacy eval.yaml when that is the file the project has, so
// a generate into an existing project updates the configuration it already
// references rather than leaving an inert second one beside it.
func SaveEvalConfig(evalDir string, cfg *EvalConfig) error {
	if err := os.MkdirAll(evalDir, 0o750); err != nil {
		return messages.Creating(evalDir, err)
	}
	return SaveEvalConfigTo(ResolveEvalConfigPath(evalDir), cfg)
}

// SaveEvalConfigTo writes cfg over an explicit path, for callers that already
// resolved one.
func SaveEvalConfigTo(path string, cfg *EvalConfig) error {
	body, err := yaml.Marshal(cfg)
	if err != nil {
		return messages.SerializingEvalConfig(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return messages.WritingEvalConfig(path, err)
	}
	return nil
}
