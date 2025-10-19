// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package figspec

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
)

const typescriptTemplate = `{{.TypeDefinitions}}
{{.Generators}}

const completionSpec: Fig.Spec = {
	name: '{{.Name}}',
	description: {{quote .Description}},
	subcommands: [
{{.Subcommands}}	],
	options: [
{{.Options}}	],
};

export default completionSpec;
`

// ToTypeScript converts the spec to TypeScript code
func (s *Spec) ToTypeScript() (string, error) {
	tmpl, err := template.New("figspec").Funcs(template.FuncMap{
		"quote":        quoteString,
		"indent":       indentString,
		"join":         strings.Join,
		"escapeString": escapeString,
	}).Parse(typescriptTemplate)

	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	data := map[string]string{
		"Name":            s.Name,
		"Description":     s.Description,
		"TypeDefinitions": renderTypeDefinitions(),
		"Generators":      renderGenerators(),
		"Subcommands":     renderSubcommands(s.Subcommands, 2),
		"Options":         renderOptions(s.Options, 2, false),
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return buf.String(), nil
}

// renderTypeDefinitions returns the TypeScript type definitions
func renderTypeDefinitions() string {
	return `interface AzdEnvListItem {
	Name: string;
	DotEnvPath: string;
	HasLocal: boolean;
	HasRemote: boolean;
	IsDefault: boolean;
}

interface AzdShowResponse {
	name: string;
	services: Record<string, unknown>;
}

interface AzdTemplateListItem {
	name: string;
	description: string;
	repositoryPath: string;
	tags: string[];
}`
}

// renderGenerators returns the generator definitions
func renderGenerators() string {
	return `
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
	listServices: {
		script: ['azd', 'show', '--output', 'json'],
		postProcess: (out) => {
			try {
				const data: AzdShowResponse = JSON.parse(out);
				return Object.keys(data.services).map((serviceName) => ({
					name: serviceName,
				}));
			} catch {
				return [];
			}
		},
		cache: {
			cacheByDirectory: true,
			strategy: 'stale-while-revalidate',
		}
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
};`
}

// renderSubcommands renders an array of subcommands
func renderSubcommands(subcommands []Subcommand, indentLevel int) string {
	if len(subcommands) == 0 {
		return ""
	}

	var parts []string

	for _, sub := range subcommands {
		parts = append(parts, renderSubcommand(&sub, indentLevel))
	}

	return strings.Join(parts, ",\n") + ",\n"
}

// renderSubcommand renders a single subcommand
func renderSubcommand(sub *Subcommand, indentLevel int) string {
	if sub == nil {
		return ""
	}

	indent := strings.Repeat("\t", indentLevel)
	var lines []string

	lines = append(lines, indent+"{")

	// Name
	if len(sub.Name) == 1 {
		lines = append(lines, fmt.Sprintf("%s\tname: ['%s'],", indent, sub.Name[0]))
	} else {
		names := make([]string, len(sub.Name))
		for i, n := range sub.Name {
			names[i] = "'" + n + "'"
		}
		lines = append(lines, fmt.Sprintf("%s\tname: [%s],", indent, strings.Join(names, ", ")))
	}

	// Description
	lines = append(lines, fmt.Sprintf("%s\tdescription: %s,", indent, quoteString(sub.Description)))

	// Hidden
	if sub.Hidden {
		lines = append(lines, fmt.Sprintf("%s\thidden: true,", indent))
	}

	// Subcommands
	if len(sub.Subcommands) > 0 {
		lines = append(lines, fmt.Sprintf("%s\tsubcommands: [", indent))
		subContent := renderSubcommands(sub.Subcommands, indentLevel+2)
		lines = append(lines, strings.TrimSuffix(subContent, "\n"))
		lines = append(lines, fmt.Sprintf("%s\t],", indent))
	}

	// Options
	if len(sub.Options) > 0 {
		lines = append(lines, fmt.Sprintf("%s\toptions: [", indent))
		optContent := renderOptions(sub.Options, indentLevel+2, true)
		lines = append(lines, strings.TrimSuffix(optContent, "\n"))
		lines = append(lines, fmt.Sprintf("%s\t],", indent))
	}

	// Args
	if len(sub.Args) > 0 {
		argsContent := renderArgs(sub.Args, indentLevel+1)
		if len(sub.Args) == 1 {
			// For single arg, trim the trailing comma from argsContent
			trimmed := strings.TrimSuffix(strings.TrimSpace(argsContent), ",")
			lines = append(lines, fmt.Sprintf("%s\targs: %s,", indent, trimmed))
		} else {
			lines = append(lines, fmt.Sprintf("%s\targs: [", indent))
			lines = append(lines, argsContent)
			lines = append(lines, fmt.Sprintf("%s\t],", indent))
		}
	}

	lines = append(lines, indent+"}")

	return strings.Join(lines, "\n")
}

// renderOptions renders an array of options
func renderOptions(options []Option, indentLevel int, inSubcommand bool) string {
	if len(options) == 0 {
		return ""
	}

	var parts []string

	for _, opt := range options {
		// Skip persistent flags if we're in a subcommand (they're defined at root level)
		if inSubcommand && opt.IsPersistent {
			continue
		}

		parts = append(parts, renderOption(&opt, indentLevel))
	}

	if len(parts) == 0 {
		return ""
	}

	return strings.Join(parts, ",\n") + ",\n"
}

// renderOption renders a single option
func renderOption(opt *Option, indentLevel int) string {
	indent := strings.Repeat("\t", indentLevel)
	var lines []string

	lines = append(lines, indent+"{")

	// Name
	if len(opt.Name) == 1 {
		lines = append(lines, fmt.Sprintf("%s\tname: ['%s'],", indent, opt.Name[0]))
	} else {
		names := make([]string, len(opt.Name))
		for i, n := range opt.Name {
			names[i] = "'" + n + "'"
		}
		lines = append(lines, fmt.Sprintf("%s\tname: [%s],", indent, strings.Join(names, ", ")))
	}

	// Description
	lines = append(lines, fmt.Sprintf("%s\tdescription: %s,", indent, quoteString(opt.Description)))

	// Properties
	if opt.IsPersistent {
		lines = append(lines, fmt.Sprintf("%s\tisPersistent: true,", indent))
	}
	if opt.IsRepeatable {
		lines = append(lines, fmt.Sprintf("%s\tisRepeatable: true,", indent))
	}
	if opt.IsRequired {
		lines = append(lines, fmt.Sprintf("%s\tisRequired: true,", indent))
	}
	if opt.IsDangerous {
		lines = append(lines, fmt.Sprintf("%s\tisDangerous: true,", indent))
	}
	if opt.Hidden {
		lines = append(lines, fmt.Sprintf("%s\thidden: true,", indent))
	}

	// Args
	if len(opt.Args) > 0 {
		argsContent := renderArgs(opt.Args, indentLevel+1)
		if len(opt.Args) == 1 {
			lines = append(lines, fmt.Sprintf("%s\targs: [", indent))
			lines = append(lines, argsContent)
			lines = append(lines, fmt.Sprintf("%s\t],", indent))
		} else {
			lines = append(lines, fmt.Sprintf("%s\targs: [", indent))
			lines = append(lines, argsContent)
			lines = append(lines, fmt.Sprintf("%s\t],", indent))
		}
	}

	lines = append(lines, indent+"}")

	return strings.Join(lines, "\n")
}

// renderArgs renders an array of arguments
func renderArgs(args []Arg, indentLevel int) string {
	if len(args) == 0 {
		return ""
	}

	indent := strings.Repeat("\t", indentLevel)
	var parts []string

	for _, arg := range args {
		var lines []string
		lines = append(lines, indent+"{")

		// Name
		lines = append(lines, fmt.Sprintf("%s\tname: '%s',", indent, arg.Name))

		// Description
		if arg.Description != "" {
			lines = append(lines, fmt.Sprintf("%s\tdescription: %s,", indent, quoteString(arg.Description)))
		}

		// IsOptional
		if arg.IsOptional {
			lines = append(lines, fmt.Sprintf("%s\tisOptional: true,", indent))
		}

		// Suggestions
		if len(arg.Suggestions) > 0 {
			suggestions := make([]string, len(arg.Suggestions))
			for i, s := range arg.Suggestions {
				suggestions[i] = "'" + s + "'"
			}

			if len(arg.Suggestions) <= 3 {
				// Single line for short lists
				lines = append(lines, fmt.Sprintf("%s\tsuggestions: [%s],", indent, strings.Join(suggestions, ", ")))
			} else {
				// Multi-line for longer lists
				lines = append(lines, fmt.Sprintf("%s\tsuggestions: [", indent))
				for _, s := range suggestions {
					lines = append(lines, fmt.Sprintf("%s\t\t%s,", indent, s))
				}
				lines = append(lines, fmt.Sprintf("%s\t],", indent))
			}
		}

		// Generator
		if arg.Generator != "" {
			lines = append(lines, fmt.Sprintf("%s\tgenerators: %s,", indent, arg.Generator))
		}

		// Template
		if arg.Template != "" {
			lines = append(lines, fmt.Sprintf("%s\ttemplate: '%s',", indent, arg.Template))
		}

		lines = append(lines, indent+"},")

		parts = append(parts, strings.Join(lines, "\n"))
	}

	return strings.Join(parts, "\n")
}

// quoteString properly quotes a string for TypeScript, handling multiline strings
func quoteString(s string) string {
	// Escape special characters
	s = escapeString(s)

	// For simple strings, use single quotes
	if !strings.Contains(s, "\n") {
		return "'" + s + "'"
	}

	// For multiline strings, use backticks (template literals)
	return "`" + s + "`"
}

// escapeString escapes special characters in a string
func escapeString(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "'", "\\'")
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "\\r")
	s = strings.ReplaceAll(s, "\t", "\\t")
	return s
}

// indentString indents a string by the specified number of tabs
func indentString(s string, level int) string {
	indent := strings.Repeat("\t", level)
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = indent + line
		}
	}
	return strings.Join(lines, "\n")
}
