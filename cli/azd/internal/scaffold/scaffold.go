package scaffold

import (
	"bytes"
	"embed"
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

const BaseRoot = "scaffold/base"
const TemplateRoot = "scaffold/templates"

// Copy base assets to the target directory.
func CopyBase(targetDir string) error {
	err := copyFS(
		resources.ScaffoldBase,
		BaseRoot,
		targetDir)
	if err != nil {
		return fmt.Errorf("copying base assets: %w", err)
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

// Load loads all templates under a single template.Template.
//
// To execute a named template, call Execute with the defined name.
func Load() (*template.Template, error) {
	funcMap := template.FuncMap{
		"bicepName":        BicepName,
		"containerAppName": ContainerAppName,
		"upper":            strings.ToUpper,
		"lower":            strings.ToLower,
	}

	t, err := template.New("templates").
		Option("missingkey=error").
		Funcs(funcMap).
		ParseFS(resources.ScaffoldTemplates,
			path.Join(TemplateRoot, "*"))
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

func ExecInfra(
	t *template.Template,
	spec InfraSpec,
	target string) error {
	infraRoot := target
	infraApp := filepath.Join(infraRoot, "app")
	err := CopyBase(infraRoot)
	if err != nil {
		return err
	}

	if err = os.MkdirAll(infraApp, osutil.PermissionDirectory); err != nil {
		return err
	}

	if spec.DbCosmos != nil {
		if err := Execute(t, "db-cosmos.bicep", spec.DbCosmos, filepath.Join(infraApp, "db-cosmos.bicep")); err != nil {
			return fmt.Errorf("scaffolding cosmos: %w", err)
		}
	}

	if spec.DbPostgres != nil {
		if err := Execute(t, "db-postgre.bicep", spec.DbPostgres, filepath.Join(infraApp, "db-postgre.bicep")); err != nil {
			return err
		}
	}

	for _, svc := range spec.Services {
		if err := Execute(t, "host-containerapp.bicep", svc, filepath.Join(infraApp, svc.Name+".bicep")); err != nil {
			return err
		}
	}

	err = Execute(t, "main.bicep", spec, filepath.Join(infraRoot, "main.bicep"))
	if err != nil {
		return err
	}

	err = Execute(t, "main.parameters.json", spec, filepath.Join(infraRoot, "main.parameters.json"))
	if err != nil {
		return err
	}

	return nil
}
