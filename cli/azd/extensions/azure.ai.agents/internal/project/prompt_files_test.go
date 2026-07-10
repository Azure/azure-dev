// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"
)

// fakeVectorStoreBuilder records the files it was asked to build and returns a
// fixed store id, so node behavior can be asserted without a live endpoint.
type fakeVectorStoreBuilder struct {
	storeID   string
	calls     int
	lastFiles []fileEntry
	lastReuse string
}

func (b *fakeVectorStoreBuilder) EnsureVectorStore(
	_ context.Context, _ string, reuseStoreID string, files []fileEntry,
) (string, error) {
	b.calls++
	b.lastFiles = files
	b.lastReuse = reuseStoreID
	if b.storeID == "" {
		b.storeID = "vs-fake"
	}
	return b.storeID, nil
}

func writeFilesDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if files == nil {
		return dir
	}
	filesDir := filepath.Join(dir, "files")
	if err := os.MkdirAll(filesDir, 0o750); err != nil {
		t.Fatalf("mkdir files: %v", err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(filesDir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

func TestScanFilesDir_Empty(t *testing.T) {
	// Absent files/ folder.
	dir := writeFilesDir(t, nil)
	entries, err := scanFilesDir(dir)
	if err != nil {
		t.Fatalf("scanFilesDir: %v", err)
	}
	if entries != nil {
		t.Errorf("expected nil entries for missing files/, got %d", len(entries))
	}
}

func TestScanFilesDir_IgnoresDotfiles(t *testing.T) {
	dir := writeFilesDir(t, map[string]string{
		".DS_Store": "junk",
		"faq.md":    "content",
	})
	entries, err := scanFilesDir(dir)
	if err != nil {
		t.Fatalf("scanFilesDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "faq.md" {
		t.Fatalf("expected only faq.md, got %+v", entries)
	}
	if entries[0].Hash == "" {
		t.Error("expected a content hash")
	}
}

func TestScanFilesDir_SortedByName(t *testing.T) {
	dir := writeFilesDir(t, map[string]string{
		"b.md": "b",
		"a.md": "a",
		"c.md": "c",
	})
	entries, err := scanFilesDir(dir)
	if err != nil {
		t.Fatalf("scanFilesDir: %v", err)
	}
	got := []string{entries[0].Name, entries[1].Name, entries[2].Name}
	want := []string{"a.md", "b.md", "c.md"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sort: got %v, want %v", got, want)
		}
	}
}

func TestInjectFileSearchTool_AddsWhenAbsent(t *testing.T) {
	managed := &agent_yaml.ManagedAgent{}
	injectFileSearchTool(managed, "vs-1")

	if len(managed.Tools) != 1 {
		t.Fatalf("tools: got %d, want 1", len(managed.Tools))
	}
	tool := managed.Tools[0].(map[string]any)
	if tool["type"] != "file_search" {
		t.Errorf("type: got %v", tool["type"])
	}
	ids := toStringSlice(tool["vector_store_ids"])
	if len(ids) != 1 || ids[0] != "vs-1" {
		t.Errorf("vector_store_ids: got %v", ids)
	}
}

func TestInjectFileSearchTool_MergesExisting(t *testing.T) {
	managed := &agent_yaml.ManagedAgent{
		Tools: []any{
			map[string]any{
				"type":             "file_search",
				"vector_store_ids": []any{"vs-existing"},
			},
		},
	}
	injectFileSearchTool(managed, "vs-new")

	if len(managed.Tools) != 1 {
		t.Fatalf("tools: got %d, want 1 (merged, not duplicated)", len(managed.Tools))
	}
	tool := managed.Tools[0].(map[string]any)
	ids := toStringSlice(tool["vector_store_ids"])
	if len(ids) != 2 || ids[0] != "vs-existing" || ids[1] != "vs-new" {
		t.Errorf("merged ids: got %v, want [vs-existing vs-new]", ids)
	}
}

func TestInjectFileSearchTool_NoDuplicateID(t *testing.T) {
	managed := &agent_yaml.ManagedAgent{
		Tools: []any{
			map[string]any{
				"type":             "file_search",
				"vector_store_ids": []any{"vs-1"},
			},
		},
	}
	injectFileSearchTool(managed, "vs-1")

	tool := managed.Tools[0].(map[string]any)
	ids := toStringSlice(tool["vector_store_ids"])
	if len(ids) != 1 {
		t.Errorf("expected no duplicate, got %v", ids)
	}
}

func TestFileStoreNode_NoFilesNoNode(t *testing.T) {
	g := &promptGraph{managed: &agent_yaml.ManagedAgent{}, bindings: map[string]any{}}
	node := fileStoreNode(g, nil, func() (vectorStoreBuilder, error) { return nil, nil })
	if node != nil {
		t.Fatal("expected no node when there are no files")
	}
}

func TestFileStoreNode_InjectsFileSearch(t *testing.T) {
	managed := &agent_yaml.ManagedAgent{Model: "m", Instructions: "i"}
	managed.Name = "agent"
	g := &promptGraph{managed: managed, bindings: map[string]any{}}
	fake := &fakeVectorStoreBuilder{storeID: "vs-42"}

	files := []fileEntry{{Name: "faq.md", Hash: "h", Content: []byte("x")}}
	node := fileStoreNode(g, files, func() (vectorStoreBuilder, error) { return fake, nil })
	if node == nil {
		t.Fatal("expected a file_store node")
	}
	if err := node.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := node.Resolve(context.Background()); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if fake.calls != 1 {
		t.Errorf("builder calls: got %d, want 1", fake.calls)
	}
	if g.bindings[vectorStoreBindingKey] != "vs-42" {
		t.Errorf("binding: got %v", g.bindings[vectorStoreBindingKey])
	}
	if len(managed.Tools) != 1 {
		t.Fatalf("tools: got %d, want 1", len(managed.Tools))
	}
	tool := managed.Tools[0].(map[string]any)
	ids := toStringSlice(tool["vector_store_ids"])
	if len(ids) != 1 || ids[0] != "vs-42" {
		t.Errorf("vector_store_ids: got %v", ids)
	}
}

func TestFileStoreNode_ValidateRejectsEmptyFile(t *testing.T) {
	g := &promptGraph{managed: &agent_yaml.ManagedAgent{}, bindings: map[string]any{}}
	files := []fileEntry{{Name: "empty.md", Hash: "h", Content: []byte{}}}
	node := fileStoreNode(g, files, func() (vectorStoreBuilder, error) { return nil, nil })
	if node == nil {
		t.Fatal("expected a node")
	}
	if err := node.Validate(); err == nil {
		t.Error("expected validation error for empty file")
	}
}

func TestFoundryVectorStoreBuilder_DedupesByHash(t *testing.T) {
	b := &foundryVectorStoreBuilder{uploaded: map[string]string{"h1": "file-1"}}
	// Two entries with the same hash h1 should not trigger any upload; since
	// the client is nil, an upload attempt would panic — proving dedupe.
	files := []fileEntry{
		{Name: "a.md", Hash: "h1", Content: []byte("a")},
		{Name: "b.md", Hash: "h1", Content: []byte("a")},
	}
	storeID, err := b.EnsureVectorStore(context.Background(), "agent", "vs-existing", files)
	if err != nil {
		t.Fatalf("EnsureVectorStore: %v", err)
	}
	if storeID != "vs-existing" {
		t.Errorf("store id: got %q, want reused vs-existing", storeID)
	}
}
