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
		name       string
		content    string
		definition definition
		want       bool
	}{
		{
			name:       "exact value",
			content:    "| `demo.event` |",
			definition: definition{kind: "event", value: "demo.event"},
			want:       true,
		},
		{
			name:       "dynamic command event",
			content:    "| `cmd.<command>` |",
			definition: definition{kind: "event", value: "cmd.provision"},
			want:       true,
		},
		{
			name:       "dynamic event family marker",
			content:    "| `mcp.` |",
			definition: definition{kind: "event", value: "mcp.new_tool"},
			want:       true,
		},
		{
			name:       "unrelated value does not document dynamic event",
			content:    "| `mcp.client.name` |",
			definition: definition{kind: "event", value: "mcp.new_tool"},
			want:       false,
		},
		{
			name:       "unrelated prefix",
			content:    "| `cmd.<command>` |",
			definition: definition{kind: "event", value: "other.event"},
			want:       false,
		},
		{
			name:       "substring collision",
			content:    "| `auth.cache_clear_failed` |",
			definition: definition{kind: "field", value: "auth.cache"},
			want:       false,
		},
		{
			name:       "plain text is not a documented value",
			content:    "The demo.event event is emitted.",
			definition: definition{kind: "event", value: "demo.event"},
			want:       false,
		},
		{
			name:       "prefix does not document a field",
			content:    "| `mcp.client.name` |",
			definition: definition{kind: "field", value: "mcp.tool.name"},
			want:       false,
		},
		{
			name:    "prefix does not document an extension event",
			content: "| `vsrpc.<method>` |",
			definition: definition{
				kind:  "extension event",
				value: "vsrpc.custom",
			},
			want: false,
		},
		{
			name:    "extension runtime field name",
			content: "| `ext.demo.mode` |",
			definition: definition{
				kind:  "extension field",
				value: "demo.mode",
			},
			want: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			doc := newDocument("telemetry.md", test.content)
			if got := isDocumented(doc, test.definition); got != test.want {
				t.Fatalf("isDocumented() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestCheckDefinitionsInEveryDocument(t *testing.T) {
	t.Parallel()

	definitions := []definition{
		{kind: "event", value: "demo.event", source: "events.go", line: 10},
		{kind: "field", value: "demo.field", source: "fields.go", line: 20},
	}
	documents := []document{
		newDocument("reference.md", "`demo.event`"),
		newDocument("schema.md", "`demo.field`"),
	}

	issues := checkDefinitionsInEveryDocument(definitions, documents)
	if len(issues) != 2 {
		t.Fatalf(
			"checkDefinitionsInEveryDocument() returned %d issues, want 2",
			len(issues),
		)
	}
	if issues[0].doc != "schema.md" || issues[1].doc != "reference.md" {
		t.Fatalf("unexpected issues: %#v", issues)
	}
}

func TestCheckDefinitionsInAnyDocument(t *testing.T) {
	t.Parallel()

	definitions := []definition{
		{kind: "extension event", value: "demo.event", source: "telemetry.go"},
		{kind: "extension field", value: "demo.field", source: "telemetry.go"},
		{kind: "extension field", value: "demo.missing", source: "telemetry.go"},
	}
	documents := []document{
		newDocument("README.md", "`demo.event`"),
		newDocument("telemetry.md", "`demo.field`"),
		newDocument("CONTRIBUTING.md", "Contribution guide."),
	}

	issues := checkDefinitionsInAnyDocument(
		definitions,
		documents,
		"README.md",
	)
	if len(issues) != 1 {
		t.Fatalf(
			"checkDefinitionsInAnyDocument() returned %d issues, want 1",
			len(issues),
		)
	}
	if issues[0].doc != "README.md" ||
		issues[0].def.value != "demo.missing" {
		t.Fatalf("unexpected issues: %#v", issues)
	}
}

func TestExtractDocumentedValues(t *testing.T) {
	t.Parallel()

	content := "plain demo.event `demo.event` and `demo.field`"
	values := extractDocumentedValues(content)
	if _, ok := values["demo.event"]; !ok {
		t.Fatal("extractDocumentedValues() did not find demo.event")
	}
	if _, ok := values["demo.field"]; !ok {
		t.Fatal("extractDocumentedValues() did not find demo.field")
	}
	if _, ok := values["plain demo.event"]; ok {
		t.Fatal("extractDocumentedValues() included plain text")
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

func TestParseExtensionTelemetryEvent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	extension := filepath.Join(root, "demo")
	if err := os.MkdirAll(extension, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(extension, "a_telemetry.go"),
		[]byte(`package demo

import foundryTelemetry "example.com/foundry/telemetry"

const (
	eventName = forwardEvent
)

func event() foundryTelemetry.Event {
	return foundryTelemetry.Event{
		Name:       eventName,
		Attributes: attributes,
	}
}
`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(extension, "z_constants.go"),
		[]byte(`package demo

const (
	forwardEvent = "demo.event"
	modeKey      = "demo.mode"
	repeatedKey = "demo.repeated"
	repeatedKeyAgain
)

var attributes = map[string]string{
	modeKey:          "safe",
	repeatedKeyAgain: "also-safe",
}
`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	usages, err := parseExtensionUsages(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(usages) != 1 {
		t.Fatalf("parseExtensionUsages() returned %d usages, want 1", len(usages))
	}

	values := make(map[string]bool)
	for _, definition := range usages[0].definitions {
		values[definition.kind+":"+definition.value] = true
	}
	if !values["extension event:demo.event"] ||
		!values["extension field:demo.mode"] ||
		!values["extension field:demo.repeated"] {
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
