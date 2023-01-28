// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	azd "github.com/azure/azure-dev/cli/azd/cmd"
	"github.com/spf13/cobra"
)

// fontMatterFormatString is the format string that generates
// the front matter text to prepend to the documentation we
// generate. This string is formatted with a single value, the
// current date in MM/DD/YY format.
const fontMatterFormatString = `---
title: Azure Developer CLI reference (preview)
description: This article explains the syntax and parameters for the various Azure Developer CLI Preview commands.
author: hhunter-ms
ms.author: hannahhunter
ms.date: %v
ms.service: azure-dev-cli
ms.topic: conceptual
ms.custom: devx-track-azdevcli
---

# Azure Developer CLI reference (preview)

This article explains the syntax and parameters for the various Azure Developer CLI Preview commands.

`

// directoryMode is the mode applied to output directory we create.
const directoryMode fs.FileMode = 0755

func main() {
	fmt.Println("Generating documentation")

	// staticHelp is true to inform commands to use generate help text instead
	// of generating help text that includes execution-specific state.
	cmd := azd.NewRootCmd(true, nil)

	basename := strings.Replace(cmd.CommandPath(), " ", "_", -1) + ".md"
	filename := filepath.Join("./md", basename)

	if err := os.MkdirAll(filepath.Dir(filename), directoryMode); err != nil {
		fmt.Printf("Error: %v", err)
		os.Exit(1)
	}

	docFile, err := os.Create(filename)
	if err != nil {
		fmt.Printf("Error: %v", err)
		os.Exit(1)
	}
	defer docFile.Close()

	// Write front-matter to the file:
	if _, err := docFile.WriteString(fmt.Sprintf(fontMatterFormatString, time.Now().Format("01/02/2006"))); err != nil {
		fmt.Printf("Error: %v", err)
		os.Exit(1)
	}

	if err := genMarkdownFile(docFile, cmd); err != nil {
		fmt.Printf("Error: %v", err)
		os.Exit(1)
	}

	fmt.Printf("Generated documentation to %v", filename)
}

// addCodeFences adds Markdown code fences (i.e. ```) to example commands listed in help
// text. An example command is a line which begins with a tab character and a dollar sign
// (which signifies the terminal prompt). Blocks of example commands are preceded and terminated
// by whitespace only lines.
func addCodeFencesToSampleCommands(s string) string {
	lines := strings.Split(s, "\n")
	newLines := []string{}

	inBlock := false
	for idx, line := range lines {
		// blank lines cause possible state changes...
		if strings.TrimSpace(line) == "" {
			if inBlock {
				inBlock = false
				newLines = append(newLines, "```")
				newLines = append(newLines, line)
			} else if !inBlock && idx+1 < len(lines) && strings.HasPrefix(lines[idx+1], "\t$") {
				inBlock = true
				newLines = append(newLines, line)
				newLines = append(newLines, "```azdeveloper")
			} else {
				newLines = append(newLines, line)
			}
		} else {
			if inBlock && strings.HasPrefix(line, "\t$") {
				line = formatCommandLine(line)
			}
			newLines = append(newLines, line)
		}
	}
	if inBlock {
		newLines = append(newLines, "```")
	}

	return strings.Join(newLines, "\n")
}

var precedingDollarRegexp = regexp.MustCompile(`^([\s]*)\$ (.*)$`)

func formatCommandLine(line string) string {
	return precedingDollarRegexp.ReplaceAllString(line, "$1$2")
}

// genMarkdownFile writes the help document for a single command (and all sub commands) to an
// io.Writer. It is similar to GenMarkdownTree from spf13/cobra/docs@v1.3.0 package, with some
// small tweaks based on the output we want for docs.microsoft.com. The changes we have made:
//
//   - Instead of emitting a file per command, we emit the help text for all commands into the
//     same unified writer (so they all appear in the same file)
//
//   - For a command with children, we emit the documentation for the parent command before
//     visiting any child commands. This ensures the parent help text is in the combined
//
//   - Since we are writing to a single file, we fix up the markdown links to refer to anchors
//     in the current file instead of separate files on disk.
func genMarkdownFile(w io.Writer, cmd *cobra.Command) error {
	// Cobra's GenMarkdownCustom provides a way to control the target of a link. Since
	// we put all commands into a single markdown file, we change the link target
	// from a markdown file to an anchor in the current file to the command.
	linkMapper := func(s string) string {
		commandName := strings.TrimSuffix(s, ".md")
		return "#" + strings.ReplaceAll(commandName, "_", "-")
	}

	if err := genMarkdownCustom(cmd, w, linkMapper); err != nil {
		return err
	}

	for _, c := range cmd.Commands() {
		// This ignore logic is the same as GenMarkdownTree
		if !c.IsAvailableCommand() || c.IsAdditionalHelpTopicCommand() {
			continue
		}

		if err := genMarkdownFile(w, c); err != nil {
			return err
		}
	}

	return nil
}

var linkRegexp = regexp.MustCompile(`(https://[^ ]*)`)

func convertLinksToMarkdown(s string) string {
	return linkRegexp.ReplaceAllStringFunc(s, func(link string) string {
		// assume a trailing period ends a sentence vs being part of the
		// url
		if strings.HasSuffix(link, ".") {
			link = link[:len(link)-1]
			return fmt.Sprintf("[%s](%s).", link, link)
		} else {
			return fmt.Sprintf("[%s](%s)", link, link)
		}
	})
}

// genMarkdownCustom is like `GetMarkdownCustom` from the spf13/cobra/docs@v1.3.0 package, with some
// small tweaks based on the output we want for docs.microsoft.com. The changes we have made:
//
//   - Don't include a link to the parent command in the "See also" section when the parent command
//     is itself the root command (since the logic below will add the link at the end of the list)
//
//   - Add a "Back to top" link at the end of every "See also" section that links back to the root
//     command.
//
// - We use addCodeFencesToSampleCommands to add code fences to the long help where needed.
//
// - We format URLs as markdown links (the text of the link is the URL).
func genMarkdownCustom(cmd *cobra.Command, w io.Writer, linkHandler func(string) string) error {
	cmd.InitDefaultHelpCmd()
	cmd.InitDefaultHelpFlag()

	buf := new(bytes.Buffer)
	name := cmd.CommandPath()

	buf.WriteString("## " + name + "\n\n")
	buf.WriteString(cmd.Short + "\n\n")
	if len(cmd.Long) > 0 {
		buf.WriteString("### Synopsis\n\n")
		buf.WriteString(convertLinksToMarkdown(addCodeFencesToSampleCommands(cmd.Long)) + "\n\n")
	}

	if cmd.Runnable() {
		buf.WriteString(fmt.Sprintf("```azdeveloper\n%s\n```\n\n", cmd.UseLine()))
	}

	if len(cmd.Example) > 0 {
		buf.WriteString("### Examples\n\n```azdeveloper\n")
		lines := strings.Split(cmd.Example, "\n")
		for _, line := range lines {
			buf.WriteString(formatCommandLine(line) + "\n")
		}
		buf.WriteString("```\n\n")
	}

	if err := printOptions(buf, cmd, name); err != nil {
		return err
	}
	if hasSeeAlso(cmd) {
		buf.WriteString("### See also\n\n")

		if cmd.HasParent() {
			parent := cmd.Parent()

			// Write a link to the parent, assuming that it is not the root command (we print a link to the root
			// command later, after all the child commands, with the text "Back to top")
			if parent != cmd.Root() {
				pname := parent.CommandPath()
				link := pname + ".md"
				link = strings.Replace(link, " ", "_", -1)
				buf.WriteString(fmt.Sprintf("* [%s](%s): %s\n", pname, linkHandler(link), parent.Short))
			}
			cmd.VisitParents(func(c *cobra.Command) {
				if c.DisableAutoGenTag {
					cmd.DisableAutoGenTag = c.DisableAutoGenTag
				}
			})
		}

		children := cmd.Commands()
		sort.Sort(byName(children))

		for _, child := range children {
			if !child.IsAvailableCommand() || child.IsAdditionalHelpTopicCommand() {
				continue
			}
			cname := name + " " + child.Name()
			link := cname + ".md"
			link = strings.Replace(link, " ", "_", -1)
			buf.WriteString(fmt.Sprintf("* [%s](%s): %s\n", cname, linkHandler(link), child.Short))
		}

		// for child commands, write a link back to the root command with the text "Back to top".
		if cmd.HasParent() {
			root := cmd.Root()
			cname := root.Name()
			link := cname + ".md"
			link = strings.Replace(link, " ", "_", -1)
			buf.WriteString(fmt.Sprintf("* [Back to top](%s)\n", linkHandler(link)))
		}

		buf.WriteString("\n")
	}
	if !cmd.DisableAutoGenTag {
		buf.WriteString("###### Auto generated by spf13/cobra on " + time.Now().Format("2-Jan-2006") + "\n")
	}
	_, err := buf.WriteTo(w)
	return err
}

// printOptions is the same as the un-exported helper from spf13/cobra/docs@v1.3.0
func printOptions(buf *bytes.Buffer, cmd *cobra.Command, name string) error {
	flags := cmd.NonInheritedFlags()
	flags.SetOutput(buf)
	if flags.HasAvailableFlags() {
		buf.WriteString("### Options\n\n```azdeveloper\n")
		flags.PrintDefaults()
		buf.WriteString("```\n\n")
	}

	parentFlags := cmd.InheritedFlags()
	parentFlags.SetOutput(buf)
	if parentFlags.HasAvailableFlags() {
		buf.WriteString("### Options inherited from parent commands\n\n```azdeveloper\n")
		parentFlags.PrintDefaults()
		buf.WriteString("```\n\n")
	}
	return nil
}

// hasSeeAlso is the same as the un-exported helper from spf13/cobra/docs@v1.3.0
func hasSeeAlso(cmd *cobra.Command) bool {
	if cmd.HasParent() {
		return true
	}
	for _, c := range cmd.Commands() {
		if !c.IsAvailableCommand() || c.IsAdditionalHelpTopicCommand() {
			continue
		}
		return true
	}
	return false
}

// byName is the same as the un-exported helper from spf13/cobra/docs@v1.3.0
type byName []*cobra.Command

func (s byName) Len() int           { return len(s) }
func (s byName) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
func (s byName) Less(i, j int) bool { return s[i].Name() < s[j].Name() }
