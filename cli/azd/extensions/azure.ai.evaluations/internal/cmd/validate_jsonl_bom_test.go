// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestValidateJSONLAcceptsAFileWindowsWrote is the Windows reader's path
// through the quick start.
//
// `... | Out-File rows.jsonl` and `Set-Content` both put a byte order mark in
// front of the first row. The upload path drops it, so `dataset create`
// accepted such a file -- and then `azd up` refused the very same bytes with
// "row 1 is not valid JSON", pointing at a row that is perfectly good. Two
// commands disagreeing about one file is the defect; this pins them together.
func TestValidateJSONLAcceptsAFileWindowsWrote(t *testing.T) {
	path := writeRows(t, "\uFEFF"+`{"query":"hi","response":"hello"}`+"\n")

	if err := validateJSONL(path); err != nil {
		t.Fatalf("a file PowerShell wrote was refused: %v", err)
	}
}

// TestValidateJSONLStillRefusesRealBreakage keeps the fix narrow. Only a
// leading mark is skipped; the same bytes in the middle of a file are content,
// and content that is not JSON still has to fail.
func TestValidateJSONLStillRefusesRealBreakage(t *testing.T) {
	cases := map[string]string{
		"a mark on a later row is not a mark": `{"a":1}` + "\n\uFEFF" + `{"b":2}` + "\n",
		"still catches a genuine typo":        `{"a":1}` + "\n" + `{"b":,}` + "\n",
		"still catches a bare scalar":         `"just a string"` + "\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if err := validateJSONL(writeRows(t, body)); err == nil {
				t.Error("the validator accepted a file it should have refused")
			}
		})
	}
}

// TestValidateJSONLNamesTheRowThatBroke matters because the mark shifts nothing:
// a reader told "row 2" has to find row 2, not row 1.
func TestValidateJSONLNamesTheRowThatBroke(t *testing.T) {
	path := writeRows(t, "\uFEFF"+`{"a":1}`+"\n"+`{"b":,}`+"\n")

	err := validateJSONL(path)
	if err == nil {
		t.Fatal("a broken row was accepted")
	}
	if !strings.Contains(err.Error(), "2") {
		t.Errorf("the error does not name row 2: %v", err)
	}
}

func writeRows(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rows.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}
	return path
}
