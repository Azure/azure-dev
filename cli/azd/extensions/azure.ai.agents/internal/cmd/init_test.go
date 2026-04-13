// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestIsRecoverableDeploymentSelectionError_StructuredReason(t *testing.T) {
	t.Parallel()

	st := status.New(codes.FailedPrecondition, "no valid SKUs for selected model")
	withDetails, err := st.WithDetails(&errdetails.ErrorInfo{
		Reason: azdext.AiErrorReasonNoValidSkus,
		Domain: azdext.AiErrorDomain,
	})
	if err != nil {
		t.Fatalf("failed to attach grpc error details: %v", err)
	}

	if !isRecoverableDeploymentSelectionError(withDetails.Err()) {
		t.Fatalf("expected structured AI reason to be recoverable")
	}
}

func TestIsRecoverableDeploymentSelectionError_NonRecoverableStructuredReason(t *testing.T) {
	t.Parallel()

	st := status.New(codes.InvalidArgument, "quota location is required")
	withDetails, err := st.WithDetails(&errdetails.ErrorInfo{
		Reason: azdext.AiErrorReasonQuotaLocation,
		Domain: azdext.AiErrorDomain,
	})
	if err != nil {
		t.Fatalf("failed to attach grpc error details: %v", err)
	}

	if isRecoverableDeploymentSelectionError(withDetails.Err()) {
		t.Fatalf("expected structured quota-location error to be non-recoverable")
	}
}

func TestIsRecoverableDeploymentSelectionError_UnstructuredError(t *testing.T) {
	t.Parallel()

	if isRecoverableDeploymentSelectionError(
		status.Error(codes.Internal, "no deployment found for model \"foo\" with the specified options"),
	) {
		t.Fatalf("expected unstructured error to be non-recoverable")
	}
}

func TestHasAiErrorReason(t *testing.T) {
	t.Parallel()

	st := status.New(codes.NotFound, "no locations with sufficient quota")
	withDetails, err := st.WithDetails(&errdetails.ErrorInfo{
		Reason: azdext.AiErrorReasonNoLocationsWithQuota,
		Domain: azdext.AiErrorDomain,
	})
	if err != nil {
		t.Fatalf("failed to attach grpc error details: %v", err)
	}

	if !hasAiErrorReason(withDetails.Err(), azdext.AiErrorReasonNoLocationsWithQuota) {
		t.Fatalf("expected reason to be detected")
	}
	if hasAiErrorReason(withDetails.Err(), azdext.AiErrorReasonNoValidSkus) {
		t.Fatalf("expected non-matching reason to be false")
	}
}

func TestCopyDirectory_RefusesToCopyIntoSubtree(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(src, "child")

	//nolint:gosec // test fixture directory permissions are intentional
	if err := os.MkdirAll(src, 0755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	//nolint:gosec // test fixture file permissions are intentional
	if err := os.WriteFile(filepath.Join(src, "file.txt"), []byte("hello"), 0644); err != nil {
		t.Fatalf("write src file: %v", err)
	}

	if err := copyDirectory(src, dst); err == nil {
		t.Fatalf("expected error when destination is inside source")
	}
}

func TestCopyDirectory_NoOpWhenSamePath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	//nolint:gosec // test fixture file permissions are intentional
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hello"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if err := copyDirectory(dir, dir); err != nil {
		t.Fatalf("expected no error when src==dst: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "file.txt")); err != nil {
		t.Fatalf("expected file to still exist: %v", err)
	}
}

func TestValidateLocalContainerAgentCopy_AllowsReinitInPlace(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	manifestPointer := filepath.Join(dir, "agent.yaml")
	//nolint:gosec // test fixture file permissions are intentional
	if err := os.WriteFile(manifestPointer, []byte("name: test"), 0644); err != nil {
		t.Fatalf("write agent.yaml: %v", err)
	}

	// InitAction with nil azdClient is safe here because isSamePath returns early
	// before any prompting code is reached.
	a := &InitAction{}
	if err := a.validateLocalContainerAgentCopy(context.Background(), manifestPointer, dir); err != nil {
		t.Fatalf("expected no error for re-init in place: %v", err)
	}
}

func TestIsSubpath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		child    string
		parent   string
		expected bool
	}{
		{"child inside parent", "/a/b/c", "/a/b", true},
		{"child equals parent", "/a/b", "/a/b", true},
		{"child outside parent", "/a/b", "/a/b/c", false},
		{"sibling directories", "/a/b", "/a/c", false},
		{"parent with trailing slash", "/a/b/c", "/a/b/", true},
		{"relative same", ".", ".", true},
		{"relative child", "a/b", "a", true},
		{"relative outside", "a", "a/b", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isSubpath(tt.child, tt.parent)
			if result != tt.expected {
				t.Errorf("isSubpath(%q, %q) = %v, want %v", tt.child, tt.parent, result, tt.expected)
			}
		})
	}
}

func TestIsSamePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		a        string
		b        string
		expected bool
	}{
		{"identical paths", "/a/b/c", "/a/b/c", true},
		{"trailing slash difference", "/a/b/c", "/a/b/c/", true},
		{"with dot segments", "/a/b/../b/c", "/a/b/c", true},
		{"different paths", "/a/b", "/a/c", false},
		{"relative same", "a/b", "a/b", true},
		{"relative with dots", "a/b/../b", "a/b", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isSamePath(tt.a, tt.b)
			if result != tt.expected {
				t.Errorf("isSamePath(%q, %q) = %v, want %v", tt.a, tt.b, result, tt.expected)
			}
		})
	}
}

type mockDirEntry struct {
	name  string
	isDir bool
}

func (m mockDirEntry) Name() string               { return m.name }
func (m mockDirEntry) IsDir() bool                { return m.isDir }
func (m mockDirEntry) Type() os.FileMode          { return 0 }
func (m mockDirEntry) Info() (os.FileInfo, error) { return nil, nil }

func TestFormatDirectoryPreview(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		entries    []os.DirEntry
		maxEntries int
		expected   string
	}{
		{
			name:       "empty entries",
			entries:    []os.DirEntry{},
			maxEntries: 5,
			expected:   "",
		},
		{
			name: "fewer than max",
			entries: []os.DirEntry{
				mockDirEntry{name: "file.txt", isDir: false},
				mockDirEntry{name: "dir", isDir: true},
			},
			maxEntries: 5,
			expected:   "dir/, file.txt",
		},
		{
			name: "exactly max",
			entries: []os.DirEntry{
				mockDirEntry{name: "a.txt", isDir: false},
				mockDirEntry{name: "b.txt", isDir: false},
			},
			maxEntries: 2,
			expected:   "a.txt, b.txt",
		},
		{
			name: "more than max",
			entries: []os.DirEntry{
				mockDirEntry{name: "c.txt", isDir: false},
				mockDirEntry{name: "a.txt", isDir: false},
				mockDirEntry{name: "b.txt", isDir: false},
				mockDirEntry{name: "d.txt", isDir: false},
			},
			maxEntries: 2,
			expected:   "a.txt, b.txt, ... (+2 more)",
		},
		{
			name: "directories get trailing slash",
			entries: []os.DirEntry{
				mockDirEntry{name: "mydir", isDir: true},
				mockDirEntry{name: "myfile", isDir: false},
			},
			maxEntries: 5,
			expected:   "mydir/, myfile",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := formatDirectoryPreview(tt.entries, tt.maxEntries)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result != tt.expected {
				t.Errorf("formatDirectoryPreview() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestParseGitHubUrlNaive(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		url      string
		expected *GitHubUrlInfo
	}{
		{
			name: "github.com blob URL",
			url:  "https://github.com/owner/repo/blob/main/path/to/file.yaml",
			expected: &GitHubUrlInfo{
				RepoSlug: "owner/repo",
				Branch:   "main",
				FilePath: "path/to/file.yaml",
				Hostname: "github.com",
			},
		},
		{
			name: "github.com blob URL with fragment",
			url:  "https://github.com/owner/repo/blob/main/path/to/file.yaml#L10",
			expected: &GitHubUrlInfo{
				RepoSlug: "owner/repo",
				Branch:   "main",
				FilePath: "path/to/file.yaml",
				Hostname: "github.com",
			},
		},
		{
			name: "github.com blob URL with query parameter",
			url:  "https://github.com/owner/repo/blob/main/path/to/file.yaml?plain=1",
			expected: &GitHubUrlInfo{
				RepoSlug: "owner/repo",
				Branch:   "main",
				FilePath: "path/to/file.yaml",
				Hostname: "github.com",
			},
		},
		{
			name: "github.com blob URL with both fragment and query",
			url:  "https://github.com/owner/repo/blob/develop/path/file.yaml?plain=1#L20-L30",
			expected: &GitHubUrlInfo{
				RepoSlug: "owner/repo",
				Branch:   "develop",
				FilePath: "path/file.yaml",
				Hostname: "github.com",
			},
		},
		{
			name: "raw.githubusercontent.com URL",
			url:  "https://raw.githubusercontent.com/owner/repo/refs/heads/main/path/to/file.yaml",
			expected: &GitHubUrlInfo{
				RepoSlug: "owner/repo",
				Branch:   "main",
				FilePath: "path/to/file.yaml",
				Hostname: "github.com",
			},
		},
		{
			name: "raw.githubusercontent.com URL with query parameter",
			url:  "https://raw.githubusercontent.com/owner/repo/refs/heads/main/path/to/file.yaml?token=abc123",
			expected: &GitHubUrlInfo{
				RepoSlug: "owner/repo",
				Branch:   "main",
				FilePath: "path/to/file.yaml",
				Hostname: "github.com",
			},
		},
		{
			name: "URL with branch containing slash (naive parsing treats first part as branch)",
			url:  "https://github.com/owner/repo/blob/feature/my-branch/file.yaml",
			// This is a known limitation - the naive parser will incorrectly treat "feature" as the branch
			// and "my-branch/file.yaml" as the file path. This is acceptable since the function is designed
			// to handle simple cases and fall back to full parsing for complex branch names.
			expected: &GitHubUrlInfo{
				RepoSlug: "owner/repo",
				Branch:   "feature",
				FilePath: "my-branch/file.yaml",
				Hostname: "github.com",
			},
		},
		{
			name:     "invalid URL",
			url:      "not a url",
			expected: nil,
		},
		{
			name:     "non-github URL",
			url:      "https://gitlab.com/owner/repo/blob/main/file.yaml",
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &InitAction{}
			result := a.parseGitHubUrlNaive(tt.url)

			if tt.expected == nil {
				if result != nil {
					t.Errorf("expected nil, got %+v", result)
				}
				return
			}

			if result == nil {
				t.Fatalf("expected non-nil result, got nil")
			}

			if result.RepoSlug != tt.expected.RepoSlug {
				t.Errorf("RepoSlug = %q, want %q", result.RepoSlug, tt.expected.RepoSlug)
			}
			if result.Branch != tt.expected.Branch {
				t.Errorf("Branch = %q, want %q", result.Branch, tt.expected.Branch)
			}
			if result.FilePath != tt.expected.FilePath {
				t.Errorf("FilePath = %q, want %q", result.FilePath, tt.expected.FilePath)
			}
			if result.Hostname != tt.expected.Hostname {
				t.Errorf("Hostname = %q, want %q", result.Hostname, tt.expected.Hostname)
			}
		})
	}
}

func TestCheckNotDirectory_ReturnsNilForFile(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "agent.yaml")
	//nolint:gosec // test fixture file permissions are intentional
	if err := os.WriteFile(file, []byte("name: test"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if err := checkNotDirectory(file); err != nil {
		t.Fatalf("expected nil for a regular file, got: %v", err)
	}
}

func TestCheckNotDirectory_ReturnsNilForNonexistentPath(t *testing.T) {
	t.Parallel()

	if err := checkNotDirectory(filepath.Join(t.TempDir(), "nope")); err != nil {
		t.Fatalf("expected nil for nonexistent path, got: %v", err)
	}
}

func TestCheckNotDirectory_ErrorForDirectoryWithManifest(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	manifest := filepath.Join(dir, "agent.manifest.yaml")
	// Must include a "template" key so looksLikeManifest recognises it as a manifest.
	content := "name: test\ntemplate:\n  kind: hosted\n"
	//nolint:gosec // test fixture file permissions are intentional
	if err := os.WriteFile(manifest, []byte(content), 0644); err != nil {
		t.Fatalf("write agent.manifest.yaml: %v", err)
	}

	err := checkNotDirectory(dir)
	if err == nil {
		t.Fatal("expected error for directory containing agent.manifest.yaml")
	}

	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok {
		t.Fatalf("expected *azdext.LocalError, got %T", err)
	}

	if localErr.Code != exterrors.CodeInvalidManifestPointer {
		t.Errorf("expected code %q, got %q", exterrors.CodeInvalidManifestPointer, localErr.Code)
	}

	if !strings.Contains(localErr.Message, "directory") {
		t.Errorf("message should mention 'directory', got: %s", localErr.Message)
	}

	if !strings.Contains(localErr.Suggestion, "-m") {
		t.Errorf("suggestion should include '-m' flag, got: %s", localErr.Suggestion)
	}

	if !strings.Contains(localErr.Suggestion, "agent.manifest.yaml") {
		t.Errorf("suggestion should include candidate path, got: %s", localErr.Suggestion)
	}
}

func TestCheckNotDirectory_NoSuggestionForAgentDefinition(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// An AgentDefinition has "kind" at root but no "template" ΓÇö should NOT
	// be suggested as a manifest file.
	defContent := "kind: hosted\nname: my-agent\n"
	//nolint:gosec // test fixture file permissions are intentional
	if err := os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(defContent), 0644); err != nil {
		t.Fatalf("write agent.yaml: %v", err)
	}

	err := checkNotDirectory(dir)
	if err == nil {
		t.Fatal("expected error for directory")
	}

	// The error should NOT suggest the agent.yaml since it's a definition, not a manifest.
	errMsg := err.Error()
	if strings.Contains(errMsg, "agent.yaml") {
		t.Errorf("should not suggest AgentDefinition file, got: %s", errMsg)
	}
}

func TestCheckNotDirectory_ErrorForDirectoryWithoutManifest(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	err := checkNotDirectory(dir)
	if err == nil {
		t.Fatal("expected error for empty directory")
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "directory") {
		t.Errorf("error should mention 'directory', got: %s", errMsg)
	}
}

func TestManifestHasModelResources(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		manifest *agent_yaml.AgentManifest
		expected bool
	}{
		{
			name: "prompt agent always has model resources",
			manifest: &agent_yaml.AgentManifest{
				Name:     "test-prompt",
				Template: agent_yaml.PromptAgent{},
			},
			expected: true,
		},
		{
			name: "hosted agent with model resource",
			manifest: &agent_yaml.AgentManifest{
				Name:     "test-hosted",
				Template: agent_yaml.ContainerAgent{},
				Resources: []any{
					agent_yaml.ModelResource{
						Resource: agent_yaml.Resource{
							Name: "my-model",
							Kind: agent_yaml.ResourceKindModel,
						},
						Id: "gpt-4o",
					},
				},
			},
			expected: true,
		},
		{
			name: "hosted agent with only tool resources",
			manifest: &agent_yaml.AgentManifest{
				Name:     "test-hosted-tools",
				Template: agent_yaml.ContainerAgent{},
				Resources: []any{
					agent_yaml.ToolResource{
						Resource: agent_yaml.Resource{
							Name: "my-tool",
							Kind: agent_yaml.ResourceKindTool,
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "hosted agent with no resources",
			manifest: &agent_yaml.AgentManifest{
				Name:     "test-hosted-empty",
				Template: agent_yaml.ContainerAgent{},
			},
			expected: false,
		},
		{
			name: "hosted agent with nil resources",
			manifest: &agent_yaml.AgentManifest{
				Name:      "test-hosted-nil",
				Template:  agent_yaml.ContainerAgent{},
				Resources: nil,
			},
			expected: false,
		},
		{
			name: "hosted agent with empty resources slice",
			manifest: &agent_yaml.AgentManifest{
				Name:      "test-hosted-empty-slice",
				Template:  agent_yaml.ContainerAgent{},
				Resources: []any{},
			},
			expected: false,
		},
		{
			name: "hosted agent with mixed model and tool resources",
			manifest: &agent_yaml.AgentManifest{
				Name:     "test-hosted-mixed",
				Template: agent_yaml.ContainerAgent{},
				Resources: []any{
					agent_yaml.ToolResource{
						Resource: agent_yaml.Resource{
							Name: "my-tool",
							Kind: agent_yaml.ResourceKindTool,
						},
					},
					agent_yaml.ModelResource{
						Resource: agent_yaml.Resource{
							Name: "my-model",
							Kind: agent_yaml.ResourceKindModel,
						},
						Id: "gpt-4o",
					},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := manifestHasModelResources(tt.manifest)
			if result != tt.expected {
				t.Errorf("manifestHasModelResources() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestResolvePositionalArg(t *testing.T) {
	t.Parallel()

	// Create a temp directory with a manifest file for testing
	tmpDir := t.TempDir()
	manifestPath := filepath.Join(tmpDir, "agent.yaml")
	if err := os.WriteFile(manifestPath, []byte("name: test\n"), 0600); err != nil {
		t.Fatalf("failed to create test manifest: %v", err)
	}

	tests := []struct {
		name       string
		arg        string
		isManifest bool
		isSrc      bool
	}{
		{
			name:       "https URL is manifest",
			arg:        "https://github.com/org/repo/blob/main/agent.yaml",
			isManifest: true,
		},
		{
			name:       "http URL is manifest",
			arg:        "http://example.com/agent.yaml",
			isManifest: true,
		},
		{
			name:       "azureml registry URL is manifest",
			arg:        "azureml://registries/myReg/agentmanifests/myManifest",
			isManifest: true,
		},
		{
			name:       "custom scheme URL is manifest",
			arg:        "custom://some/resource",
			isManifest: true,
		},
		{
			name:       "existing file is manifest",
			arg:        manifestPath,
			isManifest: true,
		},
		{
			name:  "existing directory is src",
			arg:   tmpDir,
			isSrc: true,
		},
		{
			name:       "non-existent yaml path is manifest",
			arg:        filepath.Join(tmpDir, "does-not-exist.yaml"),
			isManifest: true,
		},
		{
			name:       "non-existent yml path is manifest",
			arg:        filepath.Join(tmpDir, "does-not-exist.yml"),
			isManifest: true,
		},
		{
			name:  "non-existent path without extension is src",
			arg:   filepath.Join(tmpDir, "new-project-dir"),
			isSrc: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			isManifest, isSrc, err := resolvePositionalArg(tt.arg)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if isManifest != tt.isManifest {
				t.Errorf("isManifest = %v, want %v", isManifest, tt.isManifest)
			}
			if isSrc != tt.isSrc {
				t.Errorf("isSrc = %v, want %v", isSrc, tt.isSrc)
			}
		})
	}
}

func TestApplyPositionalArg_ConflictWithManifestFlag(t *testing.T) {
	t.Parallel()

	manifestPath := filepath.Join(t.TempDir(), "agent.yaml")
	if err := os.WriteFile(manifestPath, []byte("name: test\n"), 0600); err != nil {
		t.Fatalf("failed to create test manifest: %v", err)
	}

	flags := &initFlags{rootFlagsDefinition: &rootFlagsDefinition{}}
	cmd := &cobra.Command{}
	cmd.Flags().StringVarP(&flags.manifestPointer, "manifest", "m", "", "")
	// Simulate the user having set --manifest explicitly
	if err := cmd.Flags().Set("manifest", "other.yaml"); err != nil {
		t.Fatalf("failed to set flag: %v", err)
	}

	err := applyPositionalArg(manifestPath, flags, cmd)
	if err == nil {
		t.Fatal("expected error for conflicting positional arg and --manifest flag")
	}

	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok {
		t.Fatalf("expected *azdext.LocalError, got %T", err)
	}
	if localErr.Code != exterrors.CodeConflictingArguments {
		t.Errorf("code = %q, want %q", localErr.Code, exterrors.CodeConflictingArguments)
	}
	if !strings.Contains(localErr.Suggestion, "azd ai agent init") {
		t.Errorf("suggestion should include usage example, got: %s", localErr.Suggestion)
	}
}

func TestApplyPositionalArg_ConflictWithSrcFlag(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	flags := &initFlags{rootFlagsDefinition: &rootFlagsDefinition{}}
	cmd := &cobra.Command{}
	cmd.Flags().StringVarP(&flags.src, "src", "s", "", "")
	cmd.Flags().StringVarP(&flags.manifestPointer, "manifest", "m", "", "")
	// Simulate the user having set --src explicitly
	if err := cmd.Flags().Set("src", "other-dir"); err != nil {
		t.Fatalf("failed to set flag: %v", err)
	}

	err := applyPositionalArg(tmpDir, flags, cmd)
	if err == nil {
		t.Fatal("expected error for conflicting positional arg and --src flag")
	}

	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok {
		t.Fatalf("expected *azdext.LocalError, got %T", err)
	}
	if localErr.Code != exterrors.CodeConflictingArguments {
		t.Errorf("code = %q, want %q", localErr.Code, exterrors.CodeConflictingArguments)
	}
	if !strings.Contains(localErr.Suggestion, "azd ai agent init") {
		t.Errorf("suggestion should include usage example, got: %s", localErr.Suggestion)
	}
}

func TestApplyPositionalArg_SetsManifestPointer(t *testing.T) {
	t.Parallel()

	manifestPath := filepath.Join(t.TempDir(), "agent.yaml")
	if err := os.WriteFile(manifestPath, []byte("name: test\n"), 0600); err != nil {
		t.Fatalf("failed to create test manifest: %v", err)
	}

	flags := &initFlags{rootFlagsDefinition: &rootFlagsDefinition{}}
	cmd := &cobra.Command{}
	cmd.Flags().StringVarP(&flags.manifestPointer, "manifest", "m", "", "")
	cmd.Flags().StringVarP(&flags.src, "src", "s", "", "")

	if err := applyPositionalArg(manifestPath, flags, cmd); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if flags.manifestPointer != manifestPath {
		t.Errorf("manifestPointer = %q, want %q", flags.manifestPointer, manifestPath)
	}
}

func TestApplyPositionalArg_SetsSrcDir(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	flags := &initFlags{rootFlagsDefinition: &rootFlagsDefinition{}}
	cmd := &cobra.Command{}
	cmd.Flags().StringVarP(&flags.manifestPointer, "manifest", "m", "", "")
	cmd.Flags().StringVarP(&flags.src, "src", "s", "", "")

	if err := applyPositionalArg(tmpDir, flags, cmd); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if flags.src != tmpDir {
		t.Errorf("src = %q, want %q", flags.src, tmpDir)
	}
}

func TestApplyPositionalArg_NonExistentDirSetsSrc(t *testing.T) {
	t.Parallel()

	newDir := filepath.Join(t.TempDir(), "new-project")

	flags := &initFlags{rootFlagsDefinition: &rootFlagsDefinition{}}
	cmd := &cobra.Command{}
	cmd.Flags().StringVarP(&flags.manifestPointer, "manifest", "m", "", "")
	cmd.Flags().StringVarP(&flags.src, "src", "s", "", "")

	if err := applyPositionalArg(newDir, flags, cmd); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if flags.src != newDir {
		t.Errorf("src = %q, want %q", flags.src, newDir)
	}
}

func TestApplyPositionalArg_NonExistentYamlSetsManifest(t *testing.T) {
	t.Parallel()

	yamlPath := filepath.Join(t.TempDir(), "agent.yaml")

	flags := &initFlags{rootFlagsDefinition: &rootFlagsDefinition{}}
	cmd := &cobra.Command{}
	cmd.Flags().StringVarP(&flags.manifestPointer, "manifest", "m", "", "")
	cmd.Flags().StringVarP(&flags.src, "src", "s", "", "")

	if err := applyPositionalArg(yamlPath, flags, cmd); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if flags.manifestPointer != yamlPath {
		t.Errorf("manifestPointer = %q, want %q", flags.manifestPointer, yamlPath)
	}
}

// ---------------------------------------------------------------------------
// validateRenameInput (covers PR review ΓÇö input validation for user-provided
// rename names in resolveCollisions)
// ---------------------------------------------------------------------------

func TestValidateRenameInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		input      string
		wantDir    string
		wantSvc    string
		wantErr    bool
		errContain string
	}{
		{
			name:    "simple valid name",
			input:   "my-agent",
			wantDir: filepath.Join("src", "my-agent"),
			wantSvc: "my-agent",
		},
		{
			name:    "name with spaces produces valid svc",
			input:   "my agent",
			wantDir: filepath.Join("src", "my agent"),
			wantSvc: "myagent",
		},
		{
			name:       "path separator forward slash rejected",
			input:      "../escape",
			wantErr:    true,
			errContain: "path separators or dot segments",
		},
		{
			name:       "path separator backslash rejected",
			input:      `sub\dir`,
			wantErr:    true,
			errContain: "path separators or dot segments",
		},
		{
			name:       "single dot rejected",
			input:      ".",
			wantErr:    true,
			errContain: "path separators or dot segments",
		},
		{
			name:       "double dot rejected",
			input:      "..",
			wantErr:    true,
			errContain: "path separators or dot segments",
		},
		{
			name:       "absolute path rejected",
			input:      "/etc/passwd",
			wantErr:    true,
			errContain: "path separators or dot segments",
		},
		{
			name:       "empty name fails service validation",
			input:      "",
			wantErr:    true,
			errContain: "invalid service name",
		},
		{
			name:       "invalid characters fail service validation",
			input:      "agent@name!",
			wantErr:    true,
			errContain: "invalid service name",
		},
		{
			name:    "name with dots and hyphens is valid",
			input:   "agent.v2-beta",
			wantDir: filepath.Join("src", "agent.v2-beta"),
			wantSvc: "agent.v2-beta",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotDir, gotSvc, err := validateRenameInput(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tt.errContain != "" &&
					!strings.Contains(err.Error(), tt.errContain) {
					t.Errorf("error = %q, want containing %q",
						err.Error(), tt.errContain)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotDir != tt.wantDir {
				t.Errorf("dir = %q, want %q", gotDir, tt.wantDir)
			}
			if gotSvc != tt.wantSvc {
				t.Errorf("svc = %q, want %q", gotSvc, tt.wantSvc)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// buildCollisionMessage (covers PR review ΓÇö tailored collision messages)
// ---------------------------------------------------------------------------

func TestBuildCollisionMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		dirExists      bool
		serviceExists  bool
		targetDir      string
		serviceName    string
		wantContain    string
		wantNotContain string
	}{
		{
			name:          "both collisions mentions service and directory",
			dirExists:     true,
			serviceExists: true,
			targetDir:     "src/agent",
			serviceName:   "agent",
			wantContain:   "src/agent",
		},
		{
			name:          "service-only collision mentions azure.yaml",
			dirExists:     false,
			serviceExists: true,
			targetDir:     "src/agent",
			serviceName:   "agent",
			wantContain:   "azure.yaml",
		},
		{
			name:           "dir-only collision does not mention azure.yaml",
			dirExists:      true,
			serviceExists:  false,
			targetDir:      "src/agent",
			serviceName:    "agent",
			wantContain:    "src/agent",
			wantNotContain: "azure.yaml",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			msg := buildCollisionMessage(
				tt.dirExists, tt.serviceExists,
				tt.targetDir, tt.serviceName,
			)
			if !strings.Contains(msg, tt.wantContain) {
				t.Errorf("message = %q, want containing %q",
					msg, tt.wantContain)
			}
			if tt.wantNotContain != "" &&
				strings.Contains(msg, tt.wantNotContain) {
				t.Errorf("message = %q, should NOT contain %q",
					msg, tt.wantNotContain)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// nextAvailableName (covers PR review ΓÇö collision-resolution naming logic)
// ---------------------------------------------------------------------------

func TestNextAvailableName(t *testing.T) {
	tests := []struct {
		name          string
		agentId       string
		existingDirs  []string // dirs to create under src/
		existingSvcs  []string // service names in projectConfig
		wantCandidate string
		wantDir       string
		wantSvc       string
		wantErr       bool
	}{
		{
			name:          "no collisions picks -2",
			agentId:       "my-agent",
			wantCandidate: "my-agent-2",
			wantDir:       filepath.Join("src", "my-agent-2"),
			wantSvc:       "my-agent-2",
		},
		{
			name:          "dir collision skips to -3",
			agentId:       "my-agent",
			existingDirs:  []string{"my-agent-2"},
			wantCandidate: "my-agent-3",
			wantDir:       filepath.Join("src", "my-agent-3"),
			wantSvc:       "my-agent-3",
		},
		{
			name:          "service collision skips to -3",
			agentId:       "my-agent",
			existingSvcs:  []string{"my-agent-2"},
			wantCandidate: "my-agent-3",
			wantDir:       filepath.Join("src", "my-agent-3"),
			wantSvc:       "my-agent-3",
		},
		{
			name:          "both dir and svc collisions skip",
			agentId:       "my-agent",
			existingDirs:  []string{"my-agent-2"},
			existingSvcs:  []string{"my-agent-3"},
			wantCandidate: "my-agent-4",
			wantDir:       filepath.Join("src", "my-agent-4"),
			wantSvc:       "my-agent-4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			t.Chdir(tmpDir)

			for _, d := range tt.existingDirs {
				dirPath := filepath.Join("src", d)
				//nolint:gosec // test fixture directory permissions are intentional
				if err := os.MkdirAll(dirPath, 0o755); err != nil {
					t.Fatalf("setup: MkdirAll(%q): %v", dirPath, err)
				}
			}

			var projectCfg *azdext.ProjectConfig
			if len(tt.existingSvcs) > 0 {
				svcs := make(map[string]*azdext.ServiceConfig, len(tt.existingSvcs))
				for _, svcName := range tt.existingSvcs {
					svcs[svcName] = &azdext.ServiceConfig{Name: svcName}
				}
				projectCfg = &azdext.ProjectConfig{Services: svcs}
			}

			action := &InitAction{projectConfig: projectCfg}
			candidate, dir, svc, err := action.nextAvailableName(tt.agentId)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if candidate != tt.wantCandidate {
				t.Errorf("candidate = %q, want %q",
					candidate, tt.wantCandidate)
			}
			if dir != tt.wantDir {
				t.Errorf("dir = %q, want %q", dir, tt.wantDir)
			}
			if svc != tt.wantSvc {
				t.Errorf("svc = %q, want %q", svc, tt.wantSvc)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// resolveCollisions ΓÇö no collision / no-prompt paths
// (covers PR review ΓÇö collision resolution unit tests)
// ---------------------------------------------------------------------------

func TestResolveCollisions_NoCollision(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	action := &InitAction{
		flags: &initFlags{rootFlagsDefinition: &rootFlagsDefinition{}},
	}

	dir, svc, err := action.resolveCollisions(
		t.Context(), "agent",
		filepath.Join("src", "agent"), "agent",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dir != filepath.Join("src", "agent") {
		t.Errorf("dir = %q, want %q",
			dir, filepath.Join("src", "agent"))
	}
	if svc != "agent" {
		t.Errorf("svc = %q, want %q", svc, "agent")
	}
}

func TestResolveCollisions_NoPrompt(t *testing.T) {
	tests := []struct {
		name         string
		agentId      string
		existingDirs []string
		existingSvcs []string
		wantDir      string
		wantSvc      string
	}{
		{
			name:         "dir-only collision auto-suffixes",
			agentId:      "agent",
			existingDirs: []string{"agent"},
			wantDir:      filepath.Join("src", "agent-2"),
			wantSvc:      "agent-2",
		},
		{
			name:         "service-only collision auto-suffixes",
			agentId:      "agent",
			existingSvcs: []string{"agent"},
			wantDir:      filepath.Join("src", "agent-2"),
			wantSvc:      "agent-2",
		},
		{
			name:         "both collisions auto-suffix",
			agentId:      "agent",
			existingDirs: []string{"agent"},
			existingSvcs: []string{"agent"},
			wantDir:      filepath.Join("src", "agent-2"),
			wantSvc:      "agent-2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			t.Chdir(tmpDir)

			for _, d := range tt.existingDirs {
				dirPath := filepath.Join("src", d)
				//nolint:gosec // test fixture directory permissions are intentional
				if err := os.MkdirAll(dirPath, 0o755); err != nil {
					t.Fatalf("setup: MkdirAll(%q): %v", dirPath, err)
				}
			}

			var projectCfg *azdext.ProjectConfig
			svcs := make(map[string]*azdext.ServiceConfig, len(tt.existingSvcs))
			for _, svcName := range tt.existingSvcs {
				svcs[svcName] = &azdext.ServiceConfig{Name: svcName}
			}
			if len(svcs) > 0 {
				projectCfg = &azdext.ProjectConfig{Services: svcs}
			}

			action := &InitAction{
				projectConfig: projectCfg,
				flags: &initFlags{
					rootFlagsDefinition: &rootFlagsDefinition{
						NoPrompt: true,
					},
				},
			}

			targetDir := filepath.Join("src", tt.agentId)
			dir, svc, err := action.resolveCollisions(
				t.Context(), tt.agentId, targetDir, tt.agentId,
			)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if dir != tt.wantDir {
				t.Errorf("dir = %q, want %q", dir, tt.wantDir)
			}
			if svc != tt.wantSvc {
				t.Errorf("svc = %q, want %q", svc, tt.wantSvc)
			}
		})
	}
}

func TestEnsureLoggedIn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		output   []byte
		runErr   error
		wantErr  bool
		wantCode string
		wantMsg  string
	}{
		{
			name:    "authenticated returns nil",
			output:  []byte(`{"status":"authenticated","type":"user","email":"user@example.com"}`),
			wantErr: false,
		},
		{
			name:     "unauthenticated returns structured auth error",
			output:   []byte(`{"status":"unauthenticated"}`),
			wantErr:  true,
			wantCode: exterrors.CodeNotLoggedIn,
			wantMsg:  "not logged in",
		},
		{
			name:     "unauthenticated with non-zero exit still detected",
			output:   []byte(`{"status":"unauthenticated"}`),
			runErr:   errors.New("exit status 1"),
			wantErr:  true,
			wantCode: exterrors.CodeNotLoggedIn,
			wantMsg:  "not logged in",
		},
		{
			name:    "command failure with no output is skipped",
			output:  nil,
			runErr:  errors.New("exec: azd not found"),
			wantErr: false,
		},
		{
			name:    "malformed JSON is skipped",
			output:  []byte(`not-json`),
			wantErr: false,
		},
		{
			name:    "empty status field is skipped",
			output:  []byte(`{"status":""}`),
			wantErr: false,
		},
		{
			name:    "missing status field is skipped",
			output:  []byte(`{"email":"user@example.com"}`),
			wantErr: false,
		},
		{
			name:    "unrecognised status value is skipped",
			output:  []byte(`{"status":"unknown-value"}`),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stub := func(_ context.Context) ([]byte, error) {
				return tt.output, tt.runErr
			}

			err := ensureLoggedIn(t.Context(), stub)

			if !tt.wantErr {
				if err != nil {
					t.Fatalf("expected nil error, got: %v", err)
				}
				return
			}

			if err == nil {
				t.Fatal("expected an error, got nil")
			}

			var localErr *azdext.LocalError
			if !errors.As(err, &localErr) {
				t.Fatalf("expected *azdext.LocalError, got %T: %v", err, err)
			}
			if localErr.Code != tt.wantCode {
				t.Errorf("code = %q, want %q", localErr.Code, tt.wantCode)
			}
			if tt.wantMsg != "" && !strings.Contains(localErr.Message, tt.wantMsg) {
				t.Errorf("message = %q, want it to contain %q", localErr.Message, tt.wantMsg)
			}
		})
	}
}

func TestEnsureLoggedIn_ContextCancelled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // cancel immediately

	stub := func(_ context.Context) ([]byte, error) {
		return nil, ctx.Err()
	}

	err := ensureLoggedIn(ctx, stub)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
}

func TestParseAuthStatusJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		data    []byte
		want    string
		wantErr bool
	}{
		{
			name: "authenticated",
			data: []byte(`{"status":"authenticated","type":"user","email":"a@b.com"}`),
			want: "authenticated",
		},
		{
			name: "unauthenticated",
			data: []byte(`{"status":"unauthenticated"}`),
			want: "unauthenticated",
		},
		{
			name:    "invalid JSON",
			data:    []byte(`not json`),
			wantErr: true,
		},
		{
			name:    "missing status",
			data:    []byte(`{"email":"a@b.com"}`),
			wantErr: true,
		},
		{
			name:    "empty status",
			data:    []byte(`{"status":""}`),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseAuthStatusJSON(tt.data)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
