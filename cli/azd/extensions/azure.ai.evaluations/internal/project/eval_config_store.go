// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"bytes"
	"errors"
	"io"
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

// checkOneConfig refuses a directory holding both names.
//
// Preferring one silently is the dangerous answer: `azure.yaml` `$ref`s a
// single file by name, so the CLI would edit one configuration while `azd up`
// deployed the other, and nothing would say so.
func checkOneConfig(evalDir string) error {
	current := EvalConfigPath(evalDir)
	legacy := filepath.Join(evalDir, LegacyEvalConfigBase)
	if _, err := os.Stat(current); err != nil {
		return nil
	}
	if _, err := os.Stat(legacy); err != nil {
		return nil
	}
	return messages.AmbiguousEvalConfig(current, legacy)
}

// OpenEvalConfig reads the configuration under evalDir.
//
// A missing file returns (nil, nil): generate runs before init, so "no
// configuration yet" is an ordinary state rather than a failure.
func OpenEvalConfig(evalDir string) (*EvalConfig, error) {
	if err := checkOneConfig(evalDir); err != nil {
		return nil, err
	}
	cfg, err := LoadEvalConfig(ResolveEvalConfigPath(evalDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return cfg, err
}

// LoadEvalConfig reads a configuration from an explicit path. The path is used
// verbatim, relative to the process working directory — never re-rooted.
//
// Decoded strictly: a key this extension does not know is a typo, and reading
// it as nothing leaves a configuration that looks fine and fails later
// somewhere else. `agent:` written under `target:` instead of `type:`/`name:`
// used to produce an empty target and a run that complained about the target.
func LoadEvalConfig(path string) (*EvalConfig, error) {
	data, err := ReadFileNoBOM(path)
	if err != nil {
		return nil, messages.ReadingEvalConfig(path, err)
	}
	return DecodeEvalConfig(data, path)
}

// DecodeEvalConfig is the one strict decoder, so every route into a
// configuration reports a mistyped key the same way.
//
// `azd up` reads the configuration through the service entry rather than off
// disk, and that route used json.Unmarshal, which drops unknown keys in
// silence. The same typo was therefore named by `azd ai eval run` and ignored
// by `azd up`. The name is what the diagnostic points at.
func DecodeEvalConfig(data []byte, name string) (*EvalConfig, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)

	var cfg EvalConfig
	if err := decoder.Decode(&cfg); err != nil {
		// An empty file is a configuration with nothing in it, not a parse
		// failure: `generate` writes one before it has anything to record.
		if errors.Is(err, io.EOF) {
			return &cfg, nil
		}
		return nil, messages.ParsingEvalConfig(name, explainUnknownKeys(err))
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
	if err := checkOneConfig(evalDir); err != nil {
		return err
	}
	if err := os.MkdirAll(evalDir, 0o750); err != nil {
		return messages.Creating(evalDir, err)
	}
	return SaveEvalConfigTo(ResolveEvalConfigPath(evalDir), cfg)
}

// SaveEvalConfigTo writes cfg over an explicit path, for callers that already
// resolved one.
//
// The replacement is atomic because os.WriteFile truncates first, and this file
// is read by other processes. A reader landing inside that window sees zero
// bytes, and a zero-byte config parses as a valid empty one rather than as an
// error, so it would go on to write back a configuration with every eval
// missing. Renaming into place means a reader sees either the whole old file or
// the whole new one.
func SaveEvalConfigTo(path string, cfg *EvalConfig) error {
	body, err := yaml.Marshal(cfg)
	if err != nil {
		return messages.SerializingEvalConfig(err)
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".azd-eval-config-*")
	if err != nil {
		return messages.WritingEvalConfig(path, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return messages.WritingEvalConfig(path, err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return messages.WritingEvalConfig(path, err)
	}
	if err := tmp.Close(); err != nil {
		return messages.WritingEvalConfig(path, err)
	}
	// Windows will not rename onto an existing file.
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return messages.WritingEvalConfig(path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return messages.WritingEvalConfig(path, err)
	}
	return nil
}
