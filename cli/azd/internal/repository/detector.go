package repository

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

func DetectionToConfig(path string, projects []appdetect.Project) (project.ProjectConfig, error) {
	config := project.ProjectConfig{
		Name:     filepath.Base(path),
		Services: map[string]*project.ServiceConfig{},
	}
	for _, prj := range projects {
		rel, err := filepath.Rel(path, prj.Path)
		if err != nil {
			return project.ProjectConfig{}, err
		}

		svc := project.ServiceConfig{}
		svc.Host = "containerapp"
		svc.RelativePath = rel

		language := mapLanguage(prj.Language)
		if language == "" {
			continue
		}
		svc.Language = language

		// if prj.Docker != nil {
		// 	relDocker, err := filepath.Rel(prj.Path, prj.Docker.Path)
		// 	if err != nil {
		// 		return project.ProjectConfig{}, err
		// 	}

		// 	svc.Docker = project.DockerProjectOptions{
		// 		Path: relDocker,
		// 	}
		// }

		name := filepath.Base(rel)
		if name == "." {
			name = config.Name
		}
		config.Services[name] = &svc
	}

	return config, nil
}

func GenerateProject(path string) error {
	projects, err := appdetect.Detect(path)
	if err != nil {
		return err
	}

	config := project.ProjectConfig{
		Name:     filepath.Base(path),
		Services: map[string]*project.ServiceConfig{},
	}
	for _, prj := range projects {
		rel, err := filepath.Rel(path, prj.Path)
		if err != nil {
			return err
		}

		svc := project.ServiceConfig{}
		svc.Name = filepath.Base(rel)
		svc.Host = "appservice"
		svc.RelativePath = rel

		switch prj.Language {
		case appdetect.Python:
			svc.Language = project.ServiceLanguagePython
		case appdetect.DotNet:
			svc.Language = project.ServiceLanguageDotNet
		case appdetect.JavaScript:
			svc.Language = project.ServiceLanguageJavaScript
		case appdetect.TypeScript:
			svc.Language = project.ServiceLanguageTypeScript
		case appdetect.Java:
			svc.Language = project.ServiceLanguageJava
		default:
			panic(fmt.Sprintf("unhandled language: %s", string(prj.Language)))
		}

		if prj.Docker != nil {
			relDocker, err := filepath.Rel(prj.Path, prj.Docker.Path)
			if err != nil {
				return err
			}

			svc.Docker = project.DockerProjectOptions{
				Path: relDocker,
			}
		}

		config.Services[svc.Name] = &svc
	}

	return project.Save(context.Background(), &config, filepath.Join(path, "azure.yaml.gen"))
}
