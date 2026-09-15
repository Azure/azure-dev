// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Notepad, VS Code on Windows and PowerShell's Set-Content all write a BOM.
// Neither encoding/json nor yaml.v3 skips one, and what a developer sees is
// "invalid character 'ï' looking for beginning of value" — which names a
// character, not the cause. Hit for real while editing a rubric by hand.
func TestReadFileNoBOM_StripsTheWindowsByteOrderMark(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rubric.json")
	body := append([]byte{0xEF, 0xBB, 0xBF}, []byte(`{"dimensions":[]}`)...)
	require.NoError(t, os.WriteFile(path, body, 0o600))

	data, err := ReadFileNoBOM(path)

	require.NoError(t, err)
	assert.Equal(t, `{"dimensions":[]}`, string(data))
}

// A file without one is returned byte for byte.
func TestReadFileNoBOM_LeavesOrdinaryContentAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rubric.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"dimensions":[]}`), 0o600))

	data, err := ReadFileNoBOM(path)

	require.NoError(t, err)
	assert.Equal(t, `{"dimensions":[]}`, string(data))
}

// Only a leading mark is a BOM. The same bytes inside the content are content.
func TestReadFileNoBOM_OnlyStripsALeadingMark(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rubric.json")
	body := []byte("{\"note\":\"\uFEFF inside\"}")
	require.NoError(t, os.WriteFile(path, body, 0o600))

	data, err := ReadFileNoBOM(path)

	require.NoError(t, err)
	assert.Equal(t, string(body), string(data))
}

// A configuration saved by a Windows editor has to load, or every command that
// reads it fails at once.
func TestLoadEvalConfig_AcceptsAByteOrderMark(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, EvalConfigBase)
	body := append([]byte{0xEF, 0xBB, 0xBF},
		[]byte("datasets:\n  - name: d\n    file: ./d.jsonl\n")...)
	require.NoError(t, os.WriteFile(path, body, 0o600))

	cfg, err := LoadEvalConfig(path)

	require.NoError(t, err)
	require.Len(t, cfg.Datasets, 1)
	assert.Equal(t, "d", cfg.Datasets[0].Name)
}
