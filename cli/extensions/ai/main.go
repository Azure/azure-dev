package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/azure/azure-dev/cli/extensions/ai/internal/cmd"
	"github.com/azure/azure-dev/cli/sdk/azdcore/ext"
	"github.com/fatih/color"
)

func main() {
	// Execute the root command
	ctx := context.Background()
	rootCmd := cmd.NewRootCommand()

	if err := rootCmd.ExecuteContext(ctx); err != nil {
		var errWithSuggestion *ext.ErrorWithSuggestion

		if ok := errors.As(err, &errWithSuggestion); ok {
			color.Red("Error: %v", errWithSuggestion.Err)
			fmt.Printf("%s: %s\n", color.YellowString("Suggestion:"), errWithSuggestion.Suggestion)
		} else {
			color.Red("Error: %v", err)
		}

		os.Exit(1)
	}
}
