// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package figspec

// Fig generator names used for dynamic autocomplete suggestions.
// These constants map to the TypeScript generator implementations in the rendered Fig spec.
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

// figGeneratorDefinitions returns the TypeScript code defining all Fig generators and interfaces used by them.
// The generator names in this code must match the constants defined above.
func figGeneratorDefinitions() string {
	return `interface AzdEnvListItem {
	Name: string;
	DotEnvPath: string;
	HasLocal: boolean;
	HasRemote: boolean;
	IsDefault: boolean;
}

interface AzdTemplateListItem {
	name: string;
	description: string;
	repositoryPath: string;
	tags: string[];
}

interface AzdExtensionListItem {
	id: string;
	name: string;
	namespace: string;
	version: string;
	installedVersion: string;
	source: string;
}

const azdGenerators: Record<string, Fig.Generator> = {
	listEnvironments: {
		script: ['azd', 'env', 'list', '--output', 'json'],
		postProcess: (out) => {
			try {
				const envs: AzdEnvListItem[] = JSON.parse(out);
				return envs.map((env) => ({
					name: env.Name,
					displayName: env.IsDefault ? 'Default' : undefined,
				}));
			} catch {
				return [];
			}
		},
	},
	listEnvironmentVariables: {
		script: ['azd', 'env', 'get-values', '--output', 'json'],
		postProcess: (out) => {
			try {
				const envVars: Record<string, string> = JSON.parse(out);
				return Object.keys(envVars).map((key) => ({
					name: key,
				}));
			} catch {
				return [];
			}
		},
	},
	listTemplates: {
		script: ['azd', 'template', 'list', '--output', 'json'],
		postProcess: (out) => {
			try {
				const templates: AzdTemplateListItem[] = JSON.parse(out);
				return templates.map((template) => ({
					name: template.repositoryPath,
					description: template.name,
				}));
			} catch {
				return [];
			}
		},
		cache: {
			strategy: 'stale-while-revalidate',
		}
	},
	listTemplateTags: {
		script: ['azd', 'template', 'list', '--output', 'json'],
		postProcess: (out) => {
			try {
				const templates: AzdTemplateListItem[] = JSON.parse(out);
				const tagsSet = new Set<string>();

				// Collect all unique tags from all templates
				templates.forEach((template) => {
					if (template.tags && Array.isArray(template.tags)) {
						template.tags.forEach((tag) => tagsSet.add(tag));
					}
				});

				// Convert set to array and return as suggestions
				return Array.from(tagsSet).sort().map((tag) => ({
					name: tag,
				}));
			} catch {
				return [];
			}
		},
		cache: {
			strategy: 'stale-while-revalidate',
		}
	},
	listTemplatesFiltered: {
		custom: async (tokens, executeCommand, generatorContext) => {
			// Find if there's a -f or --filter flag in the tokens
			let filterValue: string | undefined;
			for (let i = 0; i < tokens.length; i++) {
				if ((tokens[i] === '-f' || tokens[i] === '--filter') && i + 1 < tokens.length) {
					filterValue = tokens[i + 1];
					break;
				}
			}

			// Build the azd command with filter if present
			const args = ['template', 'list', '--output', 'json'];
			if (filterValue) {
				args.push('--filter', filterValue);
			}

			try {
				const { stdout } = await executeCommand({
					command: 'azd',
					args: args,
				});

				const templates: AzdTemplateListItem[] = JSON.parse(stdout);
				return templates.map((template) => ({
					name: template.repositoryPath,
					description: template.name,
				}));
			} catch {
				return [];
			}
		},
		cache: {
			strategy: 'stale-while-revalidate',
		}
	},
	listExtensions: {
		script: ['azd', 'ext', 'list', '--output', 'json'],
		postProcess: (out) => {
			try {
				const extensions: AzdExtensionListItem[] = JSON.parse(out);
				const uniqueExtensions = new Map<string, AzdExtensionListItem>();

				extensions.forEach((ext) => {
					if (!uniqueExtensions.has(ext.id)) {
						uniqueExtensions.set(ext.id, ext);
					}
				});

				return Array.from(uniqueExtensions.values()).map((ext) => ({
					name: ext.id,
					description: ext.name,
				}));
			} catch {
				return [];
			}
		},
		cache: {
			strategy: 'stale-while-revalidate',
		}
	},
	listInstalledExtensions: {
		script: ['azd', 'ext', 'list', '--installed', '--output', 'json'],
		postProcess: (out) => {
			try {
				const extensions: AzdExtensionListItem[] = JSON.parse(out);
				const uniqueExtensions = new Map<string, AzdExtensionListItem>();

				extensions.forEach((ext) => {
					if (!uniqueExtensions.has(ext.id)) {
						uniqueExtensions.set(ext.id, ext);
					}
				});

				return Array.from(uniqueExtensions.values()).map((ext) => ({
					name: ext.id,
					description: ext.name,
				}));
			} catch {
				return [];
			}
		},
	},
};`
}
