// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import "github.com/azure/azure-dev/cli/azd/pkg/output"

type ActionOptions struct {
	// The desired console output format
	formatter output.Formatter
	// Whether or not the session supports interactive user input such as prompts
	interactive bool
}

// Gets a value determining whether the console is interactive
// Console is only considered interactive when the interactive
// flag has been set and an output format has not been defined.
func (options *ActionOptions) IsInteractive() bool {
	return options.interactive && options.Formatter().Kind() == output.NoneFormat
}

// Gets the specified output format
func (options *ActionOptions) Formatter() output.Formatter {
	if options.formatter == nil {
		options.formatter = &output.NoneFormatter{}
	}

	return options.formatter
}

// Infrastructure destroy options
type DestroyOptions struct {
	// Whether or not to force the deletion of resources without prompting the user
	force bool
	// Whether or not to purge any key vaults associated with the deployment
	purge bool
}

type StateOptions struct {
	// A value used to lookup the state of a specific deployment
	hint string
}

func NewStateOptions(hint string) *StateOptions {
	return &StateOptions{
		hint: hint,
	}
}

func (o *StateOptions) Hint() string {
	return o.hint
}

func (o *DestroyOptions) Purge() bool {
	return o.purge
}

func (o *DestroyOptions) Force() bool {
	return o.force
}

func NewDestroyOptions(force bool, purge bool) DestroyOptions {
	return DestroyOptions{
		force: force,
		purge: purge,
	}
}

func NewActionOptions(formatter output.Formatter, interactive bool) ActionOptions {
	return ActionOptions{
		formatter:   formatter,
		interactive: interactive,
	}
}
