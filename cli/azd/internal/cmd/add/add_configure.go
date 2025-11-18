// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package add

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

// DbMap is a map of supported database dependencies.
var DbMap = map[appdetect.DatabaseDep]project.ResourceType{
	appdetect.DbMongo:    project.ResourceTypeDbMongo,
	appdetect.DbPostgres: project.ResourceTypeDbPostgres,
	appdetect.DbMySql:    project.ResourceTypeDbMySql,
	appdetect.DbRedis:    project.ResourceTypeDbRedis,
}

// PromptOptions contains common options for prompting.
type PromptOptions struct {
	// PrjConfig is the current project configuration.
	PrjConfig *project.ProjectConfig

	// ExistingId is the ID of an existing resource.
	// This is only used to configure the resource with an existing resource.
	ExistingId string
}

// ConfigureLive fills in the fields for a resource by first querying live Azure for information.
//
// This is used in addition to Configure currently.
func (a *AddAction) ConfigureLive(
	ctx context.Context,
	r *project.ResourceConfig,
	console input.Console,
	p PromptOptions) (*project.ResourceConfig, error) {
	if r.Existing {
		return r, nil
	}

	var err error

	switch r.Type {
	case project.ResourceTypeAiProject:
		r, err = a.promptAiModel(console, ctx, r, p)
	case project.ResourceTypeOpenAiModel:
		r, err = a.promptOpenAi(console, ctx, r, p)
	}

	if err != nil {
		return nil, err
	}

	return r, nil
}

// Configure fills in the fields for a resource.
func Configure(
	ctx context.Context,
	r *project.ResourceConfig,
	console input.Console,
	p PromptOptions) (*project.ResourceConfig, error) {
	if r.Existing {
		return ConfigureExisting(ctx, r, console, p)
	}

	switch r.Type {
	case project.ResourceTypeHostAppService,
		project.ResourceTypeHostContainerApp:
		return fillUses(ctx, r, console, p)
	case project.ResourceTypeOpenAiModel:
		return fillOpenAiModelName(ctx, r, console, p)
	case project.ResourceTypeDbPostgres,
		project.ResourceTypeDbMySql,
		project.ResourceTypeDbMongo:
		return fillDatabaseName(ctx, r, console, p)
	case project.ResourceTypeDbCosmos:
		r, err := fillDatabaseName(ctx, r, console, p)
		if err != nil {
			return nil, err
		}
		r.Props = project.CosmosDBProps{}
		return r, nil
	case project.ResourceTypeMessagingEventHubs:
		return fillEventHubs(ctx, r, console, p)
	case project.ResourceTypeMessagingServiceBus:
		return fillServiceBus(ctx, r, console, p)
	case project.ResourceTypeDbRedis:
		if _, exists := p.PrjConfig.Resources["redis"]; exists {
			return nil, fmt.Errorf("only one Redis resource is allowed at this time")
		}

		r.Name = "redis"
		return r, nil
	case project.ResourceTypeStorage:
		return fillStorageDetails(ctx, r, console, p)
	case project.ResourceTypeAiProject:
		return fillAiProjectName(ctx, r, console, p)
	case project.ResourceTypeAiSearch:
		if _, exists := p.PrjConfig.Resources["search"]; exists {
			return nil, fmt.Errorf("only one AI Search resource is allowed at this time")
		}

		r.Name = "search"
		return r, nil
	case project.ResourceTypeKeyVault:
		if _, exists := p.PrjConfig.Resources["vault"]; exists {
			return nil, fmt.Errorf(
				"you already have a project key vault named 'vault'. " +
					"To add a secret to it, run 'azd env set-secret <name>'",
			)
		}

		r.Name = "vault"
		return r, nil
	default:
		return r, nil
	}
}

func fillDatabaseName(
	ctx context.Context,
	r *project.ResourceConfig,
	console input.Console,
	p PromptOptions) (*project.ResourceConfig, error) {
	if r.Name != "" {
		return r, nil
	}

	for {
		dbName, err := console.Prompt(ctx, input.ConsoleOptions{
			Message: fmt.Sprintf("Input the name of the app database (%s)", r.Type.String()),
			Help: "Hint: App database name\n\n" +
				"Name of the database that the app connects to. " +
				"This database will be created after running azd provision or azd up.",
		})
		if err != nil {
			return r, err
		}

		if err := validateResourceName(dbName, p.PrjConfig); err != nil {
			console.Message(ctx, err.Error())
			continue
		}

		r.Name = dbName
		break
	}

	return r, nil
}

func fillOpenAiModelName(
	ctx context.Context,
	r *project.ResourceConfig,
	console input.Console,
	p PromptOptions) (*project.ResourceConfig, error) {
	if r.Name != "" {
		return r, nil
	}

	modelProps, ok := r.Props.(project.AIModelProps)
	defaultName := ""

	// provide a default suggestion using the underlying model name
	if ok {
		defaultName = modelProps.Model.Name
		i := 1
		for {
			if _, exists := p.PrjConfig.Resources[defaultName]; exists {
				i++
				defaultName = fmt.Sprintf("%s-%d", defaultName, i)
			} else {
				break
			}
		}
	}

	for {
		modelName, err := console.Prompt(ctx, input.ConsoleOptions{
			Message:      "Provide a name for this model",
			DefaultValue: defaultName,
		})
		if err != nil {
			return nil, err
		}

		if err := validateResourceName(modelName, p.PrjConfig); err != nil {
			console.Message(ctx, err.Error())
			continue
		}

		r.Name = modelName
		break
	}

	return r, nil
}

func fillAiProjectName(
	_ context.Context,
	r *project.ResourceConfig,
	_ input.Console,
	pOptions PromptOptions) (*project.ResourceConfig, error) {
	if r.Name != "" {
		return r, nil
	}

	// provide a default suggestion using the underlying model name
	defaultName := "ai-project"
	i := 1
	for {
		if _, exists := pOptions.PrjConfig.Resources[defaultName]; exists {
			i++
			defaultName = fmt.Sprintf("%s-%d", defaultName, i)
		} else {
			break
		}
	}
	// automatically set a name. Avoid prompting the user for a name as we are abstracting the Foundry and project
	r.Name = defaultName
	return r, nil
}

func fillUses(
	ctx context.Context,
	r *project.ResourceConfig,
	console input.Console,
	p PromptOptions) (*project.ResourceConfig, error) {
	type resourceDisplay struct {
		Resource *project.ResourceConfig
		Display  string
	}
	res := make([]resourceDisplay, 0, len(p.PrjConfig.Resources))
	isHost := strings.HasPrefix(string(r.Type), "host.")
	for _, other := range p.PrjConfig.Resources {
		otherIsHost := strings.HasPrefix(string(other.Type), "host.")
		// Linking between different host types is not supported yet
		if isHost && otherIsHost && r.Type != other.Type {
			continue
		}
		res = append(res, resourceDisplay{
			Resource: other,
			Display: fmt.Sprintf(
				"[%s]\t%s",
				other.Type.String(),
				other.Name),
		})
	}
	slices.SortFunc(res, func(a, b resourceDisplay) int {
		comp := strings.Compare(a.Display, b.Display)
		if comp == 0 {
			return strings.Compare(a.Resource.Name, b.Resource.Name)
		}
		return comp
	})

	if len(res) > 0 {
		labels := make([]string, 0, len(res))
		for _, r := range res {
			labels = append(labels, r.Display)
		}
		if console.IsSpinnerInteractive() {
			formatted, err := output.TabAlign(labels, 3)
			if err != nil {
				return nil, fmt.Errorf("formatting labels: %w", err)
			}
			labels = formatted
		}
		uses, err := console.MultiSelect(ctx, input.ConsoleOptions{
			Message: fmt.Sprintf("Select the resources that %s uses", output.WithHighLightFormat(r.Name)),
			Options: labels,
		})
		if err != nil {
			return nil, err
		}

		// MultiSelect returns string[] not int[], and we had lost the translation mapping with TabAlign.
		// Currently, we use whitespace to splice the item from the formatting text.
		for _, use := range uses {
			for i := len(use) - 1; i >= 0; i-- {
				if unicode.IsSpace(rune(use[i])) {
					r.Uses = append(r.Uses, use[i+1:])
					break
				}
			}
		}
	}

	return r, nil
}

func promptUsedBy(
	ctx context.Context,
	r *project.ResourceConfig,
	console input.Console,
	p PromptOptions) ([]string, error) {
	svc := []string{}
	isHost := strings.HasPrefix(string(r.Type), "host.")
	for _, other := range p.PrjConfig.Resources {
		otherIsHost := strings.HasPrefix(string(other.Type), "host.")
		// Linking between different host types is not supported yet
		if isHost && otherIsHost && r.Type != other.Type {
			continue
		}
		if otherIsHost && !slices.Contains(other.Uses, r.Name) {
			svc = append(svc, other.Name)
		}
	}
	slices.Sort(svc)

	if len(svc) > 0 {
		message := "Select the service(s) that uses this resource"
		if strings.HasPrefix(string(r.Type), "host.") {
			message = "Select the front-end service(s) that uses this service (if applicable)"
		}
		uses, err := console.MultiSelect(ctx, input.ConsoleOptions{
			Message: message,
			Options: svc,
		})
		if err != nil {
			return nil, err
		}

		return uses, nil
	}

	return nil, nil
}
