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
	"runtime"
	"syscall"
	"time"

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
	// Straight over the destination, and never by unlinking it first. Windows
	// refuses a rename while a reader holds the destination open, so the
	// obvious fallback -- remove, then rename -- turns a collision into a
	// window where the config does not exist, and OpenEvalConfig reports a
	// missing file as "no configuration yet", which callers answer by writing a
	// fresh one. That is the same data loss this function exists to prevent.
	// Contention is measured in microseconds, so it is waited out instead.
	if err := renameOverContention(tmpName, path); err != nil {
		// The unlink this replaced was doing something else worth keeping:
		// os.Remove clears a read-only attribute and retries, so a config marked
		// read-only (a Perforce or TFVC checkout, `attrib +R`, some archive
		// extractions) could still be replaced. Windows fails a rename onto a
		// read-only destination with the same errno as one a reader holds open,
		// so the two cannot be told apart before the wait.
		if !clearReadOnly(path) {
			return messages.WritingEvalConfig(path, err)
		}
		if err := os.Rename(tmpName, path); err != nil {
			return messages.WritingEvalConfig(path, err)
		}
	}
	return nil
}

// clearReadOnly drops a read-only attribute, reporting whether it had one to
// drop. os.Chmod is what carries FILE_ATTRIBUTE_READONLY on Windows.
func clearReadOnly(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm()&0o200 != 0 {
		return false
	}
	return os.Chmod(path, info.Mode().Perm()|0o200) == nil
}

// The budgets are deliberately different. A replacement window is measured in
// microseconds, so neither needs to be generous -- and every millisecond here
// is also charged to a file that is genuinely unreadable, because Windows
// reports "someone has this open" and "you may not have this" as one errno.
const (
	renameRetryBudget = 500 * time.Millisecond
	readRetryBudget   = 250 * time.Millisecond
)

func renameOverContention(from, to string) error {
	deadline := time.Now().Add(renameRetryBudget)
	delay := time.Millisecond
	for {
		err := os.Rename(from, to)
		if err == nil || !isSharingContention(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(delay)
		if delay < 16*time.Millisecond {
			delay *= 2
		}
	}
}

// isSharingContention reports the errors Windows raises while another handle is
// open. It cannot be precise: renaming onto a destination a reader holds open
// and renaming onto one the caller may not touch both report ERROR_ACCESS_DENIED,
// so a genuine permission failure is waited on before it is reported. The
// budget is what keeps that wait short enough to be worth the trade.
func isSharingContention(err error) bool {
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return false
	}
	if runtime.GOOS != "windows" {
		return false
	}
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	// ERROR_ACCESS_DENIED and ERROR_SHARING_VIOLATION.
	return errno == 5 || errno == 32
}

// readFileOverContention reads a file that another process may be replacing.
func readFileOverContention(path string) ([]byte, error) {
	deadline := time.Now().Add(readRetryBudget)
	delay := time.Millisecond
	for {
		body, err := os.ReadFile(path)
		if err == nil || !isSharingContention(err) || time.Now().After(deadline) {
			return body, err
		}
		time.Sleep(delay)
		if delay < 16*time.Millisecond {
			delay *= 2
		}
	}
}
