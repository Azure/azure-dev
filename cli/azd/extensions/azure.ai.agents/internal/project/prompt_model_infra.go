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
	for _, target := range targets {
		provider := strings.TrimSpace(target.Provider)
		if provider == "" {
			provider = strings.TrimSpace(document.Infra.Provider)
		}
		path := strings.TrimSpace(target.Path)
		if path == "" {
			path = "infra"
		}
		dir := filepath.Join(root, filepath.FromSlash(path))
		if provider == "terraform" {
			if !layered {
				return true, nil
			}
			marker, err := os.ReadFile(filepath.Join(dir, ".azd-foundry"))
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return false, fmt.Errorf("read Foundry infrastructure marker: %w", err)
			}
			if string(marker) == "terraform-v1\n" {
				return true, nil
			}
			if errors.Is(err, fs.ErrNotExist) {
				source, err := os.ReadFile(filepath.Join(dir, "main.tf"))
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
		module := strings.TrimSuffix(strings.TrimSpace(target.Module), ".bicep")
		if module == "" {
			module = "main"
		}
		for _, suffix := range []string{".bicep", ".bicepparam"} {
			path := filepath.Join(dir, module+suffix)
			info, err := os.Stat(path)
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return false, fmt.Errorf("inspect infrastructure %q: %w", path, err)
			}
			if err == nil && !info.IsDir() {
				return true, nil
			}
		}
	}
	return false, nil
}
