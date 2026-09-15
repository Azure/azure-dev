// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"net/url"
	"strings"
)

// SourceCategory is a privacy-safe classification of an extension source.
type SourceCategory string

const (
	SourceCategoryAzd     SourceCategory = "azd"
	SourceCategoryDev     SourceCategory = "dev"
	SourceCategoryNightly SourceCategory = "nightly"
	SourceCategoryLocal   SourceCategory = "local"
	SourceCategoryBundle  SourceCategory = "bundle"
	SourceCategoryOther   SourceCategory = "other"
	SourceCategoryUnknown SourceCategory = "unknown"

	extensionRegistryDevURL     = "https://aka.ms/azd/extensions/registry/dev"
	extensionRegistryNightlyURL = "https://aka.ms/azd/extensions/registry/nightly"

	extensionRegistryResolvedURL = "https://raw.githubusercontent.com/azure/azure-dev/" +
		"refs/heads/main/cli/azd/extensions/registry.json"
	extensionRegistryDirectURL = "https://raw.githubusercontent.com/azure/azure-dev/" +
		"main/cli/azd/extensions/registry.json"
	extensionRegistryDevResolvedURL = "https://raw.githubusercontent.com/azure/azure-dev/" +
		"refs/heads/main/cli/azd/extensions/registry.dev.json"
	extensionRegistryDevDirectURL = "https://raw.githubusercontent.com/azure/azure-dev/" +
		"main/cli/azd/extensions/registry.dev.json"
	extensionRegistryNightlyResolvedURL = "https://raw.githubusercontent.com/azure/azure-dev/" +
		"refs/heads/nightly/cli/azd/extensions/registry.nightly.json"
	extensionRegistryNightlyDirectURL = "https://raw.githubusercontent.com/azure/azure-dev/" +
		"nightly/cli/azd/extensions/registry.nightly.json"
)

var knownSourceLocations = map[string]SourceCategory{
	extensionRegistryUrl:                SourceCategoryAzd,
	extensionRegistryResolvedURL:        SourceCategoryAzd,
	extensionRegistryDirectURL:          SourceCategoryAzd,
	extensionRegistryDevURL:             SourceCategoryDev,
	extensionRegistryDevResolvedURL:     SourceCategoryDev,
	extensionRegistryDevDirectURL:       SourceCategoryDev,
	extensionRegistryNightlyURL:         SourceCategoryNightly,
	extensionRegistryNightlyResolvedURL: SourceCategoryNightly,
	extensionRegistryNightlyDirectURL:   SourceCategoryNightly,
}

// ClassifySource returns a fixed category derived from a source's type and location.
func ClassifySource(source *SourceConfig) SourceCategory {
	if source == nil {
		return SourceCategoryUnknown
	}

	switch source.Type {
	case SourceKindFile:
		return SourceCategoryLocal
	case SourceKindBundle:
		return SourceCategoryBundle
	case SourceKindUrl:
		if category, ok := knownSourceLocations[normalizeSourceLocation(source.Location)]; ok {
			return category
		}
		return SourceCategoryOther
	default:
		return SourceCategoryOther
	}
}

func normalizeSourceLocation(location string) string {
	parsed, err := url.Parse(location)
	if err != nil {
		return strings.ToLower(strings.TrimRight(location, "/"))
	}

	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Path = strings.ToLower(strings.TrimRight(parsed.Path, "/"))
	parsed.Fragment = ""
	return parsed.String()
}

func normalizeSourceCategory(category SourceCategory) SourceCategory {
	switch category {
	case SourceCategoryAzd,
		SourceCategoryDev,
		SourceCategoryNightly,
		SourceCategoryLocal,
		SourceCategoryBundle,
		SourceCategoryOther,
		SourceCategoryUnknown:
		return category
	default:
		return SourceCategoryUnknown
	}
}
