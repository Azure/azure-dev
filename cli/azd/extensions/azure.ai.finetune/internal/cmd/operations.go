// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func newOperationCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "operation",
		Short: "Manage fine-tuning operations",
	}

	cmd.AddCommand(newOperationShowCommand())
	cmd.AddCommand(newOperationListCommand())
	cmd.AddCommand(newOperationCheckpointsCommand())

	return cmd
}

func newOperationShowCommand() *cobra.Command {
	var jobID string

	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show the fine tuning job details",
		RunE: func(cmd *cobra.Command, args []string) error {
			filePath := `D:\finetune\config-files\Job.txt`

			file, err := os.Open(filePath)
			if err != nil {
				return fmt.Errorf("failed to open job details file: %w", err)
			}
			defer file.Close()

			fmt.Println("\nFine-tuning Job Details:")
			fmt.Println(strings.Repeat("=", 80))

			scanner := bufio.NewScanner(file)
			for scanner.Scan() {
				line := scanner.Text()
				fmt.Println(line)
			}

			if err := scanner.Err(); err != nil {
				return fmt.Errorf("error reading job details file: %w", err)
			}

			fmt.Println(strings.Repeat("=", 80))

			if jobID != "" {
				fmt.Printf("\nJob ID: %s\n", jobID)
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&jobID, "job-id", "i", "", "Fine-tuning job ID")

	return cmd
}

func newOperationListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the fine tuning jobs",
		RunE: func(cmd *cobra.Command, args []string) error {
			filePath := `D:\finetune\config-files\jobs.txt`

			file, err := os.Open(filePath)
			if err != nil {
				return fmt.Errorf("failed to open jobs file: %w", err)
			}
			defer file.Close()

			fmt.Println("\nFine-tuning Jobs:")
			fmt.Println(strings.Repeat("-", 80))

			scanner := bufio.NewScanner(file)
			lineNum := 0
			for scanner.Scan() {
				line := scanner.Text()
				if strings.TrimSpace(line) != "" {
					lineNum++
					fmt.Printf("%d. %s\n", lineNum, line)
				}
			}

			if err := scanner.Err(); err != nil {
				return fmt.Errorf("error reading jobs file: %w", err)
			}

			fmt.Println(strings.Repeat("-", 80))
			fmt.Printf("\nTotal jobs: %d\n", lineNum)

			return nil
		},
	}
}

func newOperationCheckpointsCommand() *cobra.Command {
	var jobID string

	cmd := &cobra.Command{
		Use:   "checkpoints",
		Short: "Show fine tuning job checkpoints",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("\nFine-tuning Checkpoints:")
			fmt.Println(strings.Repeat("=", 100))

			checkpoints := []struct {
				ModelID      string
				Timestamp    string
				Step         int
				CheckpointID string
				FullName     string
			}{
				{
					ModelID:      "gpt-4o-2024-08-06.ft-4485dc4da8694d3b8c13c516baa18bc0",
					Timestamp:    "Dec 8, 2025 5:43 PM",
					Step:         100,
					CheckpointID: "ftchkpt-5e19eb50b09444009e7878ffcfc5dc32",
					FullName:     "gpt-4o-2024-08-06.ft-4485dc4da8694d3b8c13c516baa18bc0:ckpt-step-90",
				},
				{
					ModelID:      "gpt-4o-2024-08-06.ft-4485dc4da8694d3b8c13c516baa18bc0",
					Timestamp:    "Dec 8, 2025 5:43 PM",
					Step:         90,
					CheckpointID: "ftchkpt-4b12715397204e57bddeb9534387b913",
					FullName:     "gpt-4o-2024-08-06.ft-4485dc4da8694d3b8c13c516baa18bc0:ckpt-step-80",
				},
				{
					ModelID:      "gpt-4o-2024-08-06.ft-4485dc4da8694d3b8c13c516baa18bc0",
					Timestamp:    "Dec 8, 2025 5:42 PM",
					Step:         80,
					CheckpointID: "ftchkpt-a46e21965c054580848dc8ff636e6a3d",
					FullName:     "gpt-4o-2024-08-06.ft-4485dc4da8694d3b8c13c516baa18bc0:ckpt-step-1",
				},
				{
					ModelID:      "gpt-4o-2024-08-06.ft-4485dc4da8694d3b8c13c516baa18bc0",
					Timestamp:    "Dec 8, 2025 3:30 PM",
					Step:         1,
					CheckpointID: "ftchkpt-31cce4b265984d3482a9f980c4f2ebcf",
					FullName:     "",
				},
			}

			for i, cp := range checkpoints {
				fmt.Printf("\nCheckpoint #%d:\n", i+1)
				fmt.Printf("  Model ID:      %s\n", cp.ModelID)
				fmt.Printf("  Timestamp:     %s\n", cp.Timestamp)
				fmt.Printf("  Step:          %d\n", cp.Step)
				fmt.Printf("  Checkpoint ID: %s\n", cp.CheckpointID)
				if cp.FullName != "" {
					fmt.Printf("  Full Name:     %s\n", cp.FullName)
				}
				if i < len(checkpoints)-1 {
					fmt.Println(strings.Repeat("-", 100))
				}
			}

			fmt.Println(strings.Repeat("=", 100))
			fmt.Printf("\nTotal checkpoints: %d\n", len(checkpoints))

			if jobID != "" {
				fmt.Printf("Job ID: %s\n", jobID)
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&jobID, "job-id", "i", "", "Fine-tuning job ID")

	return cmd
}
