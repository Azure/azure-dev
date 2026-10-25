// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"azureaiagent/internal/pkg/paths"

	"go.yaml.in/yaml/v3"
)

// promptModelInfrastructureOnDisk detects user-owned deployment inputs.
// Their environment deployment list may be stale after parameter edits.
func promptModelInfrastructureOnDisk(root string) (bool, error) {
	if root == "" {
		return false, nil
	}
	var raw []byte
	for _, name := range []string{"azure.yaml", "azure.yml"} {
		var err error
		raw, err = os.ReadFile(filepath.Join(root, name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("read infrastructure configuration: %w", err)
		}
		break
	}
	type infraTarget struct {
		Provider string `yaml:"provider"`
		Path     string `yaml:"path"`
		Module   string `yaml:"module"`
	}
	var document struct {
		Infra struct {
			infraTarget `yaml:",inline"`
			Layers      []infraTarget `yaml:"layers"`
		} `yaml:"infra"`
	}
	if err := yaml.Unmarshal(raw, &document); err != nil {
		return false, fmt.Errorf("parse infrastructure configuration: %w", err)
	}
	targets := document.Infra.Layers
	layered := len(targets) > 0
	if !layered {
		targets = []infraTarget{document.Infra.infraTarget}
	}
	for i, target := range targets {
		field := "infra"
		if layered {
			field = fmt.Sprintf("infra.layers[%d]", i)
		}
		provider := strings.TrimSpace(target.Provider)
		if provider == "" {
			provider = strings.TrimSpace(document.Infra.Provider)
		}
		path := strings.TrimSpace(target.Path)
		if path == "" {
			path = "infra"
		}
		dir, err := paths.JoinAllowRoot(root, path)
		if err != nil {
			return false, fmt.Errorf("resolve azure.yaml %s.path: %w", field, err)
		}
		module := strings.TrimSuffix(strings.TrimSpace(target.Module), ".bicep")
		if module == "" {
			module = "main"
		}
		// Embedded infrastructure may not have an on-disk directory yet.
		if _, err := paths.Join(dir, module); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return false, fmt.Errorf("resolve azure.yaml %s.module: %w", field, err)
		}
		if provider == "terraform" {
			if !layered {
				for _, name := range []string{".azd-foundry", "main.tf"} {
					if _, err := paths.JoinAllowRoot(root, path, name); err != nil {
						return false, fmt.Errorf("resolve azure.yaml %s.path Terraform input: %w", field, err)
					}
				}
				if _, err := paths.JoinAllowRoot(root, path, filepath.FromSlash(module+".tf")); err != nil {
					return false, fmt.Errorf("resolve azure.yaml %s.module: %w", field, err)
				}
				return true, nil
			}
			markerPath, err := paths.JoinAllowRoot(root, path, ".azd-foundry")
			if err != nil {
				return false, fmt.Errorf("resolve azure.yaml %s.path Foundry marker: %w", field, err)
			}
			marker, err := os.ReadFile(markerPath)
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return false, fmt.Errorf("read Foundry infrastructure marker: %w", err)
			}
			if string(marker) == "terraform-v1\n" {
				return true, nil
			}
			if errors.Is(err, fs.ErrNotExist) {
				sourcePath, err := paths.JoinAllowRoot(root, path, "main.tf")
				if err != nil {
					return false, fmt.Errorf("resolve azure.yaml %s.path Terraform source: %w", field, err)
				}
				source, err := os.ReadFile(sourcePath)
				if err != nil && !errors.Is(err, fs.ErrNotExist) {
					return false, fmt.Errorf("read Terraform infrastructure: %w", err)
				}
				if strings.Contains(string(source), `resource "azapi_resource" "foundry_account"`) &&
					strings.Contains(string(source), `resource "azapi_resource" "project"`) {
					return true, nil
				}
			}
			continue
		}
		if layered && provider != "microsoft.foundry" {
			continue
		}
		for _, suffix := range []string{".bicep", ".bicepparam"} {
			file, err := paths.JoinAllowRoot(root, path, filepath.FromSlash(module+suffix))
			if err != nil {
				return false, fmt.Errorf("resolve azure.yaml %s.module: %w", field, err)
			}
			info, err := os.Stat(file)
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return false, fmt.Errorf("inspect infrastructure %q: %w", file, err)
			}
			if err == nil && !info.IsDir() {
				return true, nil
			}
		}
	}
	return false, nil
}
