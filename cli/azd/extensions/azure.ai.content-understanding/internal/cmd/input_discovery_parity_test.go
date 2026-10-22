// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"azure.ai.content-understanding/internal/analysis"
	"azure.ai.content-understanding/internal/cuclient"

	"github.com/stretchr/testify/require"
)

type discoveryFile struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Target  string `json:"target,omitempty"`
	Content string `json:"content,omitempty"`
	SHA256  string `json:"sha256,omitempty"`
	Size    int64  `json:"size,omitempty"`
}

type discoveryError struct {
	Kind       string `json:"kind"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	Hint       string `json:"hint"`
	StatusCode int    `json:"status_code"`
}

type discoveryInput struct {
	Path      string `json:"path"`
	URL       string `json:"url"`
	Reference string `json:"reference"`
	Relative  string `json:"relative"`
	SizeBytes *int64 `json:"size_bytes"`
}

type discoveryResult struct {
	Inputs          []discoveryInput `json:"inputs"`
	TotalBytes      int64            `json:"total_bytes"`
	ExtensionCounts map[string]int   `json:"extension_counts"`
}

type discoveryCall struct {
	Operation string `json:"operation"`
	Input     struct {
		Args   []string `json:"args"`
		Kwargs struct {
			Positional []string `json:"positional"`
			Files      []string `json:"files"`
			Sources    []string `json:"sources"`
			URLs       []string `json:"urls"`
			Pattern    *string  `json:"pattern"`
			Recursive  bool     `json:"recursive"`
		} `json:"kwargs"`
	} `json:"input"`
	Filesystem   []discoveryFile `json:"filesystem"`
	Result       json.RawMessage `json:"result"`
	ResultType   string          `json:"result_type"`
	Error        json.RawMessage `json:"error"`
	NativeResult json.RawMessage `json:"native_result"`
	NativeError  *discoveryError `json:"native_error"`
}

type discoveryCase struct {
	SourceCase string          `json:"source_case"`
	Calls      []discoveryCall `json:"calls"`
}

type discoveryOracle struct {
	SchemaVersion int    `json:"schema_version"`
	Role          string `json:"role"`
	Source        struct {
		Repository string            `json:"repository"`
		Commit     string            `json:"commit"`
		License    string            `json:"license"`
		Python     string            `json:"python"`
		Platform   string            `json:"platform"`
		Files      map[string]string `json:"files"`
		Functions  []string          `json:"functions"`
		CaseCount  int               `json:"case_count"`
	} `json:"source"`
	Boundaries []string        `json:"boundaries"`
	Cases      []discoveryCase `json:"cases"`
}

const discoveryGoTest = "internal/cmd/input_discovery_parity_test.go::TestInputDiscoveryParity"

func inputPlanningSubtest(source string) string {
	return sourceParitySubtest(source)
}

func sourceParitySubtest(source string) string {
	_, name, _ := strings.Cut(source, "::")
	function, _, _ := strings.Cut(name, "[")
	return function + "-" + sha256Text([]byte(source))[:16]
}

func loadDiscoveryOracle(t *testing.T) discoveryOracle {
	t.Helper()
	inventory := loadParityInventory(t)
	require.Equal(t, "testdata/parity/input_discovery.json", inventory.InputDiscovery.File)
	body, err := os.ReadFile("../../testdata/parity/input_discovery.json")
	require.NoError(t, err)
	require.Equal(t, inventory.InputDiscovery.SHA256, sha256Text(body))
	_, err = recordedJSON(body)
	require.NoError(t, err)
	var oracle discoveryOracle
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	require.NoError(t, decoder.Decode(&oracle))
	require.Equal(t, 1, oracle.SchemaVersion)
	require.Equal(t, "public-source-test-controls-not-service-recordings", oracle.Role)
	require.Equal(t, inventory.Source.Repository, oracle.Source.Repository)
	require.Equal(t, paritySourceCommit, oracle.Source.Commit)
	require.Equal(t, "MIT", oracle.Source.License)
	require.Equal(t, "Linux", oracle.Source.Platform)
	require.NotEmpty(t, oracle.Source.Python)
	require.Len(t, oracle.Source.Files, 5)
	for _, path := range []string{
		"packages/core/tests/test_input_planning.py",
		"packages/core/src/cu_cli_core/input_planning.py",
		"packages/core/src/cu_cli_core/contracts.py",
		"packages/core/src/cu_cli_core/errors.py",
		"packages/core/src/cu_cli_core/serialization.py",
	} {
		require.Regexp(t, "^[0-9a-f]{64}$", oracle.Source.Files[path])
	}
	require.Len(t, oracle.Source.Functions, 29)
	require.Len(t, oracle.Boundaries, 4)
	require.Equal(t, 58, oracle.Source.CaseCount)
	require.Len(t, oracle.Cases, oracle.Source.CaseCount)
	mapped := map[string]bool{}
	for _, row := range inventory.Cases {
		if slices.ContainsFunc(row.GoTests, func(reference string) bool {
			return strings.HasPrefix(reference, discoveryGoTest+"/")
		}) {
			require.Contains(t, []string{"evidenced", "approved"}, row.State)
			require.Equal(t, "difference", row.Disposition)
			require.NoError(t, parityState(row.State, row.Approval))
			require.Contains(t, row.GoTests, discoveryGoTest+"/"+inputPlanningSubtest(row.Source))
			mapped[row.Source] = true
		}
	}
	require.Len(t, mapped, oracle.Source.CaseCount)
	for _, row := range oracle.Cases {
		require.True(t, mapped[row.SourceCase], "missing/duplicate source mapping: %s", row.SourceCase)
		delete(mapped, row.SourceCase)
		name, found := strings.CutPrefix(row.SourceCase, "packages/core/tests/test_input_planning.py::")
		require.True(t, found)
		function, _, _ := strings.Cut(name, "[")
		require.Contains(t, oracle.Source.Functions, function)
		require.Len(t, row.Calls, 1)
		call := row.Calls[0]
		require.Contains(t, []string{"plan_inputs", "redact_input_reference"}, call.Operation)
		if call.NativeError != nil {
			require.NotEqual(t, "null", string(call.Error))
			require.Equal(t, "null", string(call.Result))
			require.Equal(t, "null", string(call.NativeResult))
			require.Empty(t, call.ResultType)
		} else {
			require.Equal(t, "null", string(call.Error))
			require.NotEqual(t, "null", string(call.Result))
			expectedType := "cu_cli_core.contracts.InputPlan"
			if call.Operation == "redact_input_reference" {
				expectedType = "builtins.str"
			}
			require.Equal(t, expectedType, call.ResultType)
		}
	}
	require.Empty(t, mapped)
	return oracle
}

func discoveryPath(t *testing.T, root, value string) string {
	t.Helper()
	require.True(t, fs.ValidPath(value), "invalid fixture-relative path %q", value)
	return filepath.Join(root, filepath.FromSlash(value))
}

func materializeDiscovery(t *testing.T, root string, tree []discoveryFile) *os.Root {
	t.Helper()
	fixture, err := os.OpenRoot(root)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, fixture.Close()) })
	for _, entry := range tree {
		discoveryPath(t, root, entry.Path)
		require.NoError(t, fixture.MkdirAll(filepath.Dir(entry.Path), 0o700))
		switch entry.Kind {
		case "directory":
			require.NoError(t, fixture.MkdirAll(entry.Path, 0o700))
		case "file":
			require.Equal(t, entry.SHA256, sha256Text([]byte(entry.Content)))
			require.Equal(t, entry.Size, int64(len(entry.Content)))
			require.NoError(t, fixture.WriteFile(entry.Path, []byte(entry.Content), 0o600))
		case "symlink", "hardlink":
		default:
			t.Fatalf("unsupported fixture entry %q", entry.Kind)
		}
	}
	for _, entry := range tree {
		switch entry.Kind {
		case "symlink":
			require.True(t, strings.HasPrefix(entry.Target, "$TMP/"))
			target := discoveryPath(t, root, strings.TrimPrefix(entry.Target, "$TMP/"))
			require.NoError(t, fixture.Symlink(target, entry.Path))
		case "hardlink":
			discoveryPath(t, root, entry.Target)
			require.NoError(t, fixture.Link(entry.Target, entry.Path))
		}
	}
	return fixture
}

func discoverySnapshot(t *testing.T, fixture *os.Root) []discoveryFile {
	t.Helper()
	tree := []discoveryFile{}
	type identity struct {
		path string
		info fs.FileInfo
	}
	identities := []identity{}
	err := fs.WalkDir(fixture.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		require.NoError(t, walkErr)
		if path == "." {
			return nil
		}
		row := discoveryFile{Path: path}
		switch {
		case entry.Type()&fs.ModeSymlink != 0:
			row.Kind = "symlink"
			target, err := fixture.Readlink(path)
			require.NoError(t, err)
			row.Target = strings.ReplaceAll(filepath.ToSlash(target), filepath.ToSlash(fixture.Name()), "$TMP")
		case entry.IsDir():
			row.Kind = "directory"
		default:
			info, err := entry.Info()
			require.NoError(t, err)
			require.True(t, info.Mode().IsRegular())
			row.Kind = "file"
			for _, prior := range identities {
				if os.SameFile(info, prior.info) {
					row.Kind, row.Target = "hardlink", prior.path
					break
				}
			}
			if row.Kind == "file" {
				content, err := fixture.ReadFile(path)
				require.NoError(t, err)
				row.Content, row.SHA256, row.Size = string(content), sha256Text(content), int64(len(content))
				identities = append(identities, identity{path: path, info: info})
			}
		}
		tree = append(tree, row)
		return nil
	})
	require.NoError(t, err)
	return tree
}

func TestInputDiscoveryParity(t *testing.T) {
	for _, row := range loadDiscoveryOracle(t).Cases {
		t.Run(inputPlanningSubtest(row.SourceCase), func(t *testing.T) {
			if runtime.GOOS == "windows" && strings.Contains(row.SourceCase, "existing_https_colon_filename") {
				t.Skip("the original source explicitly excludes Windows-invalid colon filenames")
			}
			root, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)
			call := row.Calls[0]
			fixture := materializeDiscovery(t, root, call.Filesystem)
			require.Equal(t, call.Filesystem, discoverySnapshot(t, fixture))
			t.Chdir(root)
			if call.Operation == "redact_input_reference" {
				require.Len(t, call.Input.Args, 1)
				actual := analysis.RedactURL(call.Input.Args[0])
				assertNormalizedJSON(t, call.NativeResult, actual)
				require.NotContains(t, actual, "secret")
				require.NotContains(t, actual, "password")
			} else {
				executeDiscovery(t, root, call)
			}
			require.Equal(t, call.Filesystem, discoverySnapshot(t, fixture), "native planning mutated its inputs")
		})
	}
}

func executeDiscovery(t *testing.T, root string, call discoveryCall) {
	t.Helper()
	expand := func(values []string) []string {
		result := make([]string, len(values))
		for index, value := range values {
			result[index] = strings.ReplaceAll(value, "$TMP", root)
		}
		return result
	}
	options := call.Input.Kwargs
	actual, err := analysis.Discover(t.Context(), analysis.InputOptions{
		Positional: expand(options.Positional), Files: expand(options.Files),
		Sources: expand(options.Sources), URLs: options.URLs, Pattern: options.Pattern, Recursive: options.Recursive,
	})
	if call.NativeError != nil {
		require.Error(t, err)
		require.Nil(t, actual)
		var domain *cuclient.Error
		require.True(t, errors.As(err, &domain))
		require.Equal(t, *call.NativeError, discoveryError{
			Kind: string(domain.Kind), Code: domain.Code, Message: domain.Message,
			Hint: domain.Suggestion, StatusCode: domain.StatusCode,
		})
		require.NotContains(t, err.Error(), "secret")
		return
	}
	require.NoError(t, err)
	normalize := func(value string) string {
		return strings.ReplaceAll(filepath.ToSlash(value), filepath.ToSlash(root), "$TMP")
	}
	observed := discoveryResult{Inputs: []discoveryInput{}, ExtensionCounts: map[string]int{}}
	for _, input := range actual {
		value := discoveryInput{
			Path: normalize(input.Path), URL: input.URL, Reference: normalize(input.Reference),
			Relative: filepath.ToSlash(input.Relative),
		}
		if input.URL == "" {
			file, err := input.Open()
			require.NoError(t, err)
			content, readErr := io.ReadAll(file)
			closeErr := file.Close()
			require.NoError(t, readErr)
			require.NoError(t, closeErr)
			size := int64(len(content))
			value.SizeBytes = &size
			observed.TotalBytes += size
		}
		observed.Inputs = append(observed.Inputs, value)
		observed.ExtensionCounts[strings.ToLower(filepath.Ext(input.Relative))]++
	}
	assertNormalizedJSON(t, call.NativeResult, observed)
}

func TestInputDiscoveryControlsAreNotRecordings(t *testing.T) {
	loadDiscoveryOracle(t)
	body, err := os.ReadFile("../../testdata/parity/input_discovery.json")
	require.NoError(t, err)
	require.Error(t, recordingHygiene(body), "public synthetic secret controls must remain rejected as recordings")
	for _, recording := range loadParityInventory(t).Recordings {
		require.NotEqual(t, "testdata/parity/input_discovery.json", recording.File)
	}
}
