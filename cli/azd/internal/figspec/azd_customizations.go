// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package figspec

import (
	"slices"
	"strings"

	"github.com/spf13/pflag"
)

// AzdCustomizations provides azd-specific customizations for Fig spec generation
type AzdCustomizations struct{}

var _ CustomSuggestionProvider = (*AzdCustomizations)(nil)
var _ CustomGeneratorProvider = (*AzdCustomizations)(nil)
var _ CustomArgsProvider = (*AzdCustomizations)(nil)
var _ CustomFlagArgsProvider = (*AzdCustomizations)(nil)

// federated credential provider values extracted from description
var federatedProviderValues = []string{"github", "azure-pipelines", "oidc"}

// pipeline provider values
var pipelineProviderValues = []string{"github", "azdo"}

// pipeline auth type values
var pipelineAuthTypeValues = []string{"federated", "client-credentials"}

// mcp consent values
var mcpActionValues = []string{"all", "readonly"}
var mcpOperationValues = []string{"tool", "sampling"}
var mcpPermissionValues = []string{"allow", "deny", "prompt"}
var mcpScopeValues = []string{"global", "project"}

// hook names for azd hooks run
var hookNameValues = []string{
	"prebuild",
	"postbuild",
	"predeploy",
	"postdeploy",
	"predown",
	"postdown",
	"prepackage",
	"postpackage",
	"preprovision",
	"postprovision",
	"prepublish",
	"postpublish",
	"prerestore",
	"postrestore",
	"preup",
	"postup",
}

// Service commands
var serviceCommands = []string{
	"build",
	"deploy",
	"package",
	"publish",
	"restore",
}

// GetSuggestions returns custom suggestions for specific flags that mention values in descriptions
func (c *AzdCustomizations) GetSuggestions(ctx *FlagContext) []string {
	flagName := ctx.Flag.Name

	// Handle federated-credential-provider
	if flagName == "federated-credential-provider" {
		return federatedProviderValues
	}

	// Handle pipeline provider
	if flagName == "provider" && strings.Contains(ctx.CommandPath, "pipeline config") {
		return pipelineProviderValues
	}

	// Handle pipeline auth-type
	if flagName == "auth-type" && strings.Contains(ctx.CommandPath, "pipeline config") {
		return pipelineAuthTypeValues
	}

	// Handle MCP consent flags
	if strings.Contains(ctx.CommandPath, "mcp consent") {
		switch flagName {
		case "action":
			return mcpActionValues
		case "operation":
			return mcpOperationValues
		case "permission":
			return mcpPermissionValues
		case "scope":
			return mcpScopeValues
		}
	}

	return nil
}

// GetCommandArgGenerator returns custom generator names for specific command arguments
func (c *AzdCustomizations) GetCommandArgGenerator(ctx *CommandContext, argName string) string {
	path := ctx.CommandPath
	cmdName := ctx.Command.Name()

	// Service argument generator for deploy, package, publish, restore, build
	if slices.Contains(serviceCommands, cmdName) && argName == "service" {
		return "azdGenerators.listServices"
	}

	// Environment generators
	switch path {
	case "azd env get-value":
		if argName == "keyName" {
			return "azdGenerators.listEnvironmentVariables"
		}
	case "azd env select":
		if argName == "environment" {
			return "azdGenerators.listEnvironments"
		}
	}

	// Template generators
	if path == "azd template show" && argName == "template" {
		return "azdGenerators.listTemplates"
	}

	return ""
}

// GetFlagGenerator returns custom generator names for specific flag arguments
func (c *AzdCustomizations) GetFlagGenerator(ctx *FlagContext) string {
	flagName := ctx.Flag.Name
	path := ctx.CommandPath

	// Template filter flag
	if flagName == "filter" && strings.Contains(path, "init") {
		return "azdGenerators.listTemplateTags"
	}

	// Template list with dynamic filtering
	if flagName == "template" && strings.Contains(path, "init") {
		return "azdGenerators.listTemplatesFiltered"
	}

	// Template filter in template list command
	if flagName == "filter" && strings.Contains(path, "template list") {
		return "azdGenerators.listTemplateTags"
	}

	return ""
}

// GetCommandArgs returns custom argument specifications for specific commands
func (c *AzdCustomizations) GetCommandArgs(ctx *CommandContext) []Arg {
	path := ctx.CommandPath
	cmdName := ctx.Command.Name()

	// Handle "azd env set" - simplified args instead of complex Use string parsing
	if path == "azd env set" {
		return []Arg{
			{
				Name:       "key",
				IsOptional: true,
			},
			{
				Name:       "value",
				IsOptional: true,
			},
		}
	}

	// Handle "azd show" - optional resource argument
	if path == "azd show" {
		return []Arg{
			{
				Name:       "resource-name|resource-id",
				IsOptional: true,
			},
		}
	}

	// Handle "azd hooks run" - hook name argument with suggestions
	if path == "azd hooks run" {
		return []Arg{
			{
				Name:        "name",
				Suggestions: hookNameValues,
			},
		}
	}

	// Handle service commands - service argument is optional
	if slices.Contains(serviceCommands, cmdName) {
		return []Arg{
			{
				Name:       "service",
				IsOptional: true,
			},
		}
	}

	// Handle extension commands - extension-name argument is optional
	if path == "azd extension uninstall" || path == "azd extension upgrade"{
		return []Arg{
			{
				Name:       "extension-name",
				IsOptional: true,
			},
		}
	}

	return nil
}

// GetFlagArgs returns custom argument specifications for specific flags
func (c *AzdCustomizations) GetFlagArgs(ctx *FlagContext) *Arg {
	flagName := ctx.Flag.Name
	path := ctx.CommandPath

	// Handle "azd publish --from-package"
	if flagName == "from-package" && strings.Contains(path, "publish") {
		return &Arg{
			Name:        "image-tag",
		}
	}

	// Handle "azd publish --to"
	if flagName == "to" && strings.Contains(path, "publish") {
		return &Arg{
			Name:        "image-tag",
		}
	}

	// Handle "azd deploy --from-package"
	if flagName == "from-package" && strings.Contains(path, "deploy") {
		return &Arg{
			Name:        "file-path|image-tag",
		}
	}

	return nil
}

// ShouldSkipPersistentFlag returns true if the flag should not be included at the command level
// (it will be included at the root level instead)
func ShouldSkipPersistentFlag(flag *pflag.Flag) bool {
	// These flags should only appear at the root level as persistent
	persistentOnlyFlags := map[string]bool{
		"help":      true,
		"debug":     true,
		"cwd":       true,
		"no-prompt": true,
		"docs":      true,
	}

	return persistentOnlyFlags[flag.Name]
}
