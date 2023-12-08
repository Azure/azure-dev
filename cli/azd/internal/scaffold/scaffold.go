package scaffold

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/resources"
)

const baseRoot = "scaffold/base"
const templateRoot = "scaffold/templates"

// Copy base assets to the target directory.
func CopyBase(targetDir string) error {
	err := copyFS(
		resources.ScaffoldBase,
		baseRoot,
		targetDir)
	if err != nil {
		return fmt.Errorf("copying base assets: %w", err)
	}

	return nil
}

func copyFS(embedFs fs.FS, root string, target string) error {
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

// Load loads all templates as a template.Template.
//
// To execute a named template, call Execute with the defined name.
func Load() (*template.Template, error) {
	funcMap := template.FuncMap{
		"bicepName":         BicepName,
		"containerAppInfix": ContainerAppInfix,
		"upper":             strings.ToUpper,
		"lower":             strings.ToLower,
		"formatParam":       FormatParameter,
	}

	t, err := template.New("templates").
		Option("missingkey=error").
		Funcs(funcMap).
		ParseFS(resources.ScaffoldTemplates,
			path.Join(templateRoot, "*"))
	if err != nil {
		return nil, fmt.Errorf("parsing templates: %w", err)
	}

	return t, nil
}

// Execute applies the template associated with t that has the given name
// to the specified data object and writes the output to the dest path on the filesystem.
func Execute(
	t *template.Template,
	name string,
	data any,
	dest string) error {
	buf := bytes.NewBufferString("")
	err := t.ExecuteTemplate(buf, name, data)
	if err != nil {
		return fmt.Errorf("executing template: %w", err)
	}

	err = os.WriteFile(dest, buf.Bytes(), osutil.PermissionFile)
	if err != nil {
		return fmt.Errorf("writing file: %w", err)
	}

	return nil
}

// ExecInfra scaffolds infrastructure files for the given spec, using the loaded templates in t. The resulting files
// are written to the target directory.
func ExecInfra(
	t *template.Template,
	spec InfraSpec,
	target string) error {
	infraRoot := target
	infraApp := filepath.Join(infraRoot, "app")

	// Pre-execution expansion. Additional parameters are added, derived from the initial spec.
	preExecExpand(&spec)

	err := CopyBase(infraRoot)
	if err != nil {
		return err
	}

	if err = os.MkdirAll(infraApp, osutil.PermissionDirectory); err != nil {
		return err
	}

	if spec.DbCosmosMongo != nil {
		err = Execute(t, "db-cosmos-mongo.bicep", spec.DbCosmosMongo, filepath.Join(infraApp, "db-cosmos-mongo.bicep"))
		if err != nil {
			return fmt.Errorf("scaffolding cosmos mongodb: %w", err)
		}
	}

	if spec.DbPostgres != nil {
		err = Execute(t, "db-postgres.bicep", spec.DbPostgres, filepath.Join(infraApp, "db-postgres.bicep"))
		if err != nil {
			return fmt.Errorf("scaffolding postgres: %w", err)
		}
	}

	for _, svc := range spec.Services {
		err = Execute(t, "host-containerapp.bicep", svc, filepath.Join(infraApp, svc.Name+".bicep"))
		if err != nil {
			return fmt.Errorf("scaffolding containerapp: %w", err)
		}
	}

	err = Execute(t, "main.bicep", spec, filepath.Join(infraRoot, "main.bicep"))
	if err != nil {
		return fmt.Errorf("scaffolding main.bicep: %w", err)
	}

	err = Execute(t, "main.parameters.json", spec, filepath.Join(infraRoot, "main.parameters.json"))
	if err != nil {
		return fmt.Errorf("scaffolding main.parameters.json: %w", err)
	}

	return nil
}

func preExecExpand(spec *InfraSpec) {
	// postgres requires specific password seeding parameters
	if spec.DbPostgres != nil {
		spec.Parameters = append(spec.Parameters,
			Parameter{
				Name:   "databasePassword",
				Value:  "$(secretOrRandomPassword ${AZURE_KEY_VAULT_NAME} databasePassword)",
				Type:   "string",
				Secret: true,
			})
	}

	for _, svc := range spec.Services {
		// containerapp requires a global '_exist' parameter for each service
		spec.Parameters = append(spec.Parameters,
			containerAppExistsParameter(svc.Name))
		spec.Parameters = append(spec.Parameters,
			serviceDefPlaceholder(svc.Name))
	}
}
