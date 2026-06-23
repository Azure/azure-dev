// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type sandboxFlags struct {
	sharedFlags
	version  string
	cpu      string
	memory   string
	disk     string
	wait     bool
	timeout  time.Duration
	interval time.Duration
}

func newSandboxCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sandbox <command>",
		Short: "Manage RLE environment sandboxes",
	}

	cmd.AddCommand(newSandboxCreateCommand())
	cmd.AddCommand(newSandboxListCommand())
	cmd.AddCommand(newSandboxShowCommand())
	return cmd
}

func newSandboxCreateCommand() *cobra.Command {
	flags := &sandboxFlags{
		sharedFlags: sharedFlags{
			account: defaultAccountName,
			project: defaultProjectName,
		},
	}

	cmd := &cobra.Command{
		Use:   "create <environment-id>",
		Short: "Create an RLE sandbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client := newRleClient(resolveControlPlaneEndpoint(flags.endpoint))
			request := sandboxCreateRequest{
				Version: flags.version,
				Cpu:     flags.cpu,
				Memory:  flags.memory,
				Disk:    flags.disk,
			}

			sandbox, err := createSandbox(cmd, client, flags, args[0], request)
			if err != nil {
				return sandboxCreateError(err)
			}

			if !isJsonOutput(cmd) {
				return printSandboxCreated(cmd, sandbox)
			}

			return printJson(cmd, sandbox)
		},
	}

	addSharedFlags(cmd, &flags.sharedFlags)
	cmd.Flags().StringVar(&flags.version, "version-label", "", "Environment version to sandbox. Defaults to latest")
	cmd.Flags().StringVar(&flags.cpu, "cpu", "", "Sandbox CPU request. Defaults to server configuration")
	cmd.Flags().StringVar(&flags.memory, "memory", "", "Sandbox memory request. Defaults to server configuration")
	cmd.Flags().StringVar(&flags.disk, "disk", "", "Sandbox disk request. Defaults to server configuration")
	cmd.Flags().BoolVar(&flags.wait, "wait", false, "Wait until disk image conversion is ready and sandbox creation succeeds")
	cmd.Flags().DurationVar(&flags.timeout, "wait-timeout", 30*time.Minute, "Maximum time to wait for sandbox creation")
	cmd.Flags().DurationVar(&flags.interval, "wait-interval", 30*time.Second, "Time to wait between sandbox creation attempts")
	return cmd
}

func newSandboxListCommand() *cobra.Command {
	flags := &sharedFlags{
		account: defaultAccountName,
		project: defaultProjectName,
	}

	cmd := &cobra.Command{
		Use:   "list <environment-id>",
		Short: "List RLE sandboxes for an environment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client := newRleClient(resolveControlPlaneEndpoint(flags.endpoint))
			sandboxes, err := client.listSandboxes(cmd.Context(), flags.account, flags.project, args[0])
			if err != nil {
				return serviceError(err)
			}

			return printJson(cmd, sandboxes)
		},
	}

	addSharedFlags(cmd, flags)
	return cmd
}

func newSandboxShowCommand() *cobra.Command {
	flags := &sharedFlags{
		account: defaultAccountName,
		project: defaultProjectName,
	}

	cmd := &cobra.Command{
		Use:   "show <environment-id> <sandbox-id>",
		Short: "Show an RLE sandbox",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client := newRleClient(resolveControlPlaneEndpoint(flags.endpoint))
			sandbox, err := client.getSandbox(cmd.Context(), flags.account, flags.project, args[0], args[1])
			if err != nil {
				return serviceError(err)
			}

			return printJson(cmd, sandbox)
		},
	}

	addSharedFlags(cmd, flags)
	return cmd
}

func createSandbox(
	cmd *cobra.Command,
	client *rleClient,
	flags *sandboxFlags,
	environmentId string,
	request sandboxCreateRequest,
) (*sandboxResource, error) {
	if !flags.wait {
		return client.createSandbox(cmd.Context(), flags.account, flags.project, environmentId, request)
	}
	if flags.timeout <= 0 {
		return nil, waitFlagError("wait-timeout")
	}
	if flags.interval <= 0 {
		return nil, waitFlagError("wait-interval")
	}

	return createSandboxWithWait(
		cmd.Context(),
		client,
		flags.account,
		flags.project,
		environmentId,
		request,
		flags.timeout,
		flags.interval,
		func(message string, interval time.Duration) {
			fmt.Fprintf(cmd.ErrOrStderr(), "%s Retrying in %s.\n", message, interval)
		},
	)
}

func waitFlagError(flagName string) error {
	return &azdext.LocalError{
		Message:  fmt.Sprintf("--%s must be greater than 0.", flagName),
		Code:     "rle_invalid_wait_option",
		Category: azdext.LocalErrorCategoryUser,
	}
}

func sandboxCreateError(err error) error {
	var localErr *azdext.LocalError
	if errors.As(err, &localErr) {
		return err
	}

	var httpErr *rleHTTPError
	if errors.As(err, &httpErr) && httpErr.statusCode == 409 {
		message := extractRleErrorMessage(httpErr.body)
		return &azdext.LocalError{
			Message:    message,
			Code:       "rle_sandbox_not_ready",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "The server creates the disk image asynchronously after environment creation. Retry sandbox create after conversion is ready; if the status is Failed, recreate/update the environment with a valid ACR image.",
		}
	}

	return serviceError(err)
}

func retryableSandboxCreateError(err error) (string, bool) {
	var httpErr *rleHTTPError
	if !errors.As(err, &httpErr) || httpErr.statusCode != 409 {
		return "", false
	}

	message := extractRleErrorMessage(httpErr.body)
	return message, strings.Contains(message, "conversion status: Pending") ||
		strings.Contains(message, "conversion status: NotRequested")
}

func createSandboxWithWait(
	ctx context.Context,
	client *rleClient,
	account string,
	project string,
	environmentId string,
	request sandboxCreateRequest,
	timeout time.Duration,
	interval time.Duration,
	onRetry func(message string, interval time.Duration),
) (*sandboxResource, error) {
	if timeout <= 0 {
		return nil, waitFlagError("wait-timeout")
	}
	if interval <= 0 {
		return nil, waitFlagError("wait-interval")
	}

	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var lastMessage string
	for {
		sandbox, err := client.createSandbox(waitCtx, account, project, environmentId, request)
		if err == nil {
			return sandbox, nil
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, sandboxCreateWaitTimeout(timeout, lastMessage)
		}

		message, retry := retryableSandboxCreateError(err)
		if !retry {
			return nil, err
		}
		lastMessage = message
		if onRetry != nil {
			onRetry(message, interval)
		}

		timer := time.NewTimer(interval)
		select {
		case <-waitCtx.Done():
			timer.Stop()
			return nil, sandboxCreateWaitTimeout(timeout, lastMessage)
		case <-timer.C:
		}
	}
}

func sandboxCreateWaitTimeout(timeout time.Duration, lastMessage string) error {
	message := fmt.Sprintf("Timed out after %s waiting for sandbox creation.", timeout)
	if lastMessage != "" {
		message = fmt.Sprintf("%s Last response: %s", message, lastMessage)
	}

	return &azdext.LocalError{
		Message:    message,
		Code:       "rle_sandbox_wait_timeout",
		Category:   azdext.LocalErrorCategoryUser,
		Suggestion: "Check the RLE control plane logs for disk image conversion status, then retry sandbox create.",
	}
}

func extractRleErrorMessage(body string) string {
	var payload struct {
		Error any `json:"error"`
	}

	if err := json.Unmarshal([]byte(body), &payload); err == nil && payload.Error != nil {
		switch value := payload.Error.(type) {
		case string:
			return value
		case map[string]any:
			if message, ok := value["message"].(string); ok && message != "" {
				return message
			}
		}
	}

	return strings.TrimSpace(body)
}

func printJson(cmd *cobra.Command, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), string(encoded))
	return err
}

func printSandboxCreated(cmd *cobra.Command, sandbox *sandboxResource) error {
	_, err := fmt.Fprintf(
		cmd.OutOrStdout(),
		"Sandbox created: %s\nStatus: %s\nURL: %s\n",
		sandbox.Id,
		sandbox.Status,
		sandbox.Url,
	)
	return err
}

func isJsonOutput(cmd *cobra.Command) bool {
	flag := cmd.Flag("output")
	return flag != nil && strings.EqualFold(flag.Value.String(), "json")
}
