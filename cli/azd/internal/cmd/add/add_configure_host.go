// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package add

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
	"github.com/azure/azure-dev/cli/azd/internal/names"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/fatih/color"
)

// LanguageMap is a map of supported languages.
var LanguageMap = map[appdetect.Language]project.ServiceLanguageKind{
	appdetect.DotNet:     project.ServiceLanguageDotNet,
	appdetect.Java:       project.ServiceLanguageJava,
	appdetect.JavaScript: project.ServiceLanguageJavaScript,
	appdetect.TypeScript: project.ServiceLanguageTypeScript,
	appdetect.Python:     project.ServiceLanguagePython,
}

func (a *AddAction) configureHost(
	console input.Console,
	ctx context.Context,
	p PromptOptions) (*project.ServiceConfig, *project.ResourceConfig, error) {
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
		languages := slices.SortedFunc(maps.Keys(LanguageMap),
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
	p PromptOptions,
	prj *appdetect.Project,
) (*project.ServiceConfig, error) {
	_, supported := LanguageMap[prj.Language]
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

		if err := validateServiceName(name, p.PrjConfig); err != nil {
			a.console.Message(ctx, err.Error())
			continue
		}

		if err := validateResourceName(name, p.PrjConfig); err != nil {
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

	svc, err := ServiceFromDetect(
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
			if svc.Language == project.ServiceLanguageJava || svc.Language.IsDotNet() {
				props.Port = 8080
			}
		}
	}

	if props.Port == -1 {
		port, err := PromptPort(console, ctx, svc.Name, prj)
		if err != nil {
			return nil, err
		}

		props.Port = port
	}

	resSpec.Props = props
	return &resSpec, nil
}

// ServiceFromDetect creates a ServiceConfig from an appdetect project.
func ServiceFromDetect(
	root string,
	svcName string,
	prj appdetect.Project) (project.ServiceConfig, error) {
	svc := project.ServiceConfig{
		Name: svcName,
	}
	rel, err := filepath.Rel(root, prj.Path)
	if err != nil {
		return svc, err
	}

	if svc.Name == "" {
		dirName := filepath.Base(rel)
		if dirName == "." {
			dirName = filepath.Base(root)
		}

		svc.Name = names.LabelName(dirName)
	}

	svc.Host = project.ContainerAppTarget
	svc.RelativePath = rel

	language, supported := LanguageMap[prj.Language]
	if !supported {
		return svc, fmt.Errorf("unsupported language: %s", prj.Language)
	}

	svc.Language = language

	if prj.Docker != nil {
		relDocker, err := filepath.Rel(prj.Path, prj.Docker.Path)
		if err != nil {
			return svc, err
		}

		svc.Docker.Path = relDocker
	}

	if prj.RootPath != "" {
		relContext, err := filepath.Rel(prj.Path, prj.RootPath)
		if err != nil {
			return svc, err
		}

		svc.Docker.Context = relContext
	}

	if prj.HasWebUIFramework() {
		// By default, use 'dist'. This is common for frameworks such as:
		// - TypeScript
		// - Vite
		svc.OutputPath = "dist"

	loop:
		for _, dep := range prj.Dependencies {
			switch dep {
			case appdetect.JsNext:
				// next.js works as SSR with default node configuration without static build output
				svc.OutputPath = ""
				break loop
			case appdetect.JsVite:
				svc.OutputPath = "dist"
				break loop
			case appdetect.JsReact:
				// react from create-react-app uses 'build' when used, but this can be overridden
				// by choice of build tool, such as when using Vite.
				svc.OutputPath = "build"
			case appdetect.JsAngular:
				// angular uses dist/<project name>
				svc.OutputPath = "dist/" + filepath.Base(rel)
				break loop
			}
		}
	}

	return svc, nil
}

// PromptPort prompts for port selection from an appdetect project.
func PromptPort(
	console input.Console,
	ctx context.Context,
	name string,
	svc appdetect.Project) (int, error) {
	if svc.Docker == nil || svc.Docker.Path == "" { // using default builder from azd
		if svc.Language == appdetect.Java || svc.Language == appdetect.DotNet {
			return 8080, nil
		}
		return 80, nil
	}

	// a custom Dockerfile is provided
	ports := svc.Docker.Ports
	switch len(ports) {
	case 1: // only one port was exposed, that's the one
		return ports[0].Number, nil
	case 0: // no ports exposed, prompt for port
		port, err := promptPortNumber(console, ctx, "What port does '"+name+"' listen on?")
		if err != nil {
			return -1, err
		}
		return port, nil
	}

	// multiple ports exposed, prompt for selection
	var portOptions []string
	for _, port := range ports {
		portOptions = append(portOptions, strconv.Itoa(port.Number))
	}
	portOptions = append(portOptions, "Other")

	selection, err := console.Select(ctx, input.ConsoleOptions{
		Message: "What port does '" + name + "' listen on?",
		Options: portOptions,
	})
	if err != nil {
		return -1, err
	}

	if selection < len(ports) { // user selected a port
		return ports[selection].Number, nil
	}

	// user selected 'Other', prompt for port
	port, err := promptPortNumber(console, ctx, "Provide the port number for '"+name+"':")
	if err != nil {
		return -1, err
	}

	return port, nil
}

func promptPortNumber(console input.Console, ctx context.Context, promptMessage string) (int, error) {
	var port int
	for {
		val, err := console.Prompt(ctx, input.ConsoleOptions{
			Message: promptMessage,
		})
		if err != nil {
			return -1, err
		}

		port, err = strconv.Atoi(val)
		if err != nil {
			console.Message(ctx, "Port must be an integer.")
			continue
		}

		if port < 1 || port > 65535 {
			console.Message(ctx, "Port must be a value between 1 and 65535.")
			continue
		}

		break
	}
	return port, nil
}
