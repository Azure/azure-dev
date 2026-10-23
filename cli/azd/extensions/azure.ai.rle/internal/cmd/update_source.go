// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

var updateSourceNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

type extensionUpdateSource struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Location string `json:"location"`
}

type installedRLEExtension struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	Source string `json:"source"`
}

func resolveInstalledUpdateSource() (extensionUpdateSource, error) {
	executable, err := os.Executable()
	if err != nil {
		return extensionUpdateSource{}, fmt.Errorf("locate running RLE executable: %w", err)
	}
	configPath, err := config.GetUserConfigFilePath()
	if err != nil {
		return extensionUpdateSource{}, fmt.Errorf("locate azd configuration: %w", err)
	}
	configuration, err := config.NewFileConfigManager(config.NewManager()).Load(configPath)
	if err != nil {
		return extensionUpdateSource{}, fmt.Errorf("load azd configuration for update checks: %w", err)
	}
	return resolveUpdateSource(configuration, filepath.Dir(configPath), executable)
}

func resolveUpdateSource(
	configuration config.Config, configDirectory string, executable string,
) (extensionUpdateSource, error) {
	var installed map[string]*installedRLEExtension
	if _, err := configuration.GetSection("extension.installed", &installed); err != nil {
		return extensionUpdateSource{}, fmt.Errorf("read installed extension configuration: %w", err)
	}
	var rle *installedRLEExtension
	for _, candidate := range installed {
		if candidate != nil && candidate.ID == "azure.ai.rle" {
			if rle != nil {
				return extensionUpdateSource{}, fmt.Errorf("multiple installed RLE records prevent source resolution")
			}
			rle = candidate
		}
	}
	if rle == nil {
		return extensionUpdateSource{}, fmt.Errorf("RLE is not installed in this azd profile; install from a registered source")
	}
	if rle.Path == "" || !updateSourceNamePattern.MatchString(rle.Source) {
		return extensionUpdateSource{}, fmt.Errorf("installed RLE path or source is invalid; reinstall from a registered source")
	}
	installedPath := rle.Path
	if !filepath.IsAbs(installedPath) {
		installedPath = filepath.Join(configDirectory, installedPath)
	}
	installedFile, err := os.Stat(installedPath)
	if err != nil {
		return extensionUpdateSource{}, fmt.Errorf("inspect installed RLE executable: %w", err)
	}
	runningFile, err := os.Stat(executable)
	if err != nil {
		return extensionUpdateSource{}, fmt.Errorf("inspect running RLE executable: %w", err)
	}
	if !os.SameFile(installedFile, runningFile) {
		return extensionUpdateSource{}, fmt.Errorf("running RLE binary is not this profile's installed extension")
	}
	if rle.Source == "bundle" {
		return extensionUpdateSource{}, fmt.Errorf("RLE bundle installation has no live registry for update checks")
	}

	var sources map[string]*extensionUpdateSource
	if _, err := configuration.GetSection("extension.sources", &sources); err != nil {
		return extensionUpdateSource{}, fmt.Errorf("read registered extension sources: %w", err)
	}
	var source *extensionUpdateSource
	for key, candidate := range sources {
		if candidate != nil && (strings.EqualFold(candidate.Name, rle.Source) ||
			(candidate.Name == "" && strings.EqualFold(key, rle.Source))) {
			if source != nil {
				return extensionUpdateSource{}, fmt.Errorf("multiple registered sources match RLE source %q", rle.Source)
			}
			source = candidate
		}
	}
	if source == nil {
		// Older azd versions stored dotted source names as nested configuration paths.
		var nested *extensionUpdateSource
		if _, err := configuration.GetSection("extension.sources."+strings.ToLower(rle.Source), &nested); err != nil {
			return extensionUpdateSource{}, fmt.Errorf("read RLE source %q: %w", rle.Source, err)
		}
		source = nested
	}
	if source == nil || source.Location == "" {
		return extensionUpdateSource{}, fmt.Errorf("RLE source %q is missing or has no registry location; register it again", rle.Source)
	}
	if source.Name != "" && !strings.EqualFold(source.Name, rle.Source) {
		return extensionUpdateSource{}, fmt.Errorf("registered source does not match RLE source %q", rle.Source)
	}
	location := source.Location
	switch source.Type {
	case "url":
		location, err := url.Parse(source.Location)
		if err != nil || location.Host == "" || location.User != nil ||
			(location.Scheme != "https" && location.Scheme != "http") {
			return extensionUpdateSource{}, fmt.Errorf("RLE source %q must have a valid HTTP or HTTPS registry URL", rle.Source)
		}
	case "file":
		if !filepath.IsAbs(location) {
			currentDirectory, err := os.Getwd()
			if err != nil {
				return extensionUpdateSource{}, fmt.Errorf("locate current directory for file registry: %w", err)
			}
			location, err = osutil.ResolveContainedPath([]string{currentDirectory, configDirectory}, location)
			if err != nil {
				return extensionUpdateSource{}, fmt.Errorf("resolve RLE source %q file registry: %w", rle.Source, err)
			}
		}
	default:
		return extensionUpdateSource{}, fmt.Errorf("RLE source %q has unsupported registry type %q", rle.Source, source.Type)
	}
	return extensionUpdateSource{Name: rle.Source, Type: source.Type, Location: location}, nil
}
