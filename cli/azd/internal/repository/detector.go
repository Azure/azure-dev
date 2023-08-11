package repository

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
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

		entrypoint := ""
		module := ""
		if svc.Language == project.ServiceLanguagePython {
			mapped := map[string]struct{}{}
			for _, f := range prj.RawFrameworks {
				mapped[f] = struct{}{}
			}

			if _, ok := mapped["Django"]; ok {
				de, err := os.ReadDir(path)
				if err != nil {
					return project.ProjectConfig{}, err
				}

				for _, e := range de {
					if e.IsDir() {
						if _, err := os.Stat(filepath.Join(path, e.Name(), "wsgi.py")); err == nil {
							module = e.Name() + ".wsgi"
							entrypoint = "gunicorn --access-logfile '-' --error-logfile '-' " + module
							break
						}
					}
				}
			} else if _, ok := mapped["flask"]; ok {
				knownFiles := []string{
					"app.py", "application.py", "index.py", "run.py", "server.py", "wsgi.py",
				}

				for _, f := range knownFiles {
					if _, err := os.Stat(filepath.Join(path, f)); err == nil {
						module = f[:len(f)-3] + ":" + "app"
						entrypoint = "gunicorn --access-logfile '-' --error-logfile '-' " + module
						break
					}
				}
			} else if _, ok := mapped["fastapi"]; ok {
				err = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
					if err != nil {
						return err
					}

					if filepath.Ext(p) == ".py" {
						f, err := os.Open(p)
						if err != nil {
							return err
						}
						defer f.Close()

						scanner := bufio.NewScanner(f)
						for scanner.Scan() {
							line := scanner.Text()
							if strings.Contains(line, "FastAPI(") {
								moduleFile := strings.ReplaceAll(p, "/", ".")
								moduleFile = moduleFile[:len(moduleFile)-3]
								module = moduleFile + ":" + "app"
								entrypoint = "uvicorn " + module + " --port $PORT"
								return filepath.SkipAll
							}
						}
					}

					return nil
				})

				if err != nil {
					return project.ProjectConfig{}, err
				}
			} else {
				err = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
					if err != nil {
						return err
					}

					if filepath.Ext(p) == ".py" {
						f, err := os.Open(p)
						if err != nil {
							return err
						}
						defer f.Close()

						scanner := bufio.NewScanner(f)
						for scanner.Scan() {
							line := scanner.Text()
							if strings.Contains(line, "__main__") {
								entrypoint = "python3 " + p
								return filepath.SkipAll
							}
						}
					}

					return nil
				})

				if err != nil {
					return project.ProjectConfig{}, err
				}
			}

			if entrypoint != "" {
				err = os.WriteFile(filepath.Join(prj.Path, "Procfile"), []byte("web: "+entrypoint), osutil.PermissionFile)
				if err != nil {
					return project.ProjectConfig{}, err
				}
			}
		}

		if prj.Docker != nil {
			relDocker, err := filepath.Rel(prj.Path, prj.Docker.Path)
			if err != nil {
				return project.ProjectConfig{}, err
			}

			svc.Docker = project.DockerProjectOptions{
				Path: relDocker,
			}
		}

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
