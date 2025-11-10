// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package figspec

import (
	"slices"
	"strings"
)

// azd-specific customizations for Fig spec generation
type Customizations struct{}

var _ CustomSuggestionProvider = (*Customizations)(nil)
var _ CustomGeneratorProvider = (*Customizations)(nil)
var _ CustomArgsProvider = (*Customizations)(nil)
var _ CustomFlagArgsProvider = (*Customizations)(nil)

var hookNameValues = []string{
	"prebuild", "postbuild",
	"predeploy", "postdeploy",
	"predown", "postdown",
	"prepackage", "postpackage",
	"preprovision", "postprovision",
	"prepublish", "postpublish",
	"prerestore", "postrestore",
	"preup", "postup",
}

var serviceCommandPaths = []string{
	"azd build",
	"azd deploy",
	"azd package",
	"azd publish",
	"azd restore",
}

// GetSuggestions returns static suggestion values for flags that accept a fixed set of options
func (c *Customizations) GetSuggestions(ctx *FlagContext) []string {
	path := ctx.CommandPath
	flagName := ctx.Flag.Name

	switch path {
	case "azd auth login":
		if flagName == "federated-credential-provider" {
			return []string{"github", "azure-pipelines", "oidc"}
		}

	case "azd pipeline config":
		switch flagName {
		case "provider":
			return []string{"github", "azdo"}
		case "auth-type":
			return []string{"federated", "client-credentials"}
		}
	}

	if strings.HasPrefix(path, "azd mcp consent") {
		switch flagName {
		case "action":
			return []string{"all", "readonly"}
		case "operation":
			return []string{"tool", "sampling"}
		case "permission":
			return []string{"allow", "deny", "prompt"}
		case "scope":
			return []string{"global", "project"}
		}
	}

	return nil
}

// GetCommandArgGenerator returns the Fig generator name for dynamically completing command arguments
func (c *Customizations) GetCommandArgGenerator(ctx *CommandContext, argName string) string {
	path := ctx.CommandPath

	switch path {
	case "azd env get-value":
		if argName == "keyName" {
			return FigGenListEnvironmentVariables
		}
	case "azd env select":
		if argName == "environment" {
			return FigGenListEnvironments
		}
	case "azd template show":
		if argName == "template" {
			return FigGenListTemplates
		}
	case "azd extension install":
		if argName == "extension-id" {
			return FigGenListExtensions
		}
	case "azd extension upgrade", "azd extension uninstall":
		if argName == "extension-id" {
			return FigGenListInstalledExtensions
		}
	}

	return ""
}

// GetFlagGenerator returns the Fig generator name for dynamically completing flag arguments
func (c *Customizations) GetFlagGenerator(ctx *FlagContext) string {
	flagName := ctx.Flag.Name
	path := ctx.CommandPath

	switch path {
	case "azd init":
		switch flagName {
		case "filter":
			return FigGenListTemplateTags
		case "template":
			return FigGenListTemplatesFiltered
		}

	case "azd template list":
		if flagName == "filter" {
			return FigGenListTemplateTags
		}
	}

	return ""
}

// GetCommandArgs returns custom argument specifications for commands with complex arg patterns
func (c *Customizations) GetCommandArgs(ctx *CommandContext) []Arg {
	switch ctx.CommandPath {
	case "azd env set":
		return []Arg{
			{Name: "key", IsOptional: true},
			{Name: "value", IsOptional: true},
		}
	case "azd hooks run":
		return []Arg{
			{Name: "name", Suggestions: hookNameValues},
		}
	}

	if slices.Contains(serviceCommandPaths, ctx.CommandPath) {
		return []Arg{
			{Name: "service", IsOptional: true},
		}
	}
	return nil
}

// GetFlagArgs returns custom argument names/descriptions for flags (e.g., "image-tag" instead of "from-package")
func (c *Customizations) GetFlagArgs(ctx *FlagContext) *Arg {
	flagName := ctx.Flag.Name
	path := ctx.CommandPath

	switch path {
	case "azd deploy":
		if flagName == "from-package" {
			return &Arg{
				Name: "file-path|image-tag",
			}
		}
	case "azd publish":
		switch flagName {
		case "from-package", "to":
			return &Arg{
				Name: "image-tag",
			}
		}
	}

	return nil
}
