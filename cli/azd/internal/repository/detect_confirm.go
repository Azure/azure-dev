package repository

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/AlecAivazis/survey/v2/terminal"
	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/fatih/color"
	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/exp/maps"
	"golang.org/x/exp/slices"
)

func projectDisplayName(p appdetect.Project) string {
	name := p.Language.Display()
	for _, framework := range p.Dependencies {
		if framework.IsWebUIFramework() {
			name = framework.Display()
		}
	}

	return name
}

type EntryKind string

const (
	EntryKindDetected EntryKind = "detection"
	EntryKindManual   EntryKind = "manual"
	EntryKindModified EntryKind = "modified"
)

// detectConfirm handles prompting for confirming the detected services and databases
type detectConfirm struct {
	// detected services and databases
	Services  []appdetect.Project
	Databases map[appdetect.DatabaseDep]EntryKind

	// the root directory of the project
	root string

	// internal state and components
	modified bool
	console  input.Console
}

// Init initializes state from initial detection output
func (d *detectConfirm) Init(projects []appdetect.Project, root string) {
	d.Databases = make(map[appdetect.DatabaseDep]EntryKind)
	d.Services = make([]appdetect.Project, 0, len(projects))
	d.modified = false
	d.root = root

	for _, project := range projects {
		if _, supported := languageMap[project.Language]; supported {
			d.Services = append(d.Services, project)
		}

		for _, dbType := range project.DatabaseDeps {
			if _, supported := dbMap[dbType]; supported {
				d.Databases[dbType] = EntryKindDetected
			}
		}
	}

	d.captureUsage(
		fields.AppInitDetectedDatabase,
		fields.AppInitDetectedServices)
}

func (d *detectConfirm) captureUsage(
	databases attribute.Key,
	services attribute.Key) {
	names := make([]string, 0, len(d.Services))
	for _, svc := range d.Services {
		names = append(names, string(svc.Language))
	}

	dbNames := make([]string, 0, len(d.Databases))
	for db := range d.Databases {
		dbNames = append(dbNames, string(db))
	}

	tracing.SetUsageAttributes(
		databases.StringSlice(dbNames),
		services.StringSlice(names),
	)
}

// Confirm prompts the user to confirm the detected services and databases,
// providing modifications to the detected services and databases.
func (d *detectConfirm) Confirm(ctx context.Context) error {
	for {
		if err := d.render(ctx); err != nil {
			return err
		}

		if len(d.Services) == 0 && !d.modified {
			confirmAdd, err := d.console.Confirm(ctx, input.ConsoleOptions{
				Message:      "Add an undetected service?",
				DefaultValue: true,
			})
			if err != nil {
				return err
			}

			if !confirmAdd {
				return fmt.Errorf("cancelled")
			}

			if err := d.add(ctx); err != nil {
				return err
			}

			tracing.IncrementUsageAttribute(fields.AppInitModifyAddCount.Int(1))
			continue
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
			d.captureUsage(
				fields.AppInitConfirmedDatabases,
				fields.AppInitConfirmedServices)
			return nil
		case 1:
			if err := d.remove(ctx); err != nil {
				if errors.Is(err, terminal.InterruptErr) {
					continue
				}
				return err
			}

			tracing.IncrementUsageAttribute(fields.AppInitModifyRemoveCount.Int(1))
		case 2:
			if err := d.add(ctx); err != nil {
				if errors.Is(err, terminal.InterruptErr) {
					continue
				}
				return err
			}

			tracing.IncrementUsageAttribute(fields.AppInitModifyAddCount.Int(1))
		}
	}
}

func (d *detectConfirm) render(ctx context.Context) error {
	if d.modified {
		d.console.ShowSpinner(ctx, "Revising detected services", input.Step)
		if d.console.IsSpinnerInteractive() {
			// Slow down the spinner if it's interactive to make it more visible
			time.Sleep(1 * time.Second)
		}
		d.console.StopSpinner(ctx, "Revising detected services", input.StepDone)
		d.console.Message(ctx, "\n"+output.WithBold("Detected services (Revised):")+"\n")
	} else if len(d.Services) == 0 {
		d.console.Message(ctx, "\n"+output.WithWarningFormat("No services were automatically detected.")+"\n")
	} else {
		d.console.Message(ctx, "\n"+output.WithBold("Detected services:")+"\n")
	}

	recommendedServices := []string{}
	for _, svc := range d.Services {
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

	for db, entry := range d.Databases {
		switch db {
		case appdetect.DbPostgres:
			recommendedServices = append(recommendedServices, "Azure Database for PostgreSQL flexible server")
		case appdetect.DbMongo:
			recommendedServices = append(recommendedServices, "Azure CosmosDB API for MongoDB")
		case appdetect.DbRedis:
			recommendedServices = append(recommendedServices, "Azure Container Apps Redis add-on")
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

func (d *detectConfirm) remove(ctx context.Context) error {
	modifyOptions := make([]string, 0, len(d.Services)+len(d.Databases))
	for _, svc := range d.Services {
		modifyOptions = append(
			modifyOptions, fmt.Sprintf("%s in %s", projectDisplayName(svc), relSafe(d.root, svc.Path)))
	}

	displayDbs := maps.Keys(d.Databases)
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

	if i < len(d.Services) {
		svc := d.Services[i]
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

		d.Services = append(d.Services[:i], d.Services[i+1:]...)
		d.modified = true
	} else if i < len(d.Services)+len(d.Databases) {
		db := displayDbs[i-len(d.Services)]

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

		delete(d.Databases, db)

		for i := range d.Services {
			for j, dependency := range d.Services[i].DatabaseDeps {
				if dependency == db {
					d.Services[i].DatabaseDeps = append(
						d.Services[i].DatabaseDeps[:j],
						d.Services[i].DatabaseDeps[j+1:]...)
					d.Services[i].DetectionRule = string(EntryKindModified)
				}
			}
		}
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
	for _, db := range allDbs {
		if _, ok := d.Databases[db]; !ok {
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

	// only apply tab-align if interactive
	if d.console.IsSpinnerInteractive() {
		formatted, err := tabWrite(selections, 3)
		if err != nil {
			return fmt.Errorf("formatting selections: %w", err)
		}

		selections = formatted
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
		d.Databases[dbDep] = EntryKindManual

		svcSelect := make([]string, 0, len(d.Services))
		for _, svc := range d.Services {
			svcSelect = append(svcSelect,
				fmt.Sprintf("%s in %s", projectDisplayName(svc), filepath.Base(svc.Path)))
		}

		idx, err := d.console.Select(ctx, input.ConsoleOptions{
			Message: "Select the service that uses this database",
			Options: svcSelect,
		})
		if err != nil {
			return err
		}

		d.Services[idx].DatabaseDeps = append(d.Services[idx].DatabaseDeps, dbDep)
		d.Services[idx].DetectionRule = string(EntryKindModified)
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
	for idx, svc := range d.Services {
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
				d.Services[idx].Language = s.Language
				d.Services[idx].Dependencies = s.Dependencies
				d.Services[idx].DetectionRule = string(EntryKindModified)
			}

			return nil
		}
	}

	// Provide additional validation for project selection
	if s.Language == appdetect.Python {
		if _, err := os.Stat(filepath.Join(path, "requirements.txt")); errors.Is(err, os.ErrNotExist) {
			d.console.Message(
				ctx,
				fmt.Sprintf("No '%s' file found in %s.",
					output.WithBold("requirements.txt"),
					output.WithHighLightFormat(path)))
			confirm, err := d.console.Confirm(ctx, input.ConsoleOptions{
				Message: "This file may be required when deploying to Azure. Continue?",
			})
			if err != nil {
				return err
			}

			if !confirm {
				return fmt.Errorf("cancelled")
			}
		}
	}

	s.Path = filepath.Clean(path)
	s.DetectionRule = string(EntryKindManual)
	d.Services = append(d.Services, s)
	d.modified = true
	return nil
}
