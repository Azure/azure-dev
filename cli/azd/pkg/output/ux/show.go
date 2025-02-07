// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"fmt"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/fatih/color"
)

type ShowService struct {
	Name      string
	IngresUrl string
	Env       map[string]string
}

func (s *ShowService) ToString(currentIndentation string) string {
	return fmt.Sprintf(
		"%s\n"+
			"  Endpoint: %s\n"+
			"  Environment variables:\n"+
			color.HiBlueString(formatEnv("    ", s.Env)),
		color.HiMagentaString("%s (Container App)", s.Name),
		output.WithLinkFormat(s.IngresUrl))
}

func (s *ShowService) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("not implemented")
}

func formatEnv(prefix string, values map[string]string) string {
	environ := make([]string, 0, len(values))
	for k, v := range values {
		environ = append(environ, k+"="+v)
	}
	slices.Sort(environ)

	var sb strings.Builder
	for _, env := range environ {
		sb.WriteString(prefix)
		sb.WriteString(env)
		sb.WriteString("\n")
	}

	return sb.String()
}

type ShowEnvironment struct {
	Name      string
	IsCurrent bool
	IsRemote  bool
}

type Show struct {
	AppName         string
	Services        []*ShowService
	Environments    []*ShowEnvironment
	AzurePortalLink string
}

func (s *Show) ToString(currentIndentation string) string {
	pickHeader := "\nShowing deployed endpoints and environments for apps in this directory.\n"
	if s.AzurePortalLink == "" {
		pickHeader = "\nShowing services and environments for apps in this directory.\n"
	}
	return fmt.Sprintf(
		"%s%s%s%s%s%s%s%s%s    %s\n",
		pickHeader,
		"To view a different environment, run ",
		color.HiBlueString("%s\n\n", "azd show -e <environment name>"),
		color.HiMagentaString(s.AppName),
		"\n  Services:\n",
		services(s.Services),
		"\n  Environments:\n",
		environments(s.Environments),
		"\n  View in Azure Portal:\n",
		azurePortalLink(s.AzurePortalLink),
	)
}

func azurePortalLink(link string) string {
	if link == "" {
		return fmt.Sprintf(
			"Application is not yet provisioned. Run %s or %s first.",
			color.HiBlueString("azd provision"),
			color.HiBlueString("azd up"),
		)
	}
	return output.WithLinkFormat(link)
}

func services(services []*ShowService) string {
	servicesCount := len(services)
	if servicesCount == 0 {
		return fmt.Sprintf(
			"    You don't have services defined. Add your services to %s.",
			color.HiBlueString("azure.yaml"),
		)
	}
	lines := make([]string, servicesCount)
	for index, service := range services {
		lines[index] = fmt.Sprintf(
			"    %s  %s",
			color.HiBlueString(service.Name),
			output.WithLinkFormat(service.IngresUrl),
		)
	}
	return strings.Join(lines, "\n")
}

func environments(environments []*ShowEnvironment) string {
	environmentsCount := len(environments)
	if environmentsCount == 0 {
		return fmt.Sprintf(
			"    You haven't created any environments. Run %s to create one.",
			color.HiBlueString("azd env new"),
		)
	}

	lines := make([]string, environmentsCount)
	for index, environment := range environments {
		var defaultEnv string
		if environment.IsCurrent {
			defaultEnv = " [Current]"
		}
		var isRemote string
		if environment.IsRemote {
			isRemote = " (Remote)"
		}
		lines[index] = fmt.Sprintf(
			"    %s%s%s",
			color.HiBlueString(environment.Name),
			defaultEnv,
			output.WithGrayFormat(isRemote),
		)
	}
	return strings.Join(lines, "\n")
}

func (s *Show) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("not implemented")
}
