package cmd

import (
	"fmt"
	"os"
	osexec "os/exec"

	"github.com/spf13/cobra"
)

// NewCreateCmd creates a new command for `azd create`
func NewCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create TypeScript templates",
		Long:  "Create TypeScript templates using the azd-create tool",
		RunE: func(cmd *cobra.Command, args []string) error {
			tst, _ := cmd.Flags().GetBool("tst")
			if !tst {
				fmt.Println("We're sorry. The create command is experimental and only support creating templates for the TypeScript provider, at this time.")
				fmt.Println("Do you want to run 'azd create --tst'?")
				return nil
			}

			// Check if azd-create exists in PATH
			_, err := osexec.LookPath("azd-create")
			if err != nil {
				return fmt.Errorf("unable to find 'azd-create' tool in PATH: %v\nPlease ensure the tool is installed and available in your PATH", err)
			}

			// Execute azd-create in the current directory
			fmt.Println("Starting azd-create tool...")
			createCmd := osexec.Command("azd-create")
			createCmd.Stdout = os.Stdout
			createCmd.Stderr = os.Stderr
			createCmd.Stdin = os.Stdin // Important: Connect stdin for interactive prompts
			createCmd.Dir = "."

			if err := createCmd.Run(); err != nil {
				if exitErr, ok := err.(*osexec.ExitError); ok {
					return fmt.Errorf("azd-create exited with error code %d", exitErr.ExitCode())
				}
				return fmt.Errorf("error running azd-create: %v", err)
			}

			return nil
		},
	}

	cmd.Flags().Bool("tst", false, "Create TypeScript templates")
	return cmd
}
