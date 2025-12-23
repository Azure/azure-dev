// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

func newGhUrlParseCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "gh-url-parse <github-url>",
		Short: "Parse a GitHub URL and extract repository information.",
		Long: `Parse a GitHub URL and extract repository information including hostname, 
repository slug, branch name, and file path. Supports various GitHub URL formats 
including blob, tree, raw, and API URLs. Handles branch names containing slashes.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Create a new context that includes the AZD access token
			ctx := azdext.WithAccessToken(cmd.Context())

			// Create a new AZD client
			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}

			defer azdClient.Close()

			// Get the GitHub URL from the first argument
			githubUrl := args[0]

			// Call the ParseGitHubUrl RPC method
			response, err := azdClient.Project().ParseGitHubUrl(ctx, &azdext.ParseGitHubUrlRequest{
				Url: githubUrl,
			})
			if err != nil {
				return fmt.Errorf("failed to parse GitHub URL: %w", err)
			}

			// Display the parsed URL information
			color.HiWhite("GitHub URL Information")
			fmt.Printf("Hostname:    %s\n", response.Hostname)
			fmt.Printf("Repository:  %s\n", response.RepoSlug)
			fmt.Printf("Branch:      %s\n", response.Branch)
			fmt.Printf("File Path:   %s\n", response.FilePath)

			return nil
		},
	}
}
