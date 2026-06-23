// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

// BundleRegistryFileName is the well-known registry file name expected at the
// root of a self-contained extension bundle.
const BundleRegistryFileName = "registry.json"

// newBundleSource creates a new extension source backed by an extracted
// self-contained extension bundle. A bundle is a directory (typically the
// extraction target of a portable .zip) that contains a registry.json at its
// root alongside artifact archives referenced by paths relative to that
// directory.
//
// Unlike a plain file source, a bundle source rewrites relative artifact URLs
// to absolute paths anchored at the bundle directory. This allows the bundle to
// be fully portable: the registry.json author does not need to know the final
// extraction location, and the installer's local-path artifact handling works
// unchanged because it receives absolute paths.
func newBundleSource(name string, location string) (Source, error) {
	bundleDir, registryPath, err := resolveBundlePaths(location)
	if err != nil {
		return nil, err
	}

	registryBytes, err := os.ReadFile(registryPath)
	if err != nil {
		return nil, fmt.Errorf("failed reading bundle registry '%s': %w", registryPath, err)
	}

	var registry *Registry
	if err := json.Unmarshal(registryBytes, &registry); err != nil {
		return nil, fmt.Errorf("unable to unmarshal bundle registry '%s': %w", registryPath, err)
	}

	if registry == nil {
		return nil, fmt.Errorf("bundle registry '%s' is empty or null", registryPath)
	}

	if err := CheckRegistrySchemaVersion(registry.SchemaVersion); err != nil {
		return nil, err
	}

	if err := anchorRelativeArtifacts(registry, bundleDir); err != nil {
		return nil, err
	}

	return newRegistrySource(name, registry)
}

// resolveBundlePaths normalizes a bundle source location to the bundle directory
// and the absolute path of its registry.json. The location may be either the
// bundle directory itself or the path to the registry.json within it.
func resolveBundlePaths(location string) (bundleDir string, registryPath string, err error) {
	absLocation, err := filepath.Abs(location)
	if err != nil {
		return "", "", fmt.Errorf("failed resolving bundle location '%s': %w", location, err)
	}

	info, err := os.Stat(absLocation)
	if err != nil {
		return "", "", fmt.Errorf("failed accessing bundle location '%s': %w", location, err)
	}

	if info.IsDir() {
		bundleDir = absLocation
		registryPath = filepath.Join(absLocation, BundleRegistryFileName)
	} else {
		bundleDir = filepath.Dir(absLocation)
		registryPath = absLocation
	}

	return bundleDir, registryPath, nil
}

// anchorRelativeArtifacts rewrites relative artifact URLs in the registry to
// absolute paths rooted at bundleDir. Absolute paths and remote (http/https)
// URLs are left untouched. Relative paths that escape the bundle directory are
// rejected to prevent path traversal.
func anchorRelativeArtifacts(registry *Registry, bundleDir string) error {
	for _, extension := range registry.Extensions {
		for vi := range extension.Versions {
			version := &extension.Versions[vi]
			for key, artifact := range version.Artifacts {
				resolved, err := anchorArtifactURL(artifact.URL, bundleDir)
				if err != nil {
					return fmt.Errorf(
						"extension '%s' version '%s' artifact '%s': %w",
						extension.Id, version.Version, key, err,
					)
				}

				artifact.URL = resolved
				version.Artifacts[key] = artifact
			}
		}
	}

	return nil
}

// anchorArtifactURL converts a relative artifact URL to an absolute path within
// bundleDir. Remote URLs and already-absolute paths are returned unchanged.
func anchorArtifactURL(url string, bundleDir string) (string, error) {
	if url == "" {
		return url, nil
	}

	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		return url, nil
	}

	if filepath.IsAbs(url) {
		return url, nil
	}

	resolved := filepath.Join(bundleDir, filepath.FromSlash(url))
	if !osutil.IsPathContained(bundleDir, resolved) {
		return "", fmt.Errorf(
			"artifact path %q resolves outside the bundle directory; relative paths must not contain '..' sequences",
			url,
		)
	}

	return resolved, nil
}
