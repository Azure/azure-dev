package repository

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/fatih/color"
	"golang.org/x/exp/maps"
	"golang.org/x/exp/slices"
)

var languageMap = map[appdetect.Language]project.ServiceLanguageKind{
	appdetect.DotNet:     project.ServiceLanguageDotNet,
	appdetect.Java:       project.ServiceLanguageJava,
	appdetect.JavaScript: project.ServiceLanguageJavaScript,
	appdetect.TypeScript: project.ServiceLanguageTypeScript,
	appdetect.Python:     project.ServiceLanguagePython,
}

var dbMap = map[appdetect.DatabaseDep]struct{}{
	appdetect.DbMongo:    {},
	appdetect.DbPostgres: {},
}

func projectDisplayName(p appdetect.Project) string {
	name := p.Language.Display()
	for _, framework := range p.Dependencies {
		if framework.IsWebUIFramework() {
			name = framework.Display()
		}
	}

	return name
}

func dirSuggestions(input string) []string {
	completions := []string{}
	matches, _ := filepath.Glob(input + "*")
	for _, match := range matches {
		if fs, err := os.Stat(match); err == nil && fs.IsDir() {
			completions = append(completions, match)
		}
	}
	return completions
}

func tabWrite(selections []string, padding int) ([]string, error) {
	tabbed := strings.Builder{}
	tabW := tabwriter.NewWriter(&tabbed, 0, 0, padding, ' ', 0)
	_, err := tabW.Write([]byte(strings.Join(selections, "\n")))
	if err != nil {
		return nil, err
	}
	err = tabW.Flush()
	if err != nil {
		return nil, err
	}

	return strings.Split(tabbed.String(), "\n"), nil
}

func promptDir(
	ctx context.Context,
	console input.Console,
	message string) (string, error) {
	for {
		path, err := console.Prompt(ctx, input.ConsoleOptions{
			Message: message,
			Suggest: dirSuggestions,
		})
		if err != nil {
			return "", err
		}

		fs, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) || fs != nil && !fs.IsDir() {
			console.Message(ctx, fmt.Sprintf("'%s' is not a valid directory", path))
			continue
		}

		if err != nil {
			return "", err
		}

		path, err = filepath.Abs(path)
		if err != nil {
			return "", err
		}

		return path, err
	}
}

func relSafe(root string, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}

	return rel
}

type EntryKind string

const (
	EntryKindDetected EntryKind = "detection"
	EntryKindManual   EntryKind = "manual"
	EntryKindModified EntryKind = "modified"
)

type detectConfirm struct {
	services  []appdetect.Project
	databases map[appdetect.DatabaseDep]EntryKind
	modified  bool

	console input.Console
	root    string
}

func (d *detectConfirm) init(projects []appdetect.Project) {
	d.databases = make(map[appdetect.DatabaseDep]EntryKind)
	d.services = make([]appdetect.Project, 0, len(projects))
	d.modified = false

	for _, project := range projects {
		if _, supported := languageMap[project.Language]; supported {
			d.services = append(d.services, project)
		}

		for _, dbType := range project.DatabaseDeps {
			if _, supported := dbMap[dbType]; supported {
				d.databases[dbType] = EntryKindDetected
			}
		}
	}
}

func (d *detectConfirm) render(ctx context.Context) error {
	if d.modified {
		d.console.ShowSpinner(ctx, "Revising detected services", input.Step)
		if d.console.IsSpinnerInteractive() {
			time.Sleep(1 * time.Second)
		}
		d.console.StopSpinner(ctx, "Revising detected services", input.StepDone)
		d.console.Message(ctx, "\n"+output.WithBold("Detected services (Revised):")+"\n")
	} else {
		d.console.Message(ctx, "\n"+output.WithBold("Detected services:")+"\n")
	}

	recommendedServices := []string{}
	for _, svc := range d.services {
		status := ""
		if svc.DetectionRule == string(EntryKindModified) {
			status = " " + output.WithSuccessFormat("[Updated]")
		} else if svc.DetectionRule == string(EntryKindManual) {
			status = " " + output.WithSuccessFormat("[Added]")
		}

		d.console.Message(ctx, "  "+color.BlueString(projectDisplayName(svc))+status)
		d.console.Message(ctx, "  "+"Detected in: "+output.WithHighLightFormat(relSafe(d.root, svc.Path)))
		d.console.Message(ctx, "")

		if len(recommendedServices) == 0 {
			recommendedServices = append(recommendedServices, "Azure Container Apps")
		}
	}

	for db, entry := range d.databases {
		switch db {
		case appdetect.DbPostgres:
			recommendedServices = append(recommendedServices, "Azure Database for PostgreSQL flexible server")
		case appdetect.DbMongo:
			recommendedServices = append(recommendedServices, "Azure CosmosDB API for MongoDB")
		}

		status := ""
		if entry == EntryKindModified {
			status = " " + output.WithSuccessFormat("[Updated]")
		} else if entry == EntryKindManual {
			status = " " + output.WithSuccessFormat("[Added]")
		}

		d.console.Message(ctx, "  "+color.BlueString(db.Display())+status)
		d.console.Message(ctx, "")
	}

	displayedServices := make([]string, 0, len(recommendedServices))
	for _, svc := range recommendedServices {
		displayedServices = append(displayedServices, color.MagentaString(svc))
	}

	if len(displayedServices) > 0 {
		d.console.Message(ctx,
			"azd will generate the files necessary to host your app on Azure using "+
				ux.ListAsText(displayedServices)+".\n")
	}

	return nil
}

func (d *detectConfirm) confirm(ctx context.Context) error {
confirm:
	for {
		if err := d.render(ctx); err != nil {
			return err
		}
		d.modified = false

		continueOption, err := d.console.Select(ctx, input.ConsoleOptions{
			Message: "Select an option",
			Options: []string{
				"Confirm and continue initializing my app",
				"Remove a detected service",
				"Add an undetected service",
			},
		})
		if err != nil {
			return err
		}

		switch continueOption {
		case 0:
			return nil
		case 1:
			if err := d.remove(ctx); err != nil {
				return err
			}
		case 2:
			if err := d.add(ctx); err != nil {
				return err
			}
			continue confirm
		}
	}
}

func (d *detectConfirm) remove(ctx context.Context) error {
	modifyOptions := make([]string, 0, len(d.services)+len(d.databases))
	for _, svc := range d.services {
		modifyOptions = append(
			modifyOptions, fmt.Sprintf("%s in %s", projectDisplayName(svc), relSafe(d.root, svc.Path)))
	}

	displayDbs := maps.Keys(d.databases)
	for _, db := range displayDbs {
		modifyOptions = append(modifyOptions, db.Display())
	}

	i, err := d.console.Select(ctx, input.ConsoleOptions{
		Message: "Select the service you want to remove",
		Options: modifyOptions,
	})
	if err != nil {
		return err
	}

	if i < len(d.services) {
		svc := d.services[i]
		confirm, err := d.console.Confirm(ctx, input.ConsoleOptions{
			Message: fmt.Sprintf(
				"Remove %s in %s?", projectDisplayName(svc), relSafe(d.root, svc.Path)),
		})
		if err != nil {
			return err
		}

		if !confirm {
			return nil
		}

		d.services = append(d.services[:i], d.services[i+1:]...)
		d.modified = true
	} else if i < len(d.services)+len(d.databases) {
		db := displayDbs[i-len(d.services)]

		confirm, err := d.console.Confirm(ctx, input.ConsoleOptions{
			Message: fmt.Sprintf(
				"Remove %s?", db.Display()),
		})
		if err != nil {
			return err
		}

		if !confirm {
			return nil
		}

		delete(d.databases, db)
		d.modified = true
	}

	return nil
}

func (d *detectConfirm) add(ctx context.Context) error {
	languages := maps.Keys(languageMap)
	slices.SortFunc(languages, func(a, b appdetect.Language) bool {
		return a.Display() < b.Display()
	})

	frameworks := maps.Keys(appdetect.WebUIFrameworks)
	slices.SortFunc(frameworks, func(a, b appdetect.Dependency) bool {
		return a.Display() < b.Display()
	})

	// only include databases not already added
	allDbs := maps.Keys(dbMap)
	databases := make([]appdetect.DatabaseDep, 0, len(allDbs))
	for _, db := range databases {
		if _, ok := d.databases[db]; !ok {
			databases = append(databases, db)
		}
	}
	slices.SortFunc(databases, func(a, b appdetect.DatabaseDep) bool {
		return a.Display() < b.Display()
	})

	selections := make([]string, 0, len(languages)+len(frameworks)+len(databases))
	entries := make([]any, 0, len(languages)+len(frameworks)+len(databases))

	for _, lang := range languages {
		selections = append(selections, fmt.Sprintf("%s\t%s", lang.Display(), "[Language]"))
		entries = append(entries, lang)
	}

	for _, framework := range frameworks {
		selections = append(selections, fmt.Sprintf("%s\t%s", framework.Display(), "[Framework]"))
		entries = append(entries, framework)
	}

	for _, db := range databases {
		selections = append(selections, fmt.Sprintf("%s\t%s", db.Display(), "[Database]"))
		entries = append(entries, db)
	}

	selections, err := tabWrite(selections, 3)
	if err != nil {
		return fmt.Errorf("formatting selections: %w", err)
	}

	i, err := d.console.Select(ctx, input.ConsoleOptions{
		Message: "Select a language or database to add",
		Options: selections,
	})
	if err != nil {
		return err
	}

	s := appdetect.Project{}
	switch entries[i].(type) {
	case appdetect.Language:
		s.Language = entries[i].(appdetect.Language)
	case appdetect.Dependency:
		framework := entries[i].(appdetect.Dependency)
		if framework.Language() != "" {
			s.Dependencies = []appdetect.Dependency{framework}
			s.Language = framework.Language()
		}
	case appdetect.DatabaseDep:
		dbDep := entries[i].(appdetect.DatabaseDep)
		d.databases[dbDep] = EntryKindManual

		svcSelect := make([]string, 0, len(d.services))
		for _, svc := range d.services {
			svcSelect = append(svcSelect,
				fmt.Sprintf("%s\t[%s]", projectDisplayName(svc), filepath.Base(svc.Path)))
		}

		svcSelect, err = tabWrite(svcSelect, 3)
		if err != nil {
			return err
		}

		idx, err := d.console.Select(ctx, input.ConsoleOptions{
			Message: "Select the service that uses this database",
			Options: svcSelect,
		})
		if err != nil {
			return err
		}

		d.services[idx].DatabaseDeps = append(d.services[idx].DatabaseDeps, dbDep)
		d.modified = true
		return nil
	default:
		log.Panic("unhandled entry type")
	}

	msg := fmt.Sprintf("Enter file path of the directory that uses '%s'", projectDisplayName(s))
	path, err := promptDir(ctx, d.console, msg)
	if err != nil {
		return err
	}

	// deduplicate the path against existing services
	for idx, svc := range d.services {
		if svc.Path == path {
			d.console.Message(
				ctx,
				fmt.Sprintf(
					"\nazd previously detected '%s' at %s.\n", projectDisplayName(svc), svc.Path))

			confirm, err := d.console.Confirm(ctx, input.ConsoleOptions{
				Message: fmt.Sprintf(
					"Do you want to change the detected service to '%s'", projectDisplayName(s)),
			})
			if err != nil {
				return err
			}
			if confirm {
				d.modified = true
				d.services[idx].Language = s.Language
				d.services[idx].Dependencies = s.Dependencies
				d.services[idx].DetectionRule = string(EntryKindModified)
			}

			return nil
		}
	}

	s.Path = filepath.Clean(path)
	s.DetectionRule = string(EntryKindManual)
	d.services = append(d.services, s)
	return nil
}

func prjConfigFromDetect(root string, detect detectConfirm) (project.ProjectConfig, error) {
	config := project.ProjectConfig{
		Name:     filepath.Base(root),
		Services: map[string]*project.ServiceConfig{},
	}
	for _, prj := range detect.services {
		rel, err := filepath.Rel(root, prj.Path)
		if err != nil {
			return project.ProjectConfig{}, err
		}

		svc := project.ServiceConfig{}
		svc.Host = project.ContainerAppTarget
		svc.RelativePath = rel

		language, supported := languageMap[prj.Language]
		if !supported {
			continue
		}
		svc.Language = language

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
