package main

import (
	"context"
	"fmt"
	"os"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

type rootFlags struct {
	unit bool
}

func main() {
	flags := rootFlags{}

	rootCmd := &cobra.Command{
		Use:     "azd test [options]",
		Short:   "A tool to help test azd projects and services.",
		Example: "azd test <service> --unit",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				fmt.Print("Running tests for all services")
			} else {
				fmt.Printf("Running tests for %s service", args[0])
			}

			if flags.unit {
				fmt.Print(" (including unit tests)")
			}

			fmt.Print("\n")

			return nil
		},
	}

	rootCmd.Flags().BoolVar(&flags.unit, "unit", false, "Runs unit tests")

	ctx := context.Background()
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		color.Red("Error: %v", err)
		os.Exit(1)
	}
}
