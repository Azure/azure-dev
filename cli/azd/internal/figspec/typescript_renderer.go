// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package figspec

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
)

const (
	typescriptTemplate = `{{.Generators}}

const completionSpec: Fig.Spec = {
	name: '{{.Name}}',
	description: {{quote .Description}},
	subcommands: [
{{.Subcommands}}	],
	options: [
{{.Options}}	],
};

export default completionSpec;`

	// TypeScript field names used in rendering
	fieldName        = "name"
	fieldDescription = "description"
	fieldSubcommands = "subcommands"
	fieldOptions     = "options"
	fieldArgs        = "args"
	fieldHidden      = "hidden"
)

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
		"Name":        s.Name,
		"Description": s.Description,
		"Generators":  figGeneratorDefinitionsTS,
		"Subcommands": renderSubcommands(s.Subcommands, 2),
		"Options":     renderOptions(s.Options, 2, false),
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return buf.String(), nil
}

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

func renderNameField(names []string, indent string) string {
	if len(names) == 1 {
		return fmt.Sprintf("%s\t%s: ['%s'],", indent, fieldName, names[0])
	}

	quotedNames := make([]string, len(names))
	for i, n := range names {
		quotedNames[i] = "'" + n + "'"
	}
	return fmt.Sprintf("%s\t%s: [%s],", indent, fieldName, strings.Join(quotedNames, ", "))
}

func renderBoolField(fieldName string, value bool, indent string) string {
	if !value {
		return ""
	}
	return fmt.Sprintf("%s\t%s: true,", indent, fieldName)
}

func renderSubcommand(sub *Subcommand, indentLevel int) string {
	if sub == nil {
		return ""
	}

	indent := strings.Repeat("\t", indentLevel)
	var lines []string

	lines = append(lines, indent+"{")
	lines = append(lines, renderNameField(sub.Name, indent))
	lines = append(lines, fmt.Sprintf("%s\t%s: %s,", indent, fieldDescription, quoteString(sub.Description)))

	if line := renderBoolField(fieldHidden, sub.Hidden, indent); line != "" {
		lines = append(lines, line)
	}

	if len(sub.Subcommands) > 0 {
		lines = append(lines, fmt.Sprintf("%s\t%s: [", indent, fieldSubcommands))
		subContent := renderSubcommands(sub.Subcommands, indentLevel+2)
		lines = append(lines, strings.TrimSuffix(subContent, "\n"))
		lines = append(lines, fmt.Sprintf("%s\t],", indent))
	}

	if len(sub.Options) > 0 {
		lines = append(lines, fmt.Sprintf("%s\t%s: [", indent, fieldOptions))
		optContent := renderOptions(sub.Options, indentLevel+2, true)
		lines = append(lines, strings.TrimSuffix(optContent, "\n"))
		lines = append(lines, fmt.Sprintf("%s\t],", indent))
	}

	if len(sub.Args) > 0 {
		if len(sub.Args) == 1 {
			argsContent := renderArgs(sub.Args, indentLevel+1)
			trimmed := strings.TrimSuffix(strings.TrimSpace(argsContent), ",")
			lines = append(lines, fmt.Sprintf("%s\t%s: %s,", indent, fieldArgs, trimmed))
		} else {
			argsContent := renderArgs(sub.Args, indentLevel+2)
			lines = append(lines, fmt.Sprintf("%s\t%s: [", indent, fieldArgs))
			lines = append(lines, argsContent)
			lines = append(lines, fmt.Sprintf("%s\t],", indent))
		}
	}

	lines = append(lines, indent+"}")
	return strings.Join(lines, "\n")
}

func renderOptions(options []Option, indentLevel int, inSubcommand bool) string {
	if len(options) == 0 {
		return ""
	}

	var parts []string

	for _, opt := range options {
		if inSubcommand && opt.IsPersistent {
			continue // Persistent flags already defined at root
		}

		parts = append(parts, renderOption(&opt, indentLevel))
	}

	if len(parts) == 0 {
		return ""
	}

	return strings.Join(parts, ",\n") + ",\n"
}

func renderOption(opt *Option, indentLevel int) string {
	indent := strings.Repeat("\t", indentLevel)
	var lines []string

	lines = append(lines, indent+"{")
	lines = append(lines, renderNameField(opt.Name, indent))
	lines = append(lines, fmt.Sprintf("%s\t%s: %s,", indent, fieldDescription, quoteString(opt.Description)))

	if line := renderBoolField("isPersistent", opt.IsPersistent, indent); line != "" {
		lines = append(lines, line)
	}
	if line := renderBoolField("isRepeatable", opt.IsRepeatable, indent); line != "" {
		lines = append(lines, line)
	}
	if line := renderBoolField("isRequired", opt.IsRequired, indent); line != "" {
		lines = append(lines, line)
	}
	if line := renderBoolField("isDangerous", opt.IsDangerous, indent); line != "" {
		lines = append(lines, line)
	}
	if line := renderBoolField(fieldHidden, opt.Hidden, indent); line != "" {
		lines = append(lines, line)
	}

	if len(opt.Args) > 0 {
		lines = append(lines, fmt.Sprintf("%s\t%s: [", indent, fieldArgs))
		argsContent := renderArgs(opt.Args, indentLevel+2)
		lines = append(lines, argsContent)
		lines = append(lines, fmt.Sprintf("%s\t],", indent))
	}

	lines = append(lines, indent+"}")
	return strings.Join(lines, "\n")
}

func renderArgs(args []Arg, indentLevel int) string {
	if len(args) == 0 {
		return ""
	}

	indent := strings.Repeat("\t", indentLevel)
	var parts []string

	for _, arg := range args {
		var lines []string
		lines = append(lines, indent+"{")
		lines = append(lines, fmt.Sprintf("%s\tname: '%s',", indent, arg.Name))

		if arg.Description != "" {
			lines = append(lines, fmt.Sprintf("%s\tdescription: %s,", indent, quoteString(arg.Description)))
		}

		if arg.IsOptional {
			lines = append(lines, fmt.Sprintf("%s\tisOptional: true,", indent))
		}

		if len(arg.Suggestions) > 0 {
			suggestions := make([]string, len(arg.Suggestions))
			for i, s := range arg.Suggestions {
				suggestions[i] = "'" + s + "'"
			}

			if len(arg.Suggestions) <= 3 {
				lines = append(lines, fmt.Sprintf("%s\tsuggestions: [%s],", indent, strings.Join(suggestions, ", ")))
			} else {
				lines = append(lines, fmt.Sprintf("%s\tsuggestions: [", indent))
				for _, s := range suggestions {
					lines = append(lines, fmt.Sprintf("%s\t\t%s,", indent, s))
				}
				lines = append(lines, fmt.Sprintf("%s\t],", indent))
			}
		}

		if arg.Generator != "" {
			lines = append(lines, fmt.Sprintf("%s\tgenerators: %s,", indent, arg.Generator))
		}

		if arg.Template != "" {
			lines = append(lines, fmt.Sprintf("%s\ttemplate: '%s',", indent, arg.Template))
		}

		lines = append(lines, indent+"},")

		parts = append(parts, strings.Join(lines, "\n"))
	}

	return strings.Join(parts, "\n")
}

func quoteString(s string) string {
	s = escapeString(s)

	if !strings.Contains(s, "\n") {
		return "'" + s + "'"
	}

	return "`" + s + "`" // Template literals for multiline
}

func escapeString(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "'", "\\'")
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "\\r")
	s = strings.ReplaceAll(s, "\t", "\\t")
	return s
}

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
