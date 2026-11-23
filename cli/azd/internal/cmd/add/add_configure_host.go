// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package add

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"

	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
	"github.com/azure/azure-dev/cli/azd/internal/names"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

// LanguageMap is a map of supported languages.
var LanguageMap = map[appdetect.Language]project.ServiceLanguageKind{
	appdetect.DotNet:     project.ServiceLanguageDotNet,
	appdetect.Java:       project.ServiceLanguageJava,
	appdetect.JavaScript: project.ServiceLanguageJavaScript,
	appdetect.TypeScript: project.ServiceLanguageTypeScript,
	appdetect.Python:     project.ServiceLanguagePython,
}

// ServiceFromDetect creates a ServiceConfig from an appdetect project.
func ServiceFromDetect(
	root string,
	svcName string,
	prj appdetect.Project,
	svcKind project.ServiceTargetKind,
) (*project.ServiceConfig, error) {
	svc := &project.ServiceConfig{
		Name: svcName,
	}
	rel, err := filepath.Rel(root, prj.Path)
	if err != nil {
		return nil, err
	}

	if svc.Name == "" {
		dirName := filepath.Base(rel)
		if dirName == "." {
			dirName = filepath.Base(root)
		}

		svc.Name = names.LabelName(dirName)
	}

	svc.Host = svcKind
	svc.RelativePath = rel

	language, supported := LanguageMap[prj.Language]
	if !supported {
		return nil, fmt.Errorf("unsupported language: %s", prj.Language)
	}

	svc.Language = language

	if prj.Docker != nil {
		if svcKind != project.ContainerAppTarget {
			return nil, fmt.Errorf("unsupported host with Dockerfile: %s", svcKind)
		}
		relDocker, err := filepath.Rel(prj.Path, prj.Docker.Path)
		if err != nil {
			return nil, err
		}

		svc.Docker.Path = relDocker
	}

	if prj.RootPath != "" {
		relContext, err := filepath.Rel(prj.Path, prj.RootPath)
		if err != nil {
			return nil, err
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
