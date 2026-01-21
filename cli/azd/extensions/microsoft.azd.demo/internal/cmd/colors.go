// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

func newColorsCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "colors",
		Aliases: []string{"colours"},
		Short:   "Displays all ASCII colors with their standard and high-intensity variants.",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Create a tab writer for proper column alignment
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w)
			fmt.Fprintln(w, "Standard          High Intensity")

			// Define color names and their corresponding fatih/color styles
			colors := []struct {
				name     string
				standard *color.Color
				hiColor  *color.Color
			}{
				{"Black", color.New(color.FgBlack), color.New(color.FgHiBlack)},
				{"Red", color.New(color.FgRed), color.New(color.FgHiRed)},
				{"Green", color.New(color.FgGreen), color.New(color.FgHiGreen)},
				{"Yellow", color.New(color.FgYellow), color.New(color.FgHiYellow)},
				{"Blue", color.New(color.FgBlue), color.New(color.FgHiBlue)},
				{"Magenta", color.New(color.FgMagenta), color.New(color.FgHiMagenta)},
				{"Cyan", color.New(color.FgCyan), color.New(color.FgHiCyan)},
				{"White", color.New(color.FgWhite), color.New(color.FgHiWhite)},
			}

			// Print each color row
			for _, c := range colors {
				standard := c.standard.Sprintf("██████ %s", c.name)
				hi := c.hiColor.Sprintf("██████ %s", c.name)
				fmt.Fprintf(w, "%s\t\t%s\n", standard, hi)
			}

			fmt.Fprintln(w)

			// Flush the writer
			w.Flush()

			return nil
		},
	}
}
