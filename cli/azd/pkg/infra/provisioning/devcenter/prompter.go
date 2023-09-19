package devcenter

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/devcentersdk"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"golang.org/x/exp/slices"
)

type Prompter struct {
	console         input.Console
	devCenterClient devcentersdk.DevCenterClient
}

func NewPrompter(console input.Console, devCenterClient devcentersdk.DevCenterClient) *Prompter {
	return &Prompter{
		console:         console,
		devCenterClient: devCenterClient,
	}
}

func (p *Prompter) PromptDevCenter(ctx context.Context) (string, error) {
	devCenters := []*devcentersdk.DevCenter{}
	writeableProjects, err := p.devCenterClient.WritableProjects(ctx)
	if err != nil {
		return "", err
	}

	for _, project := range writeableProjects {
		containsDevCenter := slices.ContainsFunc(devCenters, func(dc *devcentersdk.DevCenter) bool {
			return dc.ServiceUri == project.DevCenter.ServiceUri
		})

		if containsDevCenter {
			devCenters = append(devCenters, project.DevCenter)
		}
	}

	slices.SortFunc(devCenters, func(x, y *devcentersdk.DevCenter) bool {
		return x.Name < y.Name
	})

	devCenterNames := []string{}
	for _, devCenter := range devCenters {
		devCenterNames = append(devCenterNames, devCenter.Name)
	}

	selected, err := p.console.Prompt(ctx, input.ConsoleOptions{
		Message: "Select a Dev Center:",
		Options: devCenterNames,
	})
}

func (p *Prompter) PromptProject(ctx context.Context) (string, error) {

}

func (p *Prompter) PromptEnvironmentType(ctx context.Context) (string, error) {

}

func (p *Prompter) PromptEnvironmentDefinition(ctx context.Context) (string, error) {

}
