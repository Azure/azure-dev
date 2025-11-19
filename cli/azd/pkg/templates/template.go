// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package templates

import (
	"io"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

type Template struct {
	Id string `json:"id"`

	// Name is the friendly short name of the template.
	Name string `json:"name"`

	Title string `json:"title,omitempty"`

	// The source of the template
	Source string `json:"-"`

	RepoSource string `json:"source,omitempty"`

	// Description is a long description of the template.
	Description string `json:"description,omitempty"`

	// RepositoryPath is a fully qualified URI to a git repository,
	// "{owner}/{repo}" for GitHub repositories,
	// or "{repo}" for GitHub repositories under Azure-Samples (default organization).
	RepositoryPath string `json:"repositoryPath"`

	// A list of tags associated with the template
	Tags []string `json:"tags"`

	// A list of languages supported by the template
	//
	// As of November 2025, known values include: bicep, php, javascript, dotnetCsharp, typescript, python, nodejs, java
	Languages []string `json:"languages,omitempty"`

	// Additional metadata about the template
	Metadata Metadata `json:"metadata,omitempty"`
}

// Metadata contains additional metadata about the template
// This metadata is used to modify azd project, environment config and environment variables during azd init commands.
type Metadata struct {
	Variables map[string]string `json:"variables,omitempty"`
	Config    map[string]string `json:"config,omitempty"`
	Project   map[string]string `json:"project,omitempty"`
}

// Display writes a string representation of the template suitable for display.
func (t *Template) Display(writer io.Writer) error {
	tabs := tabwriter.NewWriter(
		writer,
		0,
		output.TableTabSize,
		1,
		output.TablePadCharacter,
		output.TableFlags)
	text := [][]string{
		{"RepositoryPath", ":", Hyperlink(t.RepositoryPath)},
		{"Name", ":", t.Name},
		{"Source", ":", t.Source},
		{"Description", ":", t.Description},
		{"Tags", ":", strings.Join(t.Tags, ", ")},
	}

	for _, line := range text {
		_, err := tabs.Write([]byte(strings.Join(line, "\t") + "\n"))
		if err != nil {
			return err
		}
	}

	return tabs.Flush()
}

// DisplayLanguages returns a list of languages suitable for display from the associated template Tags.
func (t *Template) DisplayLanguages() []string {
	languages := make([]string, 0, len(t.Tags))
	for _, lang := range t.Tags {
		switch lang {
		case "dotnetCsharp":
			languages = append(languages, "csharp")
		case "nodejs":
			languages = append(languages, "nodejs")
		case "javascript":
			if !slices.Contains(t.Tags, "nodejs") && !slices.Contains(t.Tags, "ts") {
				languages = append(languages, "js")
			}
		case "typescript":
			if !slices.Contains(t.Tags, "nodejs") {
				languages = append(languages, "ts")
			}
		case "python", "java":
			languages = append(languages, lang)
		}
	}

	return languages
}

// CanonicalPath returns a canonicalized path for the template repository
func (t *Template) CanonicalPath() string {
	path := t.RepositoryPath
	if after, ok := strings.CutPrefix(path, "https://github.com/"); ok {
		path = after
	}

	if after, ok := strings.CutPrefix(strings.ToLower(path), "azure-samples/"); ok {
		path = after
	}

	return path
}
