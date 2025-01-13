package add

import (
	"context"
	"maps"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

// resourceSelection prompts the user to select a given resource type, returning the resulting resource configuration.
type resourceSelection func(console input.Console, ctx context.Context, p promptOptions) (*project.ResourceConfig, error)

// A menu to be displayed.
type Menu struct {
	// Namespace of the resource type.
	Namespace string
	// Label displayed in the menu.
	Label string

	// SelectResource is the continuation that returns the resource with type filled in.
	SelectResource resourceSelection
}

func (a *AddAction) selectMenu() []Menu {
	return []Menu{
		{Namespace: "db", Label: "Database", SelectResource: selectDatabase},
		{Namespace: "host", Label: "Host service"},
		{Namespace: "ai.openai", Label: "Azure OpenAI", SelectResource: a.selectOpenAi},
	}
}

func selectDatabase(
	console input.Console,
	ctx context.Context,
	p promptOptions) (*project.ResourceConfig, error) {
	resourceTypesDisplayMap := make(map[string]project.ResourceType)
	for _, resourceType := range project.AllResourceTypes() {
		if strings.HasPrefix(string(resourceType), "db.") {
			resourceTypesDisplayMap[resourceType.String()] = resourceType
		}
	}

	r := &project.ResourceConfig{}
	resourceTypesDisplay := slices.Sorted(maps.Keys(resourceTypesDisplayMap))
	dbOption, err := console.Select(ctx, input.ConsoleOptions{
		Message: "Which type of database?",
		Options: resourceTypesDisplay,
	})
	if err != nil {
		return nil, err
	}

	r.Type = resourceTypesDisplayMap[resourceTypesDisplay[dbOption]]
	switch r.Type {
	case project.ResourceTypeDbPostgres:
		r.Props = project.PostgresProps{}
	case project.ResourceTypeDbMongo:
		r.Props = project.MongoDBProps{}
	}
	return r, nil
}
