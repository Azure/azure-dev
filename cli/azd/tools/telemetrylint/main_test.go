// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsDocumented(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		value   string
		want    bool
	}{
		{
			name:    "exact value",
			content: "| `demo.event` |",
			value:   "demo.event",
			want:    true,
		},
		{
			name:    "dynamic command event",
			content: "| `cmd.` |",
			value:   "cmd.provision",
			want:    true,
		},
		{
			name:    "unrelated prefix",
			content: "| `cmd.` |",
			value:   "other.event",
			want:    false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := isDocumented(test.content, test.value); got != test.want {
				t.Fatalf("isDocumented() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestCheckDefinitions(t *testing.T) {
	t.Parallel()

	definitions := []definition{
		{kind: "event", value: "demo.event", source: "events.go", line: 10},
		{kind: "field", value: "demo.field", source: "fields.go", line: 20},
	}
	documents := []document{
		{path: "reference.md", content: "`demo.event`"},
		{path: "schema.md", content: "`demo.field`"},
	}

	issues := checkDefinitions(definitions, documents)
	if len(issues) != 2 {
		t.Fatalf("checkDefinitions() returned %d issues, want 2", len(issues))
	}
	if issues[0].doc != "schema.md" || issues[1].doc != "reference.md" {
		t.Fatalf("unexpected issues: %#v", issues)
	}
}

func TestParseEvents(t *testing.T) {
	t.Parallel()

	path := writeTestFile(t, "events.go", `package events

const (
	CommandEventPrefix = "cmd."
	ExampleEvent = "demo.event"
)
`)

	definitions, err := parseEvents(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(definitions) != 2 {
		t.Fatalf("parseEvents() returned %d definitions, want 2", len(definitions))
	}
	if definitions[0].value != "cmd." ||
		definitions[1].value != "demo.event" {
		t.Fatalf("unexpected definitions: %#v", definitions)
	}
}

func TestParseFields(t *testing.T) {
	t.Parallel()

	path := writeTestFile(t, "fields.go", `package fields

var (
	ServiceNameKey = AttributeKey{
		Key: semconv.ServiceNameKey, // service.name
	}
	MachineIDKey = AttributeKey{
		Key: attribute.Key("machine.id"),
	}
	ObjectIdKey = attribute.Key(contracts.UserAuthUserId)
)
`)

	definitions, err := parseFields(path)
	if err != nil {
		t.Fatal(err)
	}
	values := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		values = append(values, definition.value)
	}
	want := []string{"service.name", "machine.id", "user_AuthenticatedId"}
	if strings.Join(values, ",") != strings.Join(want, ",") {
		t.Fatalf("parseFields() = %v, want %v", values, want)
	}
}

func TestParseExtensionUsages(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	extension := filepath.Join(root, "demo")
	source := filepath.Join(extension, "telemetry.go")
	if err := os.MkdirAll(extension, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte(`package demo

var request = &azdext.ReportUsageRequest{
	EventName: "demo.event",
	Attributes: map[string]string{
		"demo.mode": "safe",
	},
}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	usages, err := parseExtensionUsages(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(usages) != 1 || len(usages[0].definitions) != 2 {
		t.Fatalf("unexpected usages: %#v", usages)
	}
	if usages[0].definitions[0].value != "demo.event" ||
		usages[0].definitions[1].value != "demo.mode" {
		t.Fatalf("unexpected definitions: %#v", usages[0].definitions)
	}
}

func TestLintRepository(t *testing.T) {
	repoRoot, err := findRepoRoot(".")
	if err != nil {
		t.Fatal(err)
	}

	issues, err := lintRepository(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Fatalf("lintRepository() found issues: %#v", issues)
	}
}

func writeTestFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
