// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"io"
	"strings"

	"azureaiagent/internal/version"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/fatih/color"
)

// ASCII art using ANSI Shadow font for "FOUNDRY".
// Visual width is 61 columns; each box-drawing character is one display column
// but occupies multiple UTF-8 bytes, so len() over-counts. Tests use
// rune-aware width measurement.
const bannerArt = `███████╗ ██████╗ ██╗   ██╗███╗   ██╗██████╗ ██████╗ ██╗   ██╗
██╔════╝██╔═══██╗██║   ██║████╗  ██║██╔══██╗██╔══██╗╚██╗ ██╔╝
█████╗  ██║   ██║██║   ██║██╔██╗ ██║██║  ██║██████╔╝ ╚████╔╝ 
██╔══╝  ██║   ██║██║   ██║██║╚██╗██║██║  ██║██╔══██╗  ╚██╔╝  
██║     ╚██████╔╝╚██████╔╝██║ ╚████║██████╔╝██║  ██║   ██║   
╚═╝      ╚═════╝  ╚═════╝ ╚═╝  ╚═══╝╚═════╝ ╚═╝  ╚═╝   ╚═╝   
                                                             `

func printBanner(w io.Writer) {
	purple := color.RGB(109, 53, 255).Add(color.Bold)
	fmt.Fprintln(w)

	for line := range strings.SplitSeq(bannerArt, "\n") {
		purple.Fprintln(w, line) //nolint:gosec // G104 - banner output errors are non-critical
	}

	fmt.Fprint(w, output.WithGrayFormat("v%s", version.Version)) //nolint:gosec // G104 - banner output errors are non-critical
	fmt.Fprint(w, " ")
	fmt.Fprintln(w)
	fmt.Fprintln(w, output.WithGrayFormat("Visit the docs at https://aka.ms/azd-ai-agent-docs")) //nolint:gosec // G104 - banner output errors are non-critical
	fmt.Fprintln(w)
}

// printTagline writes the supplied tagline followed by a trailing blank
// line. Intended to be called immediately after printBanner so the
// extension's one-liner identity (the root command's Short) sits
// between the banner and whatever comes next (--help body, init
// pre-flow prompts, etc.). Whitespace is trimmed from the right edge
// of tagline so callers can pass cmd.Root().Short verbatim without
// worrying about trailing newlines.
//
// Empty (post-trim) tagline is a no-op.
func printTagline(w io.Writer, tagline string) {
	trimmed := strings.TrimRight(tagline, " \t\r\n")
	if trimmed == "" {
		return
	}
	fmt.Fprintln(w, trimmed)
	fmt.Fprintln(w)
}
