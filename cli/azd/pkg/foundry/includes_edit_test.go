// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package foundry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readFile reads dir-relative or absolute path content as a string.
func readFile(t *testing.T, path string) string {
	t.Helper()
	// #nosec G304 -- tests read files they just wrote under a t.TempDir() root.
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}

// serviceEntryMap parses an azure.yaml document and returns services.<name> as a map, matching
// the already-parsed inline map a provider would feed to ResolveFileRefs.
func serviceEntryMap(t *testing.T, doc, name string) map[string]any {
	t.Helper()
	parsed := parseYAML(t, doc)
	services, ok := parsed["services"].(map[string]any)
	require.True(t, ok, "services mapping missing")
	entry, ok := services[name].(map[string]any)
	require.True(t, ok, "service %q missing", name)
	return entry
}

func assertSubstringOrder(t *testing.T, s, before, after string) {
	t.Helper()
	beforeIndex := strings.Index(s, before)
	afterIndex := strings.Index(s, after)
	require.NotEqual(t, -1, beforeIndex, "%q missing", before)
	require.NotEqual(t, -1, afterIndex, "%q missing", after)
	assert.Less(t, beforeIndex, afterIndex)
}

func TestYAMLDocument_RoundTripPreservesCommentsAndOrder(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "azure.yaml")
	src := `name: my-project # inline project name
# a standalone comment about services
services:
  research-agent:
    # the agent host
    host: azure.ai.agent
    $ref: ./agents/research-agent.yaml
`
	require.NoError(t, os.WriteFile(path, []byte(src), 0o600))

	doc, err := LoadYAMLDocument(path)
	require.NoError(t, err)
	out, err := doc.Bytes()
	require.NoError(t, err)
	first := string(out)

	// Comments survive the round trip.
	assert.Contains(t, first, "# inline project name")
	assert.Contains(t, first, "# a standalone comment about services")
	assert.Contains(t, first, "# the agent host")
	// Key order within the entry is preserved (host before $ref).
	assertSubstringOrder(t, first, "host:", "$ref:")

	// Re-encoding is stable (idempotent).
	doc2, err := ParseYAMLDocument(path, out)
	require.NoError(t, err)
	out2, err := doc2.Bytes()
	require.NoError(t, err)
	assert.Equal(t, first, string(out2))
}

func TestYAMLDocument_ServiceEntry_FindMissingCreate(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "azure.yaml")
	src := `name: p
services:
  existing:
    host: azure.ai.agent
`
	require.NoError(t, os.WriteFile(path, []byte(src), 0o600))
	doc, err := LoadYAMLDocument(path)
	require.NoError(t, err)

	entry, err := doc.ServiceEntry("existing", false)
	require.NoError(t, err)
	require.NotNil(t, entry)
	assert.Equal(t, "azure.ai.agent", mappingValue(entry, "host").Value)

	_, err = doc.ServiceEntry("missing", false)
	require.ErrorIs(t, err, ErrServiceNotFound)

	created, err := doc.ServiceEntry("missing", true)
	require.NoError(t, err)
	require.NotNil(t, created)

	// The created entry is now findable and is the same node.
	again, err := doc.ServiceEntry("missing", false)
	require.NoError(t, err)
	assert.Same(t, created, again)
}

func TestYAMLDocument_ServiceEntry_CreatesServicesMapping(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "azure.yaml")
	require.NoError(t, os.WriteFile(path, []byte("name: p\n"), 0o600))

	doc, err := LoadYAMLDocument(path)
	require.NoError(t, err)
	entry, err := doc.ServiceEntry("svc", true)
	require.NoError(t, err)
	require.NotNil(t, entry)
	require.NoError(t, doc.Save())

	saved := readFile(t, path)
	assert.Contains(t, saved, "services:")
	assert.Contains(t, saved, "svc:")
}

func TestEntryRef(t *testing.T) {
	doc, err := ParseYAMLDocument("azure.yaml", []byte(`
services:
  ref-svc:
    host: azure.ai.agent
    $ref: ./agents/a.yaml
  inline-svc:
    host: azure.ai.agent
    kind: prompt
`))
	require.NoError(t, err)

	refEntry, err := doc.ServiceEntry("ref-svc", false)
	require.NoError(t, err)
	target, isRef := EntryRef(refEntry)
	assert.True(t, isRef)
	assert.Equal(t, "./agents/a.yaml", target)

	inlineEntry, err := doc.ServiceEntry("inline-svc", false)
	require.NoError(t, err)
	_, isRef = EntryRef(inlineEntry)
	assert.False(t, isRef)
}

// TestEntryRef_EdgeCases documents EntryRef's narrow detection behavior: empty or whitespace-only
// $ref values fall back to not-a-ref so the write helper treats the entry as inline, while scalar
// values such as 123 are reported as refs and fail later if the referenced file is invalid.
func TestEntryRef_EdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantRef bool
		wantVal string
	}{
		{
			name: "empty $ref falls back to not-a-ref",
			yaml: "services:\n  svc:\n    $ref: \"\"\n",
		},
		{
			name: "whitespace-only $ref falls back to not-a-ref",
			yaml: "services:\n  svc:\n    $ref: \"   \"\n",
		},
		{
			name: "no $ref key at all",
			yaml: "services:\n  svc:\n    kind: prompt\n",
		},
		{
			name:    "numeric $ref value parses as scalar string - treated as ref",
			yaml:    "services:\n  svc:\n    $ref: 123\n",
			wantRef: true,
			wantVal: "123",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := ParseYAMLDocument("azure.yaml", []byte(tc.yaml))
			require.NoError(t, err)
			entry, err := doc.ServiceEntry("svc", false)
			require.NoError(t, err)
			got, isRef := EntryRef(entry)
			assert.Equal(t, tc.wantRef, isRef)
			if tc.wantRef {
				assert.Equal(t, tc.wantVal, got)
			}
		})
	}
}

// TestEntryRef_NilEntry documents that EntryRef is nil-safe.
func TestEntryRef_NilEntry(t *testing.T) {
	_, isRef := EntryRef(nil)
	assert.False(t, isRef)
}

func TestYAMLDocument_SetServiceField_InlineOverlayAndAgreement(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "agents/research-agent.yaml", `
kind: hosted
project: ../src/research-agent
`)
	path := filepath.Join(root, "azure.yaml")
	src := `services:
  research-agent:
    host: azure.ai.agent
    $ref: ./agents/research-agent.yaml
`
	require.NoError(t, os.WriteFile(path, []byte(src), 0o600))

	doc, err := LoadYAMLDocument(path)
	require.NoError(t, err)
	require.NoError(t, doc.SetServiceField("research-agent", "kind", "prompt", EditInline))
	require.NoError(t, doc.Save())

	saved := readFile(t, path)
	// $ref is preserved and the overlay key is added beside it.
	assert.Contains(t, saved, "$ref: ./agents/research-agent.yaml")
	assert.Contains(t, saved, "kind: prompt")

	// Reads and writes agree: the resolver overlays the inline key over the loaded file, so the
	// inline value wins while the file's other (rebased) fields come through.
	resolved, err := ResolveFileRefs(serviceEntryMap(t, saved, "research-agent"), root)
	require.NoError(t, err)
	assert.Equal(t, "prompt", resolved["kind"])
	assert.Equal(t, "src/research-agent", resolved["project"])
}

func TestYAMLDocument_SetServiceField_InlineUpdateInPlace(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "azure.yaml")
	src := `services:
  agent-project:
    host: azure.ai.project
    endpoint: https://old.example.com # current endpoint
`
	require.NoError(t, os.WriteFile(path, []byte(src), 0o600))

	doc, err := LoadYAMLDocument(path)
	require.NoError(t, err)
	require.NoError(t, doc.SetServiceField("agent-project", "endpoint", "https://new.example.com", EditInline))
	require.NoError(t, doc.Save())

	saved := readFile(t, path)
	assert.Contains(t, saved, "https://new.example.com")
	assert.NotContains(t, saved, "https://old.example.com")
	// The replaced value's inline comment is preserved.
	assert.Contains(t, saved, "# current endpoint")
	// Existing key order is preserved (host before endpoint).
	assertSubstringOrder(t, saved, "host:", "endpoint:")
}

func TestYAMLDocument_SetServiceField_EditRefFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "agents/research-agent.yaml", `
kind: hosted
project: ../src/research-agent
`)
	path := filepath.Join(root, "azure.yaml")
	src := `services:
  research-agent:
    host: azure.ai.agent
    $ref: ./agents/research-agent.yaml
`
	require.NoError(t, os.WriteFile(path, []byte(src), 0o600))

	doc, err := LoadYAMLDocument(path)
	require.NoError(t, err)
	require.NoError(t, doc.SetServiceField("research-agent", "kind", "prompt", EditRefFile))
	require.NoError(t, doc.Save())

	// The azure.yaml entry is untouched: still just host + $ref.
	savedMain := readFile(t, path)
	assert.Contains(t, savedMain, "$ref: ./agents/research-agent.yaml")
	assert.NotContains(t, savedMain, "kind:")

	// The split file received the new value.
	savedRef := readFile(t, filepath.Join(root, "agents", "research-agent.yaml"))
	assert.Contains(t, savedRef, "kind: prompt")
	assert.NotContains(t, savedRef, "hosted")

	// The resolver now reads the new value from the file.
	resolved, err := ResolveFileRefs(serviceEntryMap(t, savedMain, "research-agent"), root)
	require.NoError(t, err)
	assert.Equal(t, "prompt", resolved["kind"])
}

func TestYAMLDocument_SetServiceField_EditRefFileFallsBackInline(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "azure.yaml")
	src := `services:
  agent-project:
    host: azure.ai.project
`
	require.NoError(t, os.WriteFile(path, []byte(src), 0o600))

	doc, err := LoadYAMLDocument(path)
	require.NoError(t, err)
	// The entry has no $ref, so EditRefFile falls back to an inline write.
	require.NoError(t, doc.SetServiceField("agent-project", "endpoint", "https://e.example.com", EditRefFile))
	require.NoError(t, doc.Save())

	assert.Contains(t, readFile(t, path), "endpoint: https://e.example.com")
}

func TestYAMLDocument_SetServiceField_EditRefFileMissing(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "azure.yaml")
	src := `services:
  research-agent:
    host: azure.ai.agent
    $ref: ./agents/missing.yaml
`
	require.NoError(t, os.WriteFile(path, []byte(src), 0o600))

	doc, err := LoadYAMLDocument(path)
	require.NoError(t, err)
	err = doc.SetServiceField("research-agent", "kind", "prompt", EditRefFile)
	requireFileRefError(t, err, "cannot read")
}
