package add

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
	"github.com/azure/azure-dev/cli/azd/internal/repository"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/fatih/color"
)

func (a *AddAction) configureHost(
	console input.Console,
	ctx context.Context,
	p promptOptions) (*project.ServiceConfig, *project.ResourceConfig, error) {
	prj, err := a.promptCodeProject(ctx)
	if err != nil {
		return nil, nil, err
	}

	svcSpec, err := a.projectAsService(ctx, p, prj)
	if err != nil {
		return nil, nil, err
	}

	resSpec, err := addServiceAsResource(
		ctx,
		console,
		svcSpec,
		*prj)
	if err != nil {
		return nil, nil, err
	}

	return svcSpec, resSpec, nil
}

// promptCodeProject prompts the user to add a code project.
func (a *AddAction) promptCodeProject(ctx context.Context) (*appdetect.Project, error) {
	path, err := promptDir(ctx, a.console, "Where is your app code project located?")
	if err != nil {
		return nil, err
	}

	prj, err := appdetect.DetectDirectory(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("detecting project: %w", err)
	}

	if prj == nil {
		// fallback, prompt for language
		a.console.MessageUxItem(ctx, &ux.WarningMessage{Description: "Could not automatically detect language"})
		languages := slices.SortedFunc(maps.Keys(repository.LanguageMap),
			func(a, b appdetect.Language) int {
				return strings.Compare(a.Display(), b.Display())
			})

		frameworks := slices.SortedFunc(maps.Keys(appdetect.WebUIFrameworks),
			func(a, b appdetect.Dependency) int {
				return strings.Compare(a.Display(), b.Display())
			})

		selections := make([]string, 0, len(languages)+len(frameworks))
		entries := make([]any, 0, len(languages)+len(frameworks))

		for _, lang := range languages {
			selections = append(selections, fmt.Sprintf("%s\t%s", lang.Display(), "[Language]"))
			entries = append(entries, lang)
		}

		for _, framework := range frameworks {
			selections = append(selections, fmt.Sprintf("%s\t%s", framework.Display(), "[Framework]"))
			entries = append(entries, framework)
		}

		// only apply tab-align if interactive
		if a.console.IsSpinnerInteractive() {
			formatted, err := output.TabAlign(selections, 3)
			if err != nil {
				return nil, fmt.Errorf("formatting selections: %w", err)
			}

			selections = formatted
		}

		i, err := a.console.Select(ctx, input.ConsoleOptions{
			Message: "Enter the language or framework",
			Options: selections,
		})
		if err != nil {
			return nil, err
		}

		prj := &appdetect.Project{
			Path:          path,
			DetectionRule: "Manual",
		}
		switch entries[i].(type) {
		case appdetect.Language:
			prj.Language = entries[i].(appdetect.Language)
		case appdetect.Dependency:
			framework := entries[i].(appdetect.Dependency)
			prj.Language = framework.Language()
			prj.Dependencies = []appdetect.Dependency{framework}
		}

		// improve: appdetect: add troubleshooting for all kinds of languages
		if prj.Language == appdetect.Python {
			_, err := os.Stat(filepath.Join(path, "requirements.txt"))
			if errors.Is(err, os.ErrNotExist) {
				return nil, &internal.ErrorWithSuggestion{
					Err: errors.New("no requirements.txt found"),
					//nolint:lll
					Suggestion: "Run 'pip freeze > requirements.txt' or 'pip3 freeze > requirements.txt' to create a requirements.txt file for .",
				}
			}
		}
		return prj, nil
	}

	return prj, nil
}

// projectAsService prompts the user for enough information to create a service.
func (a *AddAction) projectAsService(
	ctx context.Context,
	p promptOptions,
	prj *appdetect.Project,
) (*project.ServiceConfig, error) {
	_, supported := repository.LanguageMap[prj.Language]
	if !supported {
		return nil, fmt.Errorf("unsupported language: %s", prj.Language)
	}

	svcName := azdcontext.ProjectName(prj.Path)

	for {
		name, err := a.console.Prompt(ctx, input.ConsoleOptions{
			Message:      "Enter a name for this service:",
			DefaultValue: svcName,
		})
		if err != nil {
			return nil, err
		}

		if err := validateServiceName(name, p.prj); err != nil {
			a.console.Message(ctx, err.Error())
			continue
		}

		if err := validateResourceName(name, p.prj); err != nil {
			a.console.Message(ctx, err.Error())
			continue
		}

		svcName = name
		break
	}

	confirm, err := a.console.Confirm(ctx, input.ConsoleOptions{
		Message:      "azd will use " + color.MagentaString("Azure Container App") + " to host this project. Continue?",
		DefaultValue: true,
	})
	if err != nil {
		return nil, err
	} else if !confirm {
		return nil, errors.New("cancelled")
	}

	if prj.Docker == nil {
		confirm, err := a.console.Confirm(ctx, input.ConsoleOptions{
			Message:      "No Dockerfile found. Allow azd to automatically build a container image?",
			DefaultValue: true,
		})
		if err != nil {
			return nil, err
		}

		if !confirm {
			path, err := promptDockerfile(ctx, a.console, "Where is your Dockerfile located?")
			if err != nil {
				return nil, err
			}

			docker, err := appdetect.AnalyzeDocker(path)
			if err != nil {
				return nil, err
			}

			prj.Docker = docker
		}
	}

	svc, err := repository.ServiceFromDetect(
		a.azdCtx.ProjectDirectory(),
		svcName,
		*prj)
	if err != nil {
		return nil, err
	}

	return &svc, nil
}

func addServiceAsResource(
	ctx context.Context,
	console input.Console,
	svc *project.ServiceConfig,
	prj appdetect.Project) (*project.ResourceConfig, error) {
	resSpec := project.ResourceConfig{
		Name: svc.Name,
	}

	if svc.Host == project.ContainerAppTarget {
		resSpec.Type = project.ResourceTypeHostContainerApp
	} else {
		return nil, fmt.Errorf("unsupported service target: %s", svc.Host)
	}

	props := project.ContainerAppProps{
		Port: -1,
	}

	if svc.Docker.Path == "" {
		// no Dockerfile is present, set port based on azd default builder logic
		if _, err := os.Stat(filepath.Join(svc.RelativePath, "Dockerfile")); errors.Is(err, os.ErrNotExist) {
			// default builder always specifies port 80
			props.Port = 80
			if svc.Language == project.ServiceLanguageJava {
				props.Port = 8080
			}
		}
	}

	if props.Port == -1 {
		port, err := repository.PromptPort(console, ctx, svc.Name, prj)
		if err != nil {
			return nil, err
		}

		props.Port = port
	}

	resSpec.Props = props
	return &resSpec, nil
}
