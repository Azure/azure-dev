// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	toml "github.com/pelletier/go-toml/v2"
)

// HarborTask is a validated local Harbor task directory.
type HarborTask struct {
	// Dir is the absolute task directory.
	Dir string
	// Name is the task directory name.
	Name string
	// ImageTag is the content-addressed tag, task-<hash>, of the task's environment.
	ImageTag string
	// Dockerfile is the absolute path of environment/Dockerfile.
	Dockerfile string
	// EnvironmentDir is the absolute Docker build context, <Dir>/environment.
	EnvironmentDir string
}

// LoadHarborTask validates a Harbor task directory and computes its image identity.
// Multi-container, multi-step and GPU tasks are rejected because rollout does not support them.
func LoadHarborTask(dir string) (*HarborTask, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	for _, rel := range []string{"task.toml", "instruction.md", "environment/Dockerfile", "tests/test.sh"} {
		if _, err := os.Stat(filepath.Join(abs, filepath.FromSlash(rel))); err != nil {
			return nil, harborTaskError(
				fmt.Sprintf("Harbor task %q is missing %s.", abs, rel),
				"rle_harbor_task_invalid",
				"Pass the directory of a Harbor task containing task.toml, instruction.md, "+
					"environment/Dockerfile and tests/test.sh.",
			)
		}
	}
	if _, err := os.Stat(filepath.Join(abs, "environment", "docker-compose.yaml")); err == nil {
		return nil, harborTaskError(
			"Multi-container Harbor tasks (environment/docker-compose.yaml) are not supported.",
			"rle_harbor_task_unsupported",
			"Use a single-container task.",
		)
	}
	if err := rejectUnsupportedHarborConfig(filepath.Join(abs, "task.toml")); err != nil {
		return nil, err
	}
	hash, err := hashHarborTask(abs)
	if err != nil {
		return nil, err
	}
	return &HarborTask{
		Dir:            abs,
		Name:           filepath.Base(abs),
		ImageTag:       "task-" + hash,
		Dockerfile:     filepath.Join(abs, "environment", "Dockerfile"),
		EnvironmentDir: filepath.Join(abs, "environment"),
	}, nil
}

func rejectUnsupportedHarborConfig(path string) error {
	//nolint:gosec // Reads task.toml from the explicitly selected local task directory.
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return validateHarborConfig(raw)
}

func validateHarborConfig(raw []byte) error {
	var config struct {
		Environment struct {
			GPUs int `toml:"gpus"`
		} `toml:"environment"`
		Steps []map[string]any `toml:"steps"`
	}
	if err := toml.Unmarshal(raw, &config); err != nil {
		return harborTaskError(
			fmt.Sprintf("Harbor task.toml is not valid TOML: %v", err),
			"rle_harbor_task_invalid",
			"Fix task.toml.",
		)
	}
	if config.Environment.GPUs > 0 {
		return harborTaskError("GPU Harbor tasks are not supported.", "rle_harbor_task_unsupported",
			"Use a task with environment.gpus = 0.")
	}
	if len(config.Steps) > 0 {
		return harborTaskError("Multi-step Harbor tasks are not supported.", "rle_harbor_task_unsupported",
			"Use a single-step task.")
	}
	return nil
}

// harborBundleEntries are the task paths, relative to the task directory, sent with the request.
// They stay out of the image so the agent cannot read tests/ before verification.
var harborBundleEntries = []string{"task.toml", "instruction.md", "tests"}

// MaxHarborBundleBytes caps the compressed task bundle carried in the rollout request.
const MaxHarborBundleBytes = 64 << 20

// hashHarborTask hashes the environment directory in a stable order, so the image tag changes
// whenever anything inside the image changes.
func hashHarborTask(taskDir string) (string, error) {
	h := sha256.New()
	for _, rel := range []string{"environment"} {
		files, err := listRegularFiles(filepath.Join(taskDir, rel))
		if err != nil {
			return "", err
		}
		for _, path := range files {
			name, err := filepath.Rel(taskDir, path)
			if err != nil {
				return "", err
			}
			//nolint:gosec // Paths come from walking regular files in the selected task's environment directory.
			content, err := os.ReadFile(path)
			if err != nil {
				return "", err
			}
			fmt.Fprintf(h, "%s\x00%d\x00", filepath.ToSlash(name), len(content))
			h.Write(content)
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

func listRegularFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			files = append(files, path)
		}
		return nil
	})
	sort.Strings(files)
	return files, err
}

// BuildHarborBundle returns a tar.gz of task.toml, instruction.md and tests/.
func BuildHarborBundle(task *HarborTask) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, rel := range harborBundleEntries {
		root := filepath.Join(task.Dir, rel)
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && !d.Type().IsRegular() {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			name, err := filepath.Rel(task.Dir, path)
			if err != nil {
				return err
			}
			header, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return err
			}
			header.Name = filepath.ToSlash(name)
			header.Uid, header.Gid, header.Uname, header.Gname = 0, 0, "", ""
			header.ModTime = time.Unix(0, 0)
			if err := tw.WriteHeader(header); err != nil || d.IsDir() {
				return err
			}
			//nolint:gosec // WalkDir only includes regular files beneath the selected task bundle entries.
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			_, err = tw.Write(content)
			return err
		})
		if err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	if buf.Len() > MaxHarborBundleBytes {
		return nil, harborTaskError(
			fmt.Sprintf("Harbor task files (task.toml, instruction.md, tests/) compress to %d bytes; the limit is %d.",
				buf.Len(), MaxHarborBundleBytes),
			"rle_harbor_task_too_large",
			"Move large fixtures into the task image (environment/) and keep tests/ small.",
		)
	}
	return buf.Bytes(), nil
}

func harborTaskError(message, code, suggestion string) error {
	return &azdext.LocalError{
		Message:    message,
		Code:       code,
		Category:   azdext.LocalErrorCategoryUser,
		Suggestion: suggestion,
	}
}
