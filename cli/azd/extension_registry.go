// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build mage

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
)

// CheckExtensionAzdVersions checks added or modified registry versions against the latest published azd version.
//
// Usage: go tool mage checkExtensionAzdVersions <base-ref> <published-azd-version>
func CheckExtensionAzdVersions(ctx context.Context, baseRef, publishedVersion string) error {
	published, err := semver.NewVersion(strings.TrimSpace(publishedVersion))
	if err != nil {
		return fmt.Errorf("invalid published azd version %q: %w", publishedVersion, err)
	}

	repoRoot, err := findRepoRoot()
	if err != nil {
		return err
	}

	var issues []error
	for _, name := range []string{"registry.json", "registry.dev.json"} {
		registryPath := "cli/azd/extensions/" + name
		// #nosec G204 -- Git receives a revision after --end-of-options, without a shell.
		cmd := exec.CommandContext(ctx, "git", "show", "--end-of-options", baseRef+":"+registryPath)
		cmd.Dir = repoRoot
		baseData, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("reading %s at %s: %w", registryPath, baseRef, err)
		}
		proposedData, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(registryPath)))
		if err != nil {
			return fmt.Errorf("reading %s: %w", registryPath, err)
		}

		var base, proposed extensions.Registry
		if err := json.Unmarshal(baseData, &base); err != nil {
			return fmt.Errorf("parsing %s at %s: %w", registryPath, baseRef, err)
		}
		if err := json.Unmarshal(proposedData, &proposed); err != nil {
			return fmt.Errorf("parsing %s: %w", registryPath, err)
		}
		if err := validateExtensionAzdVersions(&base, &proposed, published); err != nil {
			issues = append(issues, fmt.Errorf("%s: %w", registryPath, err))
		}
	}

	return errors.Join(issues...)
}

func validateExtensionAzdVersions(base, proposed *extensions.Registry, published *semver.Version) error {
	type releaseKey struct {
		id      string
		version string
	}
	existing := map[releaseKey]extensions.ExtensionVersion{}
	for _, ext := range base.Extensions {
		if ext == nil {
			return errors.New("base registry contains a null extension")
		}
		for _, version := range ext.Versions {
			existing[releaseKey{ext.Id, version.Version}] = version
		}
	}

	var issues []error
	for _, ext := range proposed.Extensions {
		if ext == nil {
			return errors.New("proposed registry contains a null extension")
		}
		for _, version := range ext.Versions {
			previous, exists := existing[releaseKey{ext.Id, version.Version}]
			if exists && reflect.DeepEqual(previous, version) {
				continue
			}
			if version.RequiredAzdVersion == "" {
				continue
			}

			constraint, err := semver.NewConstraint(version.RequiredAzdVersion)
			if err != nil {
				issues = append(issues, fmt.Errorf(
					"[%s@%s] invalid requiredAzdVersion %q: %w",
					ext.Id, version.Version, version.RequiredAzdVersion, err,
				))
			} else if !constraint.Check(published) {
				issues = append(issues, fmt.Errorf(
					"[%s@%s] requiredAzdVersion %q is not satisfied by latest published azd %s; "+
						"wait for core CLI publishing to finish, then rerun ext-registry-ci",
					ext.Id, version.Version, version.RequiredAzdVersion, published,
				))
			}
		}
	}
	return errors.Join(issues...)
}
