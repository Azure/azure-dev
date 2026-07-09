// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"gopkg.in/yaml.v3"
)

// manifestProviderDoc is the minimal subset of an extension.yaml manifest needed to
// compare declared providers against what an extension registers at runtime.
type manifestProviderDoc struct {
	Providers []extensions.Provider `yaml:"providers"`
}

// manifestComparedProviderTypes is the set of provider types representable in a
// manifest's providers: list. Framework service and validation providers are
// registered in code only, so they are excluded.
var manifestComparedProviderTypes = []extensions.ProviderType{
	extensions.ServiceTargetProviderType,
	extensions.ProvisioningProviderType,
}

// VerifyProvidersMatchManifest asserts that the providers an extension registers via
// the supplied configure callback exactly match the providers declared in its
// extension.yaml manifest at manifestPath.
//
// It runs configure against a bare [ExtensionHost] (no azd connection; provider
// factories are never invoked) and compares the registered names against the
// manifest's providers: list. Only service-target and provisioning-provider types
// are compared; framework-service and validation registrations have no manifest
// representation.
//
// It returns a descriptive error when a provider is declared but not registered,
// registered but not declared, or listed more than once on either side.
func VerifyProvidersMatchManifest(configure func(host *ExtensionHost), manifestPath string) error {
	if configure == nil {
		return fmt.Errorf("configure callback must not be nil")
	}

	declared, err := loadManifestProviders(manifestPath)
	if err != nil {
		return err
	}

	host := NewExtensionHost(nil)
	configure(host)

	registered := map[extensions.ProviderType][]string{}
	for _, reg := range host.ServiceTargets() {
		registered[extensions.ServiceTargetProviderType] = append(
			registered[extensions.ServiceTargetProviderType], reg.Host)
	}
	for _, reg := range host.ProvisioningProviders() {
		registered[extensions.ProvisioningProviderType] = append(
			registered[extensions.ProvisioningProviderType], reg.Name)
	}

	var mismatches []string
	for _, providerType := range manifestComparedProviderTypes {
		declaredNames := declared[providerType]
		registeredNames := registered[providerType]

		for _, name := range duplicatedNames(declaredNames) {
			mismatches = append(mismatches, fmt.Sprintf(
				"provider %q of type %q is declared more than once in %s",
				name, providerType, manifestPath))
		}
		for _, name := range duplicatedNames(registeredNames) {
			mismatches = append(mismatches, fmt.Sprintf(
				"provider %q of type %q is registered more than once by the extension",
				name, providerType))
		}

		for _, name := range declaredNames {
			if !slices.Contains(registeredNames, name) {
				mismatches = append(mismatches, fmt.Sprintf(
					"provider %q of type %q is declared in %s but not registered by the extension",
					name, providerType, manifestPath))
			}
		}
		for _, name := range registeredNames {
			if !slices.Contains(declaredNames, name) {
				mismatches = append(mismatches, fmt.Sprintf(
					"provider %q of type %q is registered by the extension but not declared in %s",
					name, providerType, manifestPath))
			}
		}
	}

	if len(mismatches) > 0 {
		slices.Sort(mismatches)
		return fmt.Errorf("extension providers do not match manifest:\n  - %s",
			strings.Join(mismatches, "\n  - "))
	}

	return nil
}

// duplicatedNames returns, once each and sorted, the names that appear more than
// once in names. Matching is case-insensitive because differently cased provider
// names collide during registry discovery, but the first-seen spelling is reported.
func duplicatedNames(names []string) []string {
	counts := make(map[string]int, len(names))
	original := make(map[string]string, len(names))
	for _, name := range names {
		key := strings.ToLower(name)
		counts[key]++
		if _, ok := original[key]; !ok {
			original[key] = name
		}
	}

	var duplicated []string
	for key, count := range counts {
		if count > 1 {
			duplicated = append(duplicated, original[key])
		}
	}
	slices.Sort(duplicated)
	return duplicated
}

// loadManifestProviders reads and groups a manifest's declared providers by type,
// limited to the types that are comparable against runtime registrations.
func loadManifestProviders(manifestPath string) (map[extensions.ProviderType][]string, error) {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("reading manifest %s: %w", manifestPath, err)
	}

	var doc manifestProviderDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing manifest %s: %w", manifestPath, err)
	}

	declared := map[extensions.ProviderType][]string{}
	for _, provider := range doc.Providers {
		if slices.Contains(manifestComparedProviderTypes, provider.Type) {
			declared[provider.Type] = append(declared[provider.Type], provider.Name)
		}
	}
	return declared, nil
}
