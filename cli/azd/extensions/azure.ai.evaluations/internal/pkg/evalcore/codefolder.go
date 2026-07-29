// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package evalcore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// CodeEvaluatorMetadataFile is the optional descriptor a folder can carry so
// the schemas travel with the code instead of having to be repeated on the
// command line every time it is published.
//
// It is not part of the service contract — the service only ever sees the
// definition assembled from it — so it is read leniently and its absence is
// normal.
const CodeEvaluatorMetadataFile = "evaluator.json"

// pythonExt is the only source extension a code evaluator entry point can have.
const pythonExt = ".py"

// excludedDirNames are directories that never carry evaluator source: build
// caches, version control, and dependency trees. Any dot-prefixed directory is
// excluded as well, which is checked separately.
var excludedDirNames = map[string]bool{
	"__pycache__":  true,
	".git":         true,
	".venv":        true,
	"venv":         true,
	"node_modules": true,
}

// excludedFileExts are compiled Python artifacts. They are derived from the
// sources beside them, so uploading them adds nothing and — because they embed
// a timestamp — would make the folder fingerprint change on every rebuild.
var excludedFileExts = map[string]bool{
	".pyc": true,
	".pyo": true,
}

// CodeFile is one file in an evaluator package.
type CodeFile struct {
	// RelPath is the path relative to the package root, always
	// slash-separated. Windows and Linux must agree on it: it is both the blob
	// name the file is uploaded under and part of the fingerprint, so a
	// backslash here would republish the whole package on a change of machine.
	RelPath string

	// AbsPath is where the file is read from on this machine. It is
	// deliberately not part of the fingerprint.
	AbsPath string
}

// CodeEvaluatorMetadata is the optional descriptor read from
// CodeEvaluatorMetadataFile. Every field is optional; the raw JSON fields are
// passed to the service untouched so a schema this extension does not model
// still reaches it intact.
type CodeEvaluatorMetadata struct {
	DisplayName    string          `json:"display_name,omitempty"`
	Description    string          `json:"description,omitempty"`
	Categories     []string        `json:"categories,omitempty"`
	InitParameters json.RawMessage `json:"init_parameters,omitempty"`
	DataSchema     json.RawMessage `json:"data_schema,omitempty"`
	Metrics        json.RawMessage `json:"metrics,omitempty"`
}

// CodeEvaluatorPackage is a validated folder ready to publish.
type CodeEvaluatorPackage struct {
	// Name is the evaluator name the package was validated against.
	Name string
	// Root is the folder on disk.
	Root string
	// Files are the files to upload, in fingerprint order.
	Files []CodeFile
	// EntryPoint is the Python file holding the evaluator class.
	EntryPoint string
	// ClassName is the class the runtime instantiates.
	ClassName string
	// Metadata is the folder's descriptor, or nil when it carries none.
	Metadata *CodeEvaluatorMetadata
}

// IsCodeEvaluatorSource reports whether a declared source names a folder, and
// therefore a code evaluator rather than a rubric.
//
// The path is stat-ed rather than pattern-matched: a trailing separator is not
// required in YAML and `.json` in a folder name would misclassify it.
func IsCodeEvaluatorSource(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// EvaluatorClassName derives the class the runtime looks for from the
// evaluator name: the name in PascalCase, suffixed with Evaluator.
//
// The suffix is not appended twice, so both spellings customers use resolve to
// the same class — `answer_length` and `answer_length_evaluator` both mean
// AnswerLengthEvaluator.
func EvaluatorClassName(name string) string {
	parts := strings.FieldsFunc(name, func(r rune) bool {
		return r == '_' || r == '-' || r == '.' || r == ' '
	})

	var b strings.Builder
	for _, part := range parts {
		runes := []rune(part)
		b.WriteRune(unicode.ToUpper(runes[0]))
		b.WriteString(string(runes[1:]))
	}

	pascal := b.String()
	if strings.HasSuffix(pascal, "Evaluator") {
		return pascal
	}
	return pascal + "Evaluator"
}

// WalkCodeFolder lists the files that make up an evaluator package, excluding
// build caches, version control, dependency trees, and compiled Python.
//
// The result is sorted by RelPath so the upload order and the fingerprint do
// not depend on directory iteration order, which the filesystem does not
// promise to keep stable.
func WalkCodeFolder(dir string) ([]CodeFile, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("reading evaluator folder %q: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf(
			"evaluator source %q is a file; a code evaluator is published from a folder", dir)
	}

	var files []CodeFile
	walkErr := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}

		if entry.IsDir() {
			if isExcludedDir(entry.Name()) {
				return fs.SkipDir
			}
			return nil
		}

		// Symlinks, sockets and devices carry no content that can be uploaded,
		// and WalkDir does not follow them, so they would otherwise be
		// published as zero bytes.
		if !entry.Type().IsRegular() {
			return nil
		}
		// Dot-prefixed files are excluded for the same reason as dot-prefixed
		// directories, and one reason more: a folder kept next to an evaluator
		// tends to collect `.env`, `.netrc` and `.pypirc`, and publishing the
		// package would copy those secrets into blob storage. Nothing a Python
		// evaluator needs at runtime is named with a leading dot.
		if strings.HasPrefix(entry.Name(), ".") {
			return nil
		}
		if excludedFileExts[strings.ToLower(filepath.Ext(rel))] {
			return nil
		}

		files = append(files, CodeFile{RelPath: rel, AbsPath: path})
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("reading evaluator folder %q: %w", dir, walkErr)
	}

	sortCodeFiles(files)
	return files, nil
}

// isExcludedDir reports whether a directory is skipped along with everything
// under it.
func isExcludedDir(name string) bool {
	return excludedDirNames[name] || strings.HasPrefix(name, ".")
}

func sortCodeFiles(files []CodeFile) {
	sort.Slice(files, func(i, j int) bool { return files[i].RelPath < files[j].RelPath })
}

// FingerprintCodeFolder hashes a folder so a later deploy can tell whether the
// evaluator changed without downloading anything from the service.
func FingerprintCodeFolder(dir string) (string, error) {
	files, err := WalkCodeFolder(dir)
	if err != nil {
		return "", err
	}
	return FingerprintCodeFiles(files)
}

// FingerprintCodeFiles hashes an already-walked package.
//
// Both the relative path and the content of every file are hashed, so renaming
// a file registers as a change even when the bytes are identical. The input is
// sorted first: the digest must describe the package, not the order the caller
// happened to hand the files over in.
//
// Only RelPath — never AbsPath — feeds the hash, and RelPath is normalized to
// forward slashes, so the same package hashes the same on Windows and Linux.
func FingerprintCodeFiles(files []CodeFile) (string, error) {
	ordered := make([]CodeFile, len(files))
	copy(ordered, files)
	sortCodeFiles(ordered)

	outer := sha256.New()
	for _, file := range ordered {
		content, err := os.ReadFile(file.AbsPath)
		if err != nil {
			return "", fmt.Errorf("hashing %q: %w", file.AbsPath, err)
		}
		inner := sha256.Sum256(content)
		// Path and content digest are written on separate lines, which keeps
		// the encoding unambiguous: a file's content digest is fixed width, so
		// no path can be read as part of it.
		fmt.Fprintf(outer, "%s\n%s\n", file.RelPath, hex.EncodeToString(inner[:]))
	}
	return hex.EncodeToString(outer.Sum(nil)), nil
}

// LoadCodeEvaluator validates a folder against the packaging convention and
// returns the package to publish.
//
// The checks are done here rather than left to the service because the service
// only discovers a missing entry point when a run executes, long after a
// version has been published and an eval bound to it.
func LoadCodeEvaluator(name, dir string) (*CodeEvaluatorPackage, error) {
	if name == "" {
		return nil, fmt.Errorf("an evaluator name is required to validate the folder layout")
	}

	files, err := WalkCodeFolder(dir)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf(
			"evaluator folder %q holds no files to publish", dir)
	}

	entryPoint := name + pythonExt
	className := EvaluatorClassName(name)

	entry, ok := findFile(files, entryPoint)
	if !ok {
		return nil, fmt.Errorf(
			"evaluator %q needs %s in %s, holding a class named %s. The folder holds %s",
			name, entryPoint, dir, className, describeFiles(files))
	}

	source, err := os.ReadFile(entry.AbsPath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", entry.RelPath, err)
	}
	if !declaresClass(source, className) {
		return nil, fmt.Errorf(
			"%s does not declare a class named %s. A code evaluator is a class with that "+
				"exact name and a __call__ method that takes **kwargs and returns a JSON "+
				"object, for example:\n\nclass %s:\n    def __call__(self, **kwargs):\n"+
				"        return {\"result\": 1}",
			filepath.Join(dir, entry.RelPath), className, className)
	}

	metadata, err := readCodeEvaluatorMetadata(files)
	if err != nil {
		return nil, err
	}

	return &CodeEvaluatorPackage{
		Name:       name,
		Root:       dir,
		Files:      files,
		EntryPoint: entryPoint,
		ClassName:  className,
		Metadata:   metadata,
	}, nil
}

func findFile(files []CodeFile, relPath string) (CodeFile, bool) {
	for _, file := range files {
		if file.RelPath == relPath {
			return file, true
		}
	}
	return CodeFile{}, false
}

// describeFiles renders the folder's contents for an error message, capped so
// a large package does not bury the advice that follows it.
func describeFiles(files []CodeFile) string {
	const limit = 10
	names := make([]string, 0, limit)
	for i, file := range files {
		if i == limit {
			return fmt.Sprintf("%s and %d more", strings.Join(names, ", "), len(files)-limit)
		}
		names = append(names, file.RelPath)
	}
	return strings.Join(names, ", ")
}

// declaresClass reports whether the source declares the named class at any
// indentation, in either the bare or the inheriting form.
func declaresClass(source []byte, className string) bool {
	pattern := `(?m)^[ \t]*class[ \t]+` + regexp.QuoteMeta(className) + `[ \t]*[(:]`
	matched, err := regexp.Match(pattern, source)
	return err == nil && matched
}

// readCodeEvaluatorMetadata reads the optional folder descriptor. A folder
// without one is normal, so absence is not an error; malformed JSON is, since
// silently ignoring it would publish an evaluator missing the schemas the
// author wrote down.
func readCodeEvaluatorMetadata(files []CodeFile) (*CodeEvaluatorMetadata, error) {
	entry, ok := findFile(files, CodeEvaluatorMetadataFile)
	if !ok {
		return nil, nil
	}

	raw, err := os.ReadFile(entry.AbsPath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", entry.RelPath, err)
	}

	var metadata CodeEvaluatorMetadata
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON: %w", entry.RelPath, err)
	}
	return &metadata, nil
}
