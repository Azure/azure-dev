// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"
)

// This file is the only place that knows how the configuration is stored: the
// directory it lives in, what the file is called, and how it is parsed and
// serialized. Everything else works with *EvalConfig, so changing the on-disk
// shape stays a local edit.

// DefaultEvalDir is where init writes the configuration and its artifacts.
const DefaultEvalDir = "evals"

// EvalConfigBase is the single configuration file inside that directory.
const EvalConfigBase = "eval.yaml"

// EvalConfigPath is the configuration file inside an eval directory. It is
// exported for error messages and for the azure.yaml $ref; readers should
// prefer OpenEvalConfig.
func EvalConfigPath(evalDir string) string {
	return filepath.Join(evalDir, EvalConfigBase)
}

// OpenEvalConfig reads the configuration under evalDir.
//
// A missing file returns (nil, nil): generate runs before init, so "no
// configuration yet" is an ordinary state rather than a failure.
func OpenEvalConfig(evalDir string) (*EvalConfig, error) {
	cfg, err := LoadEvalConfig(EvalConfigPath(evalDir))
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
		return nil, fmt.Errorf("reading eval config %q: %w", path, err)
	}

	var cfg EvalConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing eval config %q: %w", path, err)
	}
	return &cfg, nil
}

// SaveEvalConfig writes cfg as the configuration under evalDir, creating the
// directory when it does not exist yet.
func SaveEvalConfig(evalDir string, cfg *EvalConfig) error {
	if err := os.MkdirAll(evalDir, 0o750); err != nil {
		return fmt.Errorf("creating %q: %w", evalDir, err)
	}
	return SaveEvalConfigTo(EvalConfigPath(evalDir), cfg)
}

// SaveEvalConfigTo writes cfg over an explicit path, for callers that already
// resolved one.
func SaveEvalConfigTo(path string, cfg *EvalConfig) error {
	body, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("serializing eval config: %w", err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return fmt.Errorf("writing eval config %q: %w", path, err)
	}
	return nil
}
