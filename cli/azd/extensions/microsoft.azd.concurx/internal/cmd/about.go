// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
)

func newAboutCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "about",
		Short: "Display information about concurX",
		RunE: func(cmd *cobra.Command, args []string) error {
			model := newAboutModel()
			p := tea.NewProgram(model, tea.WithAltScreen())

			if _, err := p.Run(); err != nil {
				return fmt.Errorf("failed to run about screen: %w", err)
			}

			return nil
		},
	}
}
