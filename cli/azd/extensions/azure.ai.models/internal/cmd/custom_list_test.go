// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"
	"testing"
)

func TestListArgumentsBeforeAuthentication(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"Foundry default", nil, ""},
		{"Foundry filter", []string{"--source-job-id", "job"}, ""},
		{"Fireworks", []string{"--connection", "fireworks", "--account", "test-account", "-o", "json"}, ""},
		{"missing account", []string{"--connection", "fireworks"}, "must both be non-empty"},
		{"missing connection", []string{"--account", "test-account"}, "must both be non-empty"},
		{"empty connection", []string{"--connection=", "--account", "test-account"}, "must both be non-empty"},
		{"empty account", []string{"--connection", "fireworks", "--account="}, "must both be non-empty"},
		{"resource path", []string{"--connection", "fireworks", "--account", "accounts/test"}, "account ID"},
		{"traversal", []string{"--connection", "fireworks", "--account", ".."}, "account ID"},
		{"bad format", []string{"--output", "yaml"}, "unsupported output format"},
		{"ignored filter", []string{
			"--connection", "fireworks", "--account", "test-account", "--source-job-id=",
		}, "cannot be used with --connection"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newCustomListCommand(&customFlags{})
			if err := cmd.ParseFlags(tt.args); err != nil {
				t.Fatal(err)
			}
			err := cmd.Args(cmd, cmd.Flags().Args())
			if tt.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}
