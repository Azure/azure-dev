// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package foundry

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

const (
	refKey      = "$ref"
	maxRefDepth = 32
)

var (
	remoteRefPattern = regexp.MustCompile(`(?i)^[a-z][a-z0-9+.-]*://`)
	yamlLinePattern  = regexp.MustCompile(`^yaml: line ([0-9]+):`)
)

type containedProject struct {
	projectRoot     string
	realProjectRoot string
	root            *os.Root
}

// ReadContainedFile reads a file contained by the project directory.
func ReadContainedFile(projectRoot, path string) ([]byte, string, error) {
	project, err := openContainedProject(projectRoot)
	if err != nil {
		return nil, "", err
	}
	defer project.root.Close()

	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return nil, "", errors.New("cannot resolve the project file path")
	}
	target, err := project.containedTarget(absolutePath)
	if err != nil {
		return nil, "", err
	}
	data, err := project.read(target.real)
	if err != nil {
		return nil, "", fmt.Errorf("cannot read project file %q", target.display)
	}
	return data, target.display, nil
}

// DecodeContainedDocument reads a YAML or JSON object in the project.
func DecodeContainedDocument(projectRoot, path string) (map[string]any, string, error) {
	data, display, err := ReadContainedFile(projectRoot, path)
	if err != nil {
		return nil, "", err
	}
	document, err := decodeObject(data, display)
	if err != nil {
		return nil, "", err
	}
	return document, display, nil
}

// ResolveContainedFileRefs resolves local $ref files in the project.
func ResolveContainedFileRefs(
	config map[string]any,
	projectRoot string,
) (map[string]any, error) {
	return resolveContainedFileRefs(config, projectRoot, "")
}

// ResolveContainedFileRefsFromFile resolves refs from a source file.
func ResolveContainedFileRefsFromFile(
	config map[string]any,
	projectRoot string,
	sourceFilePath string,
) (map[string]any, error) {
	return resolveContainedFileRefs(config, projectRoot, sourceFilePath)
}

func resolveContainedFileRefs(
	config map[string]any,
	projectRoot string,
	sourceFilePath string,
) (map[string]any, error) {
	if config == nil {
		return nil, nil
	}

	project, err := openContainedProject(projectRoot)
	if err != nil {
		return nil, err
	}
	defer project.root.Close()

	baseDir := project.projectRoot
	if sourceFilePath != "" {
		absolutePath, err := filepath.Abs(sourceFilePath)
		if err != nil {
			return nil, errors.New("cannot resolve the source file path")
		}
		source, err := project.containedTarget(absolutePath)
		if err != nil {
			return nil, err
		}
		baseDir = filepath.Dir(source.lexical)
	}

	resolved, err := project.resolveValue(config, baseDir, nil)
	if err != nil {
		return nil, err
	}
	result, ok := resolved.(map[string]any)
	if !ok {
		return nil, errors.New("resolved configuration is not an object")
	}
	return result, nil
}

func openContainedProject(projectRoot string) (*containedProject, error) {
	absoluteRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return nil, errors.New("cannot resolve the project directory")
	}
	realRoot, err := filepath.EvalSymlinks(absoluteRoot)
	if err != nil {
		return nil, errors.New("cannot resolve the project directory")
	}
	realRoot, err = filepath.Abs(realRoot)
	if err != nil {
		return nil, errors.New("cannot resolve the project directory")
	}

	root, err := os.OpenRoot(realRoot)
	if err != nil {
		return nil, errors.New("cannot open the project directory")
	}
	info, err := root.Stat(".")
	if err != nil || !info.IsDir() {
		_ = root.Close()
		return nil, errors.New("the project path is not a directory")
	}

	return &containedProject{
		projectRoot:     filepath.Clean(absoluteRoot),
		realProjectRoot: filepath.Clean(realRoot),
		root:            root,
	}, nil
}

type containedTarget struct {
	lexical string
	real    string
	display string
}

func (p *containedProject) resolveTarget(ref, baseDir string) (containedTarget, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return containedTarget{}, errors.New("$ref value must not be empty")
	}
	if remoteRefPattern.MatchString(ref) {
		return containedTarget{}, errors.New("remote $ref values are not supported")
	}

	target := ref
	if !filepath.IsAbs(target) {
		target = filepath.Join(baseDir, target)
	}
	absoluteTarget, err := filepath.Abs(target)
	if err != nil {
		return containedTarget{}, errors.New("$ref must resolve to a file inside the project")
	}
	return p.containedTarget(absoluteTarget)
}

func (p *containedProject) containedTarget(target string) (containedTarget, error) {
	absoluteTarget, err := filepath.Abs(target)
	if err != nil {
		return containedTarget{}, errors.New("file must resolve inside the project")
	}
	absoluteTarget = filepath.Clean(absoluteTarget)
	relativeTarget, err := filepath.Rel(p.projectRoot, absoluteTarget)
	if err != nil || !pathIsWithinRoot(relativeTarget) {
		return containedTarget{}, errors.New("file must resolve inside the project")
	}

	realTarget, err := filepath.EvalSymlinks(absoluteTarget)
	if err != nil {
		return containedTarget{}, errors.New("cannot resolve the file inside the project")
	}
	realTarget, err = filepath.Abs(realTarget)
	if err != nil {
		return containedTarget{}, errors.New("cannot resolve the file inside the project")
	}
	relativeRealTarget, err := filepath.Rel(p.realProjectRoot, realTarget)
	if err != nil || !pathIsWithinRoot(relativeRealTarget) {
		return containedTarget{}, errors.New("file resolves outside the project")
	}

	return containedTarget{
		lexical: absoluteTarget,
		real:    filepath.Clean(realTarget),
		display: filepath.ToSlash(relativeTarget),
	}, nil
}

func pathIsWithinRoot(relative string) bool {
	if filepath.IsAbs(relative) {
		return false
	}
	clean := filepath.Clean(relative)
	return clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

func (p *containedProject) read(realPath string) ([]byte, error) {
	relative, err := filepath.Rel(p.realProjectRoot, realPath)
	if err != nil || !pathIsWithinRoot(relative) {
		return nil, os.ErrPermission
	}
	file, err := p.root.Open(relative)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(file)
}

func (p *containedProject) resolveValue(
	value any,
	baseDir string,
	chain []string,
) (any, error) {
	switch typed := value.(type) {
	case map[string]any:
		if _, found := typed[refKey]; found {
			return p.resolveRef(typed, baseDir, chain)
		}
		resolved := make(map[string]any, len(typed))
		for key, item := range typed {
			result, err := p.resolveValue(item, baseDir, chain)
			if err != nil {
				return nil, err
			}
			resolved[key] = result
		}
		return resolved, nil
	case []any:
		resolved := make([]any, len(typed))
		for index, item := range typed {
			result, err := p.resolveValue(item, baseDir, chain)
			if err != nil {
				return nil, err
			}
			resolved[index] = result
		}
		return resolved, nil
	default:
		return value, nil
	}
}

func (p *containedProject) resolveRef(
	directive map[string]any,
	baseDir string,
	chain []string,
) (any, error) {
	ref, ok := directive[refKey].(string)
	if !ok {
		return nil, errors.New("$ref value must be a string")
	}
	target, err := p.resolveTarget(ref, baseDir)
	if err != nil {
		return nil, err
	}
	if slices.Contains(chain, target.real) {
		return nil, fmt.Errorf("cyclic $ref include detected at %q", target.display)
	}
	if len(chain) >= maxRefDepth {
		return nil, fmt.Errorf("$ref nesting exceeds %d levels at %q", maxRefDepth, target.display)
	}

	data, err := p.read(target.real)
	if err != nil {
		return nil, fmt.Errorf("cannot read $ref file %q", target.display)
	}
	loaded, err := decodeObject(data, target.display)
	if err != nil {
		return nil, err
	}

	nextChain := append(slices.Clone(chain), target.real)
	resolved, err := p.resolveValue(loaded, filepath.Dir(target.lexical), nextChain)
	if err != nil {
		return nil, err
	}
	result, ok := resolved.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("$ref file %q must contain an object", target.display)
	}
	for key, value := range directive {
		if key == refKey {
			continue
		}
		resolvedValue, err := p.resolveValue(value, baseDir, chain)
		if err != nil {
			return nil, err
		}
		result[key] = resolvedValue
	}
	return result, nil
}

func decodeObject(data []byte, display string) (map[string]any, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var result map[string]any
	if err := decoder.Decode(&result); err != nil && !errors.Is(err, io.EOF) {
		return nil, yamlObjectError(display, err)
	}
	if result == nil {
		return nil, fmt.Errorf("file %q is empty or not an object", display)
	}

	var trailing yaml.Node
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, yamlObjectError(display, err)
		}
		return nil, fmt.Errorf("file %q must contain one YAML or JSON object", display)
	}
	return result, nil
}

func yamlObjectError(display string, err error) error {
	message := fmt.Sprintf("file %q is not a valid YAML or JSON object", display)
	if match := yamlLinePattern.FindStringSubmatch(err.Error()); len(match) == 2 {
		message += " at line " + match[1]
	}
	return errors.New(message)
}
