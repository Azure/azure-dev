// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package input

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/AlecAivazis/survey/v2"
)

const currentDirDisplayed = "./   [current directory]"

// PromptFs prompts the user for a filesystem path or directory
func (c *AskerConsole) PromptFs(ctx context.Context, options ConsoleOptions, fsOpts FsOptions) (string, error) {
	var response string

	err := c.doInteraction(func(c *AskerConsole) error {
		suggest := func(input string) []string {
			return fsSuggestions(
				fsOpts.SuggestOpts,
				fsOpts.Root,
				input)
		}
		prompt := &survey.Input{
			Message: options.Message,
			Help:    options.Help,
			Suggest: suggest,
		}
		err := c.asker(prompt, &response)
		if err != nil {
			return err
		}

		// translate the display sentinel value into the valid value
		if response == currentDirDisplayed {
			response = "." + string(filepath.Separator)
		}

		return nil
	})
	if err != nil {
		return response, err
	}
	c.updateLastBytes(afterIoSentinel)
	return response, nil
}

// FsOptions provides options for prompting a filesystem path or directory.
type FsOptions struct {
	// Root directory.
	Root string

	// Path suggestion options.
	SuggestOpts FsSuggestOptions
}

// FsSuggestOptions provides options for listing filesystem suggestions.
type FsSuggestOptions struct {
	// Exclude the current directory './' in suggestions. Only applicable if displaying directories.
	ExcludeCurrentDir bool

	// Include hidden files in suggestions.
	IncludeHiddenFiles bool

	// Exclude directories from suggestions.
	ExcludeDirectories bool

	// Exclude files from suggestions.
	ExcludeFiles bool
}

// fsSuggestions provides suggestion completions for files or directories given the current user input.
func fsSuggestions(
	options FsSuggestOptions,
	root string,
	input string) []string {
	fi, err := os.Stat(input)
	if err == nil && !fi.IsDir() { // we have found the file
		return []string{input}
	}

	// we have an input that is either a partial file/directory, or a directory:
	// suggest completions that help prefix-match the current input to the next closest file or directory
	completions := []string{}
	if input == "" && !options.ExcludeCurrentDir && !options.ExcludeDirectories {
		// include current directory in the completions
		completions = append(completions, currentDirDisplayed)
	}

	entry := input
	if len(root) > 0 {
		entry = filepath.Join(root, input)
	}

	matches, _ := filepath.Glob(entry + "*")
	for _, m := range matches {
		if !options.IncludeHiddenFiles && strings.HasPrefix(filepath.Base(m), ".") {
			continue
		}

		info, err := os.Stat(m)
		if err != nil {
			continue
		}

		if options.ExcludeDirectories && info.IsDir() {
			continue
		}

		if options.ExcludeFiles && !info.IsDir() {
			continue
		}

		name := m
		if info.IsDir() {
			// add trailing slash to directories
			name += string(os.PathSeparator)
		}

		completions = append(completions, name)
	}

	return completions
}
