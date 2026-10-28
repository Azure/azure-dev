// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestRootCommand(t *testing.T) {
	t.Parallel()

	root := NewRootCommand()
	if root.Name() != "search" {
		t.Fatalf("root name = %q, want search", root.Name())
	}
	for _, tt := range []struct {
		name   string
		hidden bool
	}{
		{name: "version"},
		{name: "metadata", hidden: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			command, _, err := root.Find([]string{tt.name})
			if err != nil {
				t.Fatalf("find %s: %v", tt.name, err)
			}
			if command.Name() != tt.name || command.Hidden != tt.hidden {
				t.Fatalf("command = %q, hidden = %t; want %q, hidden = %t",
					command.Name(), command.Hidden, tt.name, tt.hidden)
			}
		})
	}
}

func TestRootHelp(t *testing.T) {
	var output bytes.Buffer
	root := NewRootCommand()
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs([]string{"--help"})

	if err := root.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("execute help: %v", err)
	}
	help := output.String()
	for _, tt := range []struct {
		name    string
		visible bool
	}{
		{name: "version", visible: true},
		{name: "context"},
		{name: "prompt"},
		{name: "listen"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			visible := false
			for line := range strings.SplitSeq(help, "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), tt.name+" ") {
					visible = true
					break
				}
			}
			if visible != tt.visible {
				t.Errorf("help command %q visible = %t, want %t", tt.name, visible, tt.visible)
			}
		})
	}
}
