// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/azure"
)

// promptFilesDirName is the conventional folder whose documents are uploaded to
// a vector store backing the agent's file_search tool.
const promptFilesDirName = "files"

// vectorStoreBindingKey is the graph binding under which the resolved vector
// store id is published for later nodes / observability.
const vectorStoreBindingKey = "vector_store_id"

// fileEntry is one document contributed to the vector store, with its content
// hash used for dedupe across re-deploys.
type fileEntry struct {
	Name    string // base file name
	Path    string // absolute path on disk
	Hash    string // sha256 of the content, hex-encoded
	Content []byte
}

// vectorStoreBuilder uploads files and (re)builds a vector store, returning the
// store id. Implementations are idempotent: unchanged files (matched by hash)
// are skipped and an existing store is reused/updated rather than recreated.
// The seam keeps the graph node unit-testable without a live endpoint.
type vectorStoreBuilder interface {
	EnsureVectorStore(
		ctx context.Context, name, reuseStoreID string, files []fileEntry,
	) (storeID string, err error)
}

// scanFilesDir returns the documents under <agentDir>/files, sorted by name.
// Dotfiles and subdirectories are ignored. A missing or empty folder returns
// (nil, nil) so the caller contributes no file_search tool.
func scanFilesDir(agentDir string) ([]fileEntry, error) {
	if strings.TrimSpace(agentDir) == "" {
		return nil, nil
	}
	dir := filepath.Join(agentDir, promptFilesDirName)

	f, err := os.Open(dir) //nolint:gosec // agentDir derives from the resolved agent.yaml path
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("opening files directory %q: %w", dir, err)
	}
	names, err := f.Readdirnames(-1)
	_ = f.Close()
	if err != nil {
		return nil, fmt.Errorf("reading files directory %q: %w", dir, err)
	}

	var entries []fileEntry
	for _, name := range names {
		if strings.HasPrefix(name, ".") {
			continue
		}
		full := filepath.Join(dir, name)
		info, statErr := os.Stat(full)
		if statErr != nil {
			return nil, fmt.Errorf("stat %q: %w", full, statErr)
		}
		if info.IsDir() {
			continue
		}
		content, readErr := os.ReadFile(full) //nolint:gosec // path derived from the agent's files/ folder
		if readErr != nil {
			return nil, fmt.Errorf("reading %q: %w", full, readErr)
		}
		sum := sha256.Sum256(content)
		entries = append(entries, fileEntry{
			Name:    name,
			Path:    full,
			Hash:    hex.EncodeToString(sum[:]),
			Content: content,
		})
	}

	slices.SortFunc(entries, func(a, b fileEntry) int {
		return strings.Compare(a.Name, b.Name)
	})
	return entries, nil
}

// injectFileSearchTool ensures the agent's tools include a file_search tool
// wired to storeID. If a file_search tool already exists, storeID is merged
// into its vector_store_ids (deduped) rather than adding a second tool. The
// managed definition is mutated in place.
func injectFileSearchTool(managed *agent_yaml.PromptAgent, storeID string) {
	if managed == nil || strings.TrimSpace(storeID) == "" {
		return
	}

	for i, raw := range managed.Tools {
		tool, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if fmt.Sprintf("%v", tool["type"]) != "file_search" {
			continue
		}
		ids := toStringSlice(tool["vector_store_ids"])
		if !slices.Contains(ids, storeID) {
			ids = append(ids, storeID)
		}
		tool["vector_store_ids"] = ids
		managed.Tools[i] = tool
		return
	}

	managed.Tools = append(managed.Tools, map[string]any{
		"type":             "file_search",
		"vector_store_ids": []string{storeID},
	})
}

// toStringSlice coerces a decoded YAML/JSON value into a []string, tolerating
// []any (as produced by the YAML decoder) and []string.
func toStringSlice(v any) []string {
	switch t := v.(type) {
	case []string:
		return slices.Clone(t)
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			out = append(out, fmt.Sprintf("%v", e))
		}
		return out
	default:
		return nil
	}
}

// fileStoreNode builds the file_store graph node for the given files. It uploads
// the documents (via the builder), publishes the resolved store id into the
// graph bindings, and injects/merges the file_search tool. Returns nil when
// there are no files (the caller then registers no node).
func fileStoreNode(
	g *promptGraph,
	files []fileEntry,
	newBuilder func() (vectorStoreBuilder, error),
) *promptNode {
	if len(files) == 0 {
		return nil
	}
	return &promptNode{
		Kind: nodeFileStore,
		ID:   promptFilesDirName,
		Validate: func() error {
			for _, f := range files {
				if len(f.Content) == 0 {
					return exterrors.Validation(
						exterrors.CodeInvalidAgentManifest,
						fmt.Sprintf("file %q in the files/ folder is empty", f.Name),
						"remove empty files or add content before deploying",
					)
				}
			}
			return nil
		},
		Resolve: func(ctx context.Context) error {
			builder, err := newBuilder()
			if err != nil {
				return err
			}
			reuse, _ := g.bindings[vectorStoreBindingKey].(string)
			storeID, err := builder.EnsureVectorStore(ctx, g.managed.Name, reuse, files)
			if err != nil {
				return err
			}
			g.bindings[vectorStoreBindingKey] = storeID
			injectFileSearchTool(g.managed, storeID)
			return nil
		},
	}
}

// foundryVectorStoreBuilder is the live vectorStoreBuilder backed by the
// Foundry Files + Vector Stores endpoints. It dedupes unchanged files by hash
// and reuses an existing store id when one is supplied.
type foundryVectorStoreBuilder struct {
	client *azure.FoundryFilesClient
	// uploaded maps content hash -> file id within this deploy, so a file that
	// appears more than once is uploaded only once.
	uploaded map[string]string
}

// EnsureVectorStore uploads any not-yet-uploaded files and creates a vector
// store from the resulting file ids. When reuseStoreID is set it is returned
// as-is after ensuring uploads (add-only update); otherwise a new store is
// created and its id returned.
func (b *foundryVectorStoreBuilder) EnsureVectorStore(
	ctx context.Context, name, reuseStoreID string, files []fileEntry,
) (string, error) {
	if b.uploaded == nil {
		b.uploaded = map[string]string{}
	}
	fileIDs := make([]string, 0, len(files))
	for _, f := range files {
		if id, ok := b.uploaded[f.Hash]; ok {
			fileIDs = append(fileIDs, id)
			continue
		}
		obj, err := b.client.UploadFile(ctx, f.Name, f.Content, "assistants")
		if err != nil {
			return "", fmt.Errorf("uploading %q: %w", f.Name, err)
		}
		b.uploaded[f.Hash] = obj.Id
		fileIDs = append(fileIDs, obj.Id)
	}

	if strings.TrimSpace(reuseStoreID) != "" {
		return reuseStoreID, nil
	}

	store, err := b.client.CreateVectorStore(ctx, name, fileIDs)
	if err != nil {
		return "", fmt.Errorf("creating vector store: %w", err)
	}
	return store.Id, nil
}

// newFoundryVectorStoreBuilder constructs the live builder from prompt settings.
// It requires a resolved project endpoint (data-plane) to reach the Files API.
func newFoundryVectorStoreBuilder(settings *PromptAgentSettings) (vectorStoreBuilder, error) {
	if settings == nil || strings.TrimSpace(settings.ProjectEndpoint) == "" {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			"a Foundry project endpoint is required to upload files for file_search",
			"run `azd up` to provision a Foundry project, or remove the files/ folder",
		)
	}
	return &foundryVectorStoreBuilder{
		client: azure.NewFoundryFilesClient(settings.ProjectEndpoint, promptCredential()),
	}, nil
}
