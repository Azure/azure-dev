// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestDownloadArgumentsBeforeAuthentication(t *testing.T) {
	parent := t.TempDir()
	baseArgs := []string{
		"download", "--connection", "fireworks", "--account", "test-account",
		"--model", "test-model", "--output-dir", filepath.Join(parent, "model"),
	}
	tests := []struct {
		name    string
		extra   []string
		wantErr string
	}{
		{"valid", nil, ""},
		{"JSON summary", []string{"--output", "json"}, ""},
		{"empty connection", []string{"--connection="}, "--connection must be non-empty"},
		{"empty account", []string{"--account="}, "--account must be"},
		{"account resource path", []string{"--account", "accounts/test"}, "--account must be"},
		{"empty model", []string{"--model="}, "--model must be"},
		{"model resource path", []string{"--model", "accounts/test/models/model"}, "--model must be"},
		{"model traversal", []string{"--model", ".."}, "--model must be"},
		{"empty directory", []string{"--output-dir="}, "--output-dir must be non-empty"},
		{"existing directory", []string{"--output-dir", parent}, "already exists"},
		{"missing parent", []string{"--output-dir", filepath.Join(parent, "missing", "model")}, "must already exist"},
		{"unsupported format", []string{"--output", "yaml"}, "unsupported output format"},
		{"unexpected positional", []string{"extra"}, "unknown command"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := NewRootCommand()
			command, _, err := root.Find([]string{"download"})
			if err != nil {
				t.Fatal(err)
			}
			reachedAuthentication := false
			stop := errors.New("stop before authentication")
			command.PreRunE = func(*cobra.Command, []string) error {
				reachedAuthentication = true
				return stop
			}
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			root.SetArgs(append(slices.Clone(baseArgs), tt.extra...))
			err = root.ExecuteContext(t.Context())
			if tt.wantErr == "" {
				if !errors.Is(err, stop) || !reachedAuthentication {
					t.Fatalf("valid download did not reach the pre-authentication boundary: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.wantErr) || reachedAuthentication {
				t.Fatalf("error = %v, reached authentication = %v, want %q", err, reachedAuthentication, tt.wantErr)
			}
		})
	}
}
