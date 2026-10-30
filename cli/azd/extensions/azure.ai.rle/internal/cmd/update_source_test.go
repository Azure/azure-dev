// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
)

func updateSourceFixture(t *testing.T, name string) (config.Config, string, string) {
	t.Helper()
	directory := t.TempDir()
	executable := filepath.Join(directory, "rle.exe")
	if err := os.WriteFile(executable, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	configuration := config.NewConfig(nil)
	setUpdateConfig(t, configuration, "extension.installed", map[string]installedRLEExtension{
		"azure-ai-rle": {ID: "azure.ai.rle", Path: "rle.exe", Source: name},
	})
	setUpdateConfig(t, configuration, "extension.sources", map[string]extensionUpdateSource{
		strings.ToLower(name): {Name: name, Type: "url", Location: "https://example.test/registry.json"},
	})
	return configuration, directory, executable
}

func setUpdateConfig(t *testing.T, configuration config.Config, key string, value any) {
	t.Helper()
	if err := configuration.Set(key, value); err != nil {
		t.Fatal(err)
	}
}

func TestResolveUpdateSourceUsesInstalledSource(t *testing.T) {
	for _, name := range []string{"rle", "rle-dev", "Team.Preview", "custom_123"} {
		t.Run(name, func(t *testing.T) {
			configuration, directory, executable := updateSourceFixture(t, name)
			source, err := resolveUpdateSource(configuration, directory, executable)
			if err != nil {
				t.Fatal(err)
			}
			if source.Name != name || source.Type != "url" || source.Location != "https://example.test/registry.json" {
				t.Fatalf("unexpected source: %#v", source)
			}
		})
	}
}

func TestResolveUpdateSourceSupportsLegacyDottedSource(t *testing.T) {
	configuration, directory, executable := updateSourceFixture(t, "Team.Preview")
	setUpdateConfig(t, configuration, "extension.sources", map[string]any{})
	setUpdateConfig(t, configuration, "extension.sources.team.preview", extensionUpdateSource{
		Name: "Team.Preview", Type: "url", Location: "https://example.test/registry.json",
	})
	source, err := resolveUpdateSource(configuration, directory, executable)
	if err != nil || source.Name != "Team.Preview" {
		t.Fatalf("expected legacy dotted source, got %#v, %v", source, err)
	}
}

func TestResolveUpdateSourceRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value any
		error string
	}{
		{"not installed", "extension.installed", map[string]any{}, "not installed"},
		{"malformed installed", "extension.installed", "invalid", "read installed"},
		{"null record", "extension.installed", map[string]any{"rle": nil}, "not installed"},
		{"missing path", "extension.installed", map[string]installedRLEExtension{
			"rle": {ID: "azure.ai.rle", Source: "preview"},
		}, "path or source is invalid"},
		{"missing executable", "extension.installed", map[string]installedRLEExtension{
			"rle": {ID: "azure.ai.rle", Source: "preview", Path: "missing.exe"},
		}, "inspect installed"},
		{"unsafe alias", "extension.installed", map[string]installedRLEExtension{
			"rle": {ID: "azure.ai.rle", Source: "preview;command", Path: "rle.exe"},
		}, "path or source is invalid"},
		{"bundle installation", "extension.installed", map[string]installedRLEExtension{
			"rle": {ID: "azure.ai.rle", Source: "bundle", Path: "rle.exe"},
		}, "no live registry"},
		{"duplicate installation", "extension.installed", map[string]installedRLEExtension{
			"one": {ID: "azure.ai.rle", Source: "preview", Path: "rle.exe"},
			"two": {ID: "azure.ai.rle", Source: "other", Path: "rle.exe"},
		}, "multiple installed"},
		{"removed source", "extension.sources", map[string]any{}, "missing or has no registry"},
		{"malformed sources", "extension.sources", "invalid", "read registered"},
		{"no location", "extension.sources", map[string]extensionUpdateSource{
			"preview": {Name: "preview", Type: "url"},
		}, "missing or has no registry"},
		{"invalid URL", "extension.sources", map[string]extensionUpdateSource{
			"preview": {Name: "preview", Type: "url", Location: "://invalid"},
		}, "valid HTTP or HTTPS"},
		{"URL credentials", "extension.sources", map[string]extensionUpdateSource{
			"preview": {Name: "preview", Type: "url", Location: (&url.URL{
				Scheme: "https", Host: "example.test", User: url.UserPassword("fixture-user", "fixture-password"),
			}).String()},
		}, "valid HTTP or HTTPS"},
		{"unsupported source", "extension.sources", map[string]extensionUpdateSource{
			"preview": {Name: "preview", Type: "bundle", Location: "bundle"},
		}, "unsupported registry type"},
		{"duplicate sources", "extension.sources", map[string]extensionUpdateSource{
			"one": {Name: "preview", Type: "url", Location: "https://example.test/one"},
			"two": {Name: "PREVIEW", Type: "url", Location: "https://example.test/two"},
		}, "multiple registered"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configuration, directory, executable := updateSourceFixture(t, "preview")
			setUpdateConfig(t, configuration, tt.key, tt.value)
			_, err := resolveUpdateSource(configuration, directory, executable)
			if err == nil || !strings.Contains(err.Error(), tt.error) {
				t.Fatalf("expected %q, got %v", tt.error, err)
			}
			if strings.Contains(err.Error(), "fixture-password") {
				t.Fatalf("error disclosed URL credentials: %v", err)
			}
		})
	}
}

func TestResolveUpdateSourceDoesNotBorrowAnotherBinarySource(t *testing.T) {
	configuration, directory, _ := updateSourceFixture(t, "rle")
	other := filepath.Join(directory, "local-build.exe")
	if err := os.WriteFile(other, []byte("other"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := resolveUpdateSource(configuration, directory, other)
	if err == nil || !strings.Contains(err.Error(), "not this profile's installed extension") {
		t.Fatalf("expected executable identity error, got %v", err)
	}
}

func TestUpdateCheckerUsesActiveAzdProfile(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"rle-dev", "customer-preview"} {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			t.Setenv("AZD_CONFIG_DIR", directory)
			registry := newRegistryServer(t, `{"extensions":[{"id":"azure.ai.rle","versions":[
				{"version":"0.8.5-preview"},{"version":"0.8.6-preview","breakingChanges":true}]}]}`)
			configuration := config.NewConfig(nil)
			setUpdateConfig(t, configuration, "extension.installed", map[string]installedRLEExtension{
				"rle": {ID: "azure.ai.rle", Path: executable, Source: name},
			})
			setUpdateConfig(t, configuration, "extension.sources", map[string]extensionUpdateSource{
				name: {Name: name, Type: "url", Location: registry.URL},
			})
			manager := config.NewFileConfigManager(config.NewManager())
			if err := manager.Save(configuration, filepath.Join(directory, "config.json")); err != nil {
				t.Fatal(err)
			}
			update, err := newRegistryExtensionUpdateChecker().Check(t.Context(), "0.8.5-preview")
			if err != nil {
				t.Fatal(err)
			}
			if update == nil || update.Source != name || !update.IsBreaking {
				t.Fatalf("expected update from active profile %q, got %#v", name, update)
			}
		})
	}
}

func TestUpdateCheckerReadsFileRegistry(t *testing.T) {
	configuration, directory, executable := updateSourceFixture(t, "local-preview")
	registry := filepath.Join(directory, "registry.json")
	if err := os.WriteFile(registry, []byte(`{"extensions":[{"id":"azure.ai.rle","versions":[
		{"version":"0.8.6-preview"}]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	setUpdateConfig(t, configuration, "extension.sources", map[string]extensionUpdateSource{
		"local-preview": {Name: "local-preview", Type: "file", Location: registry},
	})
	checker := &registryExtensionUpdateChecker{
		resolveSource: func() (extensionUpdateSource, error) {
			return resolveUpdateSource(configuration, directory, executable)
		},
	}
	update, err := checker.Check(t.Context(), "0.8.5-preview")
	if err != nil || update == nil || update.Source != "local-preview" || update.IsBreaking {
		t.Fatalf("expected non-breaking file registry update, got %#v, %v", update, err)
	}
}

func TestResolveUpdateSourceSupportsRelativeFileRegistry(t *testing.T) {
	configuration, directory, executable := updateSourceFixture(t, "local-preview")
	registry := filepath.Join(directory, "registry.json")
	if err := os.WriteFile(registry, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	setUpdateConfig(t, configuration, "extension.sources", map[string]extensionUpdateSource{
		"local-preview": {Name: "local-preview", Type: "file", Location: "registry.json"},
	})
	source, err := resolveUpdateSource(configuration, directory, executable)
	if err != nil || source.Location != registry {
		t.Fatalf("expected profile-relative file registry, got %#v, %v", source, err)
	}
}

func TestUpdateCheckerDoesNotDiscloseRegistryQuery(t *testing.T) {
	registry := newRegistryServer(t, `{}`)
	registry.Close()
	checker := &registryExtensionUpdateChecker{
		client: http.DefaultClient,
		resolveSource: func() (extensionUpdateSource, error) {
			return extensionUpdateSource{Name: "preview", Type: "url", Location: registry.URL + "?sig=private-value"}, nil
		},
	}
	_, err := checker.Check(t.Context(), "0.8.5-preview")
	if err == nil || strings.Contains(err.Error(), "private-value") {
		t.Fatalf("expected sanitized registry request failure, got %v", err)
	}
}
