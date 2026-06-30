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

	// FigGenListConfigKeys generates suggestions from available azd config keys
	FigGenListConfigKeys = "azdGenerators.listConfigKeys"

	// FigGenFilepathsZip suggests local .zip files (self-contained extension bundles).
	// Unlike the built-in `filepaths` template (which suggests all files), this uses the
	// VS Code terminal-suggest `filepaths` helper restricted to the zip extension. The helper
	// is imported from '../helpers/filepaths' in the generated spec (see filepathsHelperImport).
	FigGenFilepathsZip = "filepaths({ extensions: ['zip'] })"

	// filepathsHelperImport is the import statement required by FigGenFilepathsZip. It is emitted
	// as a preamble in the generated spec so the VS Code terminal-suggest helper resolves correctly.
	filepathsHelperImport = "import { filepaths } from '../helpers/filepaths';"
)

//go:embed resources/generators.ts
var figGeneratorDefinitionsTS string
