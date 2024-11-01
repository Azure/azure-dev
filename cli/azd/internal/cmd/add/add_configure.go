package add

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/fatih/color"
)

// promptOptions contains common options for prompting.
type promptOptions struct {
	// prj is the current project configuration.
	prj *project.ProjectConfig
}

// configure fills in the fields for a resource.
func configure(
	ctx context.Context,
	r *project.ResourceConfig,
	console input.Console,
	p promptOptions) (*project.ResourceConfig, error) {
	switch r.Type {
	case project.ResourceTypeHostContainerApp:
		return fillUses(ctx, r, console, p)
	case project.ResourceTypeOpenAiModel:
		return fillAiModelName(ctx, r, console, p)
	case project.ResourceTypeDbPostgres,
		project.ResourceTypeDbMongo:
		return fillDatabaseName(ctx, r, console, p)
	case project.ResourceTypeDbRedis:
		if _, exists := p.prj.Resources["redis"]; exists {
			return nil, fmt.Errorf("only one Redis resource is allowed at this time")
		}

		r.Name = "redis"
		return r, nil
	default:
		return r, nil
	}
}

func fillDatabaseName(
	ctx context.Context,
	r *project.ResourceConfig,
	console input.Console,
	p promptOptions) (*project.ResourceConfig, error) {
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

		if err := validateResourceName(dbName, p.prj); err != nil {
			console.Message(ctx, err.Error())
			continue
		}

		r.Name = dbName
		break
	}

	return r, nil
}

func fillAiModelName(
	ctx context.Context,
	r *project.ResourceConfig,
	console input.Console,
	p promptOptions) (*project.ResourceConfig, error) {
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
			if _, exists := p.prj.Resources[defaultName]; exists {
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

		if err := validateResourceName(modelName, p.prj); err != nil {
			console.Message(ctx, err.Error())
			continue
		}

		r.Name = modelName
		break
	}

	return r, nil
}

func fillUses(
	ctx context.Context,
	r *project.ResourceConfig,
	console input.Console,
	p promptOptions) (*project.ResourceConfig, error) {
	type resourceDisplay struct {
		Resource *project.ResourceConfig
		Display  string
	}
	res := make([]resourceDisplay, 0, len(p.prj.Resources))
	for _, r := range p.prj.Resources {
		res = append(res, resourceDisplay{
			Resource: r,
			Display: fmt.Sprintf(
				"[%s]\t%s",
				r.Type.String(),
				r.Name),
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
			Message: fmt.Sprintf("Select the resources that %s uses", color.BlueString(r.Name)),
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
	p promptOptions) ([]string, error) {
	svc := []string{}
	for _, other := range p.prj.Resources {
		if strings.HasPrefix(string(other.Type), "host.") && !slices.Contains(r.Uses, other.Name) {
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
