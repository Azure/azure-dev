// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package figspec

import _ "embed"

// Fig generator names used for dynamic autocomplete suggestions.
// These constants map to the TypeScript generator implementations defined in resources/figspec-generators.ts.
// The constant names should match the keys in the azdGenerators object (e.g., FigGenListEnvironments -> listEnvironments).
const (
	// FigGenListEnvironments generates suggestions from available azd environments
	FigGenListEnvironments = "azdGenerators.listEnvironments"

	// FigGenListEnvironmentVariables generates suggestions from environment variables
	FigGenListEnvironmentVariables = "azdGenerators.listEnvironmentVariables"

	// FigGenListTemplates generates suggestions from available azd templates
	FigGenListTemplates = "azdGenerators.listTemplates"

	// FigGenListTemplateTags generates suggestions from all available template tags
	FigGenListTemplateTags = "azdGenerators.listTemplateTags"

	// FigGenListTemplatesFiltered generates suggestions from templates filtered by --filter flag
	FigGenListTemplatesFiltered = "azdGenerators.listTemplatesFiltered"

	// FigGenListExtensions generates suggestions from all available extensions
	FigGenListExtensions = "azdGenerators.listExtensions"

	// FigGenListInstalledExtensions generates suggestions from installed extensions only
	FigGenListInstalledExtensions = "azdGenerators.listInstalledExtensions"
)

//go:embed resources/generators.ts
var figGeneratorDefinitionsTS string
