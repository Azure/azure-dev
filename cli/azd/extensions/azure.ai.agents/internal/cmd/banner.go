// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"io"
	"strings"

	"azureaiagent/internal/version"

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
	dim := color.New(color.Faint)
	fmt.Fprintln(w)

	for line := range strings.SplitSeq(bannerArt, "\n") {
		purple.Fprintln(w, line) //nolint:gosec // G104 - banner output errors are non-critical
	}

	dim.Fprintf(w, "v%s", version.Version) //nolint:gosec // G104 - banner output errors are non-critical
	fmt.Fprint(w, " ")
	fmt.Fprintln(w)
	dim.Fprintln(w, "Visit the docs at https://aka.ms/azd-ai-agent-docs") //nolint:gosec // G104 - banner output errors are non-critical
	fmt.Fprintln(w)
}
