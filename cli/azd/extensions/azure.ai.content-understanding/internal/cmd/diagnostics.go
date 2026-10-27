// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"sync"

	"azure.ai.content-understanding/internal/config"
	"azure.ai.content-understanding/internal/exterrors"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/log"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// ValidateInvocation prevents Cobra's unknown-command errors from echoing argument values.
func ValidateInvocation(root *cobra.Command, args []string) error {
	if _, _, err := root.Find(args); err != nil {
		return invalidArguments()
	}
	return nil
}

func invalidArguments() error {
	return exterrors.FromError(commandError("invalid_command_arguments",
		"The command arguments or options are invalid.", "Check the command's --help and correct the arguments."))
}

func argumentError(err error) error {
	safe := exterrors.FromError(err)
	if local, ok := errors.AsType[*azdext.LocalError](safe); ok && local.Code == exterrors.CodeUnexpected {
		return invalidArguments()
	}
	return safe
}

func protectCommandErrors(command *cobra.Command) {
	command.SetFlagErrorFunc(func(*cobra.Command, error) error { return invalidArguments() })
	if validate := command.Args; validate != nil {
		command.Args = func(cmd *cobra.Command, args []string) error {
			return argumentError(validate(cmd, args))
		}
	}
	for _, callback := range []*func(*cobra.Command, []string) error{
		&command.PersistentPreRunE, &command.PreRunE,
	} {
		if run := *callback; run != nil {
			*callback = func(cmd *cobra.Command, args []string) error { return argumentError(run(cmd, args)) }
		}
	}
	if run := command.RunE; run != nil {
		command.RunE = func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Lookup("endpoint") != nil && cmd.Flags().Changed("environment") {
				environment, err := cmd.Flags().GetString("environment")
				if err != nil {
					return invalidArguments()
				}
				if environment == "" {
					return exterrors.FromError(commandError("empty_setting", "--environment must not be empty.",
						"Select an existing environment, or omit --environment for projectless use."))
				}
			}
			for flag, setting := range map[string]string{"endpoint": "CU_ENDPOINT", "api-version": "CU_API_VERSION"} {
				if cmd.Flags().Changed(flag) {
					value, err := cmd.Flags().GetString(flag)
					if err != nil {
						return invalidArguments()
					}
					if err := config.ValidateSetting(setting, value); err != nil {
						return exterrors.FromError(err)
					}
				}
			}
			return exterrors.FromError(run(cmd, args))
		}
	}
	for _, child := range command.Commands() {
		protectCommandErrors(child)
	}
}

// SDKLogListener retains event categories, not SDK messages containing customer URLs or headers.
// Install it once at process startup, before constructing SDK clients.
func SDKLogListener(root *cobra.Command) func(log.Event, string) {
	var mutex sync.Mutex
	return func(event log.Event, _ string) {
		mutex.Lock()
		defer mutex.Unlock()
		debug, err := root.PersistentFlags().GetBool("debug")
		if err != nil || !debug {
			return
		}
		message := "Azure SDK diagnostic (details suppressed)."
		switch event {
		case log.EventRequest:
			message = "Azure SDK request (details suppressed)."
		case log.EventResponse:
			message = "Azure SDK response (details suppressed)."
		case log.EventResponseError:
			message = "Azure SDK response error (details suppressed)."
		case log.EventRetryPolicy:
			message = "Azure SDK retry (details suppressed)."
		case log.EventLRO:
			message = "Azure SDK polling (details suppressed)."
		}
		logger := azdext.NewLogger("cu", azdext.LoggerOptions{Debug: true, Writer: root.ErrOrStderr()})
		logger.Debug(message)
	}
}
