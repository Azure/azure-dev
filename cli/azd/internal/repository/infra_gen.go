package repository

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"text/template"

	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/resources"
	"github.com/otiai10/copy"
)

type InfraSpec struct {
	Services []ServiceSpec
}

type ServiceSpec struct {
	Name string
	Port int
}

func (i *Initializer) InitializeInfra(
	ctx context.Context,
	azdCtx *azdcontext.AzdContext) error {
	selection, err := i.console.Select(ctx, input.ConsoleOptions{
		Message: "Where is your app code located?",
		Options: []string{
			"In my current directory (local)",
			"In a GitHub repository (remote)",
		},
	})
	if err != nil {
		return err
	}

	switch selection {
	case 0:
		wd := azdCtx.ProjectDirectory()
		title := "Analyzing app code in " + output.WithHighLightFormat(wd)
		var err error
		i.console.ShowSpinner(ctx, title, input.Step)
		projects, err := appdetect.Detect(wd)
		i.console.StopSpinner(ctx, title, input.GetStepResultFormat(err))

		if err != nil {
			return err
		}

		i.console.Message(ctx, "\nDetected languages and databases:\n")
		builder := strings.Builder{}
		tabs := tabwriter.NewWriter(
			&builder,
			0, 0, 4, ' ', 0)
		for _, project := range projects {
			relPath, err := filepath.Rel(wd, project.Path)
			if err != nil {
				return err
			}
			tabs.Write([]byte(
				fmt.Sprintf("  %s\t%s\n",
					project.Language.Display(),
					relPath)))
		}
		err = tabs.Flush()
		if err != nil {
			return err
		}

		i.console.Message(ctx, builder.String())
		i.console.Message(ctx, "\nRecommended Azure services:\n")
		builder.Reset()
		tabs.Write([]byte(
			fmt.Sprintf("  %s\t%s\n",
				"Azure Container Apps",
				"https://learn.microsoft.com/en-us/azure/container-apps/overview")))
		err = tabs.Flush()
		if err != nil {
			return err
		}
		i.console.Message(ctx, builder.String())

		services := make([]ServiceSpec, 0, len(projects))
		for _, project := range projects {
			name := filepath.Base(project.Path)
			var port int
			for {
				val, err := i.console.Prompt(ctx, input.ConsoleOptions{
					Message: "What port does '" + name + "' listen on? (-1 means no ports)",
				})
				if err != nil {
					return err
				}

				port, err = strconv.Atoi(val)
				if err == nil {
					break
				}
				i.console.Message(ctx, "Must be an integer. Try again or press Ctrl+C to cancel")
			}

			services = append(services, ServiceSpec{
				Name: name,
				Port: port,
			})
		}

		confirm, err := i.console.Select(ctx, input.ConsoleOptions{
			Message: "Do you want to continue?",
			Options: []string{
				"Yes - Generate files to host my app on Azure using the recommended services",
				"No - Modify detected languages or databases",
			},
		})
		if err != nil {
			return err
		}

		switch confirm {
		case 0:
			title := "Generating " + output.WithBold(azdcontext.ProjectFileName) +
				" in " + output.WithHighLightFormat(azdCtx.ProjectDirectory())
			i.console.ShowSpinner(ctx, title, input.Step)
			defer i.console.StopSpinner(ctx, title, input.GetStepResultFormat(err))
			config, err := DetectionToConfig(wd, projects)
			if err != nil {
				return fmt.Errorf("converting config: %w", err)
			}
			err = project.Save(
				context.Background(),
				&config,
				filepath.Join(wd, azdcontext.ProjectFileName))
			if err != nil {
				return fmt.Errorf("generating azure.yaml: %w", err)
			}
			i.console.StopSpinner(ctx, title, input.StepDone)

			title = "Generating Infrastructure as Code files in " + output.WithHighLightFormat(azdCtx.ProjectDirectory())
			i.console.ShowSpinner(ctx, title, input.Step)
			staging, err := os.MkdirTemp("", "azd-infra")
			if err != nil {
				return fmt.Errorf("mkdir temp: %w", err)
			}

			err = copyFS(resources.ScaffoldBase, "scaffold/base", staging)
			if err != nil {
				return fmt.Errorf("copying to staging: %w", err)
			}

			stagingApp := filepath.Join(staging, "app")
			if err := os.MkdirAll(stagingApp, osutil.PermissionDirectory); err != nil {
				return err
			}

			for _, svc := range services {
				t, err := template.New(svc.Name).Option("missingkey=error").Parse(string(resources.ApiBicepTempl))
				if err != nil {
					return fmt.Errorf("parsing template: %w", err)
				}

				buf := bytes.NewBufferString("")
				err = t.Execute(buf, svc)
				if err != nil {
					return fmt.Errorf("executing template: %w", err)
				}

				err = os.WriteFile(filepath.Join(stagingApp, svc.Name+".bicep"), buf.Bytes(), osutil.PermissionFile)
				if err != nil {
					return fmt.Errorf("writing service file: %w", err)
				}
			}

			t, err := template.New("main.bicep").Option("missingkey=error").Parse(string(resources.MainBicepTempl))
			if err != nil {
				return fmt.Errorf("parsing template: %w", err)
			}

			buf := bytes.NewBufferString("")
			err = t.Execute(buf, InfraSpec{Services: services})
			if err != nil {
				return fmt.Errorf("executing template: %w", err)
			}

			err = os.WriteFile(filepath.Join(staging, "main.bicep"), buf.Bytes(), osutil.PermissionFile)
			if err != nil {
				return fmt.Errorf("writing main file: %w", err)
			}

			target := filepath.Join(azdCtx.ProjectDirectory(), "infra")
			if err := os.MkdirAll(target, osutil.PermissionDirectory); err != nil {
				return err
			}

			if err := copy.Copy(staging, target); err != nil {
				return fmt.Errorf("copying contents from temp staging directory: %w", err)
			}
		default:
			panic("unimplemented")
		}
	}

	return nil
}

func copyFS(embedFs embed.FS, root string, target string) error {
	return fs.WalkDir(embedFs, root, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		targetPath := filepath.Join(target, name[len(root):])

		if d.IsDir() {
			return os.MkdirAll(targetPath, osutil.PermissionDirectory)
		}

		contents, err := fs.ReadFile(embedFs, name)
		if err != nil {
			return fmt.Errorf("reading file: %w", err)
		}
		return os.WriteFile(targetPath, contents, osutil.PermissionFile)
	})
}
