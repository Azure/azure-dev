// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveHarborTask(t *testing.T) {
	const harborToml = "[rle]\nname = \"e\"\nversion = \"1.0.0\"\n[task]\nformat = \"harbor\"\n[harness]\ntype = \"package\"\nname = \"opencode\"\n"
	cases := []struct {
		name    string
		toml    string
		flags   rolloutFlags
		wantErr string
		agent   string
	}{
		{"non-harbor manifest", "", rolloutFlags{}, "", ""},
		{"harbor without task", harborToml, rolloutFlags{}, "is required", ""},
		{"harbor with task json", harborToml, rolloutFlags{task: `{"name":"a"}`}, "", "opencode"},
		{"harbor with task file", harborToml, rolloutFlags{taskFile: "t.json"}, "", "opencode"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			if c.toml != "" {
				if err := os.WriteFile(filepath.Join(dir, "rle.toml"), []byte(c.toml), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			t.Chdir(dir)
			flags := c.flags
			action := &rolloutAction{cmd: newRolloutCommand(), flags: &flags}
			err := action.resolveHarborTask()
			if c.wantErr == "" {
				if err != nil || flags.harborAgent != c.agent {
					t.Fatalf("err=%v agent=%q", err, flags.harborAgent)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("got %v, want %q", err, c.wantErr)
			}
		})
	}
}

func TestParseRolloutHarborTask(t *testing.T) {
	task, err := parseRolloutHarborTask([]byte(`{"format":"harbor","name":" a ","image":"x"}`))
	if err != nil || task.Name != "a" {
		t.Fatalf("task=%v err=%v", task, err)
	}
	for _, raw := range []string{`{}`, `{"name":" "}`, `[1]`} {
		if _, err := parseRolloutHarborTask([]byte(raw)); err == nil {
			t.Fatalf("expected error for %s", raw)
		}
	}
}
