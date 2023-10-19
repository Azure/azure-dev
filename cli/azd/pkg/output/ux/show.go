// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/fatih/color"
)

type ShowService struct {
	Name      string
	IngresUrl string
}

type ShowEnvironment struct {
	Name      string
	IsDefault bool
}

type Show struct {
	AppName         string
	Services        []*ShowService
	Environments    []*ShowEnvironment
	AzurePortalLink string
}

const (
	cHeader       = "\nShowing deployed endpoints and environments for apps in this directory.\n\n"
	cServices     = "\n  Services:\n"
	cEnvironments = "\n  Environments:\n"
	cDefault      = "[Default]"
	cViewInPortal = "\n  View in Azure Portal:\n"
)

func (s *Show) ToString(currentIndentation string) string {
	return fmt.Sprintf(
		"%s%s%s%s%s%s%s    %s\n",
		cHeader,
		color.MagentaString(s.AppName),
		cServices,
		services(s.Services),
		cEnvironments,
		environments(s.Environments),
		cViewInPortal,
		output.WithLinkFormat(s.AzurePortalLink),
	)
}

func services(services []*ShowService) string {
	lines := make([]string, len(services))
	for index, service := range services {
		lines[index] = fmt.Sprintf(
			"    %s  %s",
			color.BlueString(service.Name),
			output.WithLinkFormat(service.IngresUrl),
		)
	}
	return strings.Join(lines, "\n")
}

func environments(environments []*ShowEnvironment) string {
	lines := make([]string, len(environments))
	for index, environment := range environments {
		var defaultEnv string
		if environment.IsDefault {
			defaultEnv = cDefault
		}
		lines[index] = fmt.Sprintf(
			"    %s  %s",
			color.BlueString(environment.Name),
			output.WithGrayFormat(defaultEnv),
		)
	}
	return strings.Join(lines, "\n")
}

func (s *Show) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("not implemented")
}
