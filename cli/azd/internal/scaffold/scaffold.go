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
	"github.com/psanford/memfs"
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
		"bicepName":        BicepName,
		"containerAppName": ContainerAppName,
		"upper":            strings.ToUpper,
		"lower":            strings.ToLower,
		"formatParam":      FormatParameter,
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

	err = Execute(t, "main.bicep", spec, filepath.Join(infraRoot, "main.bicep"))
	if err != nil {
		return fmt.Errorf("scaffolding main.bicep: %w", err)
	}

	err = Execute(t, "resources.bicep", spec, filepath.Join(infraRoot, "resources.bicep"))
	if err != nil {
		return fmt.Errorf("scaffolding resources.bicep: %w", err)
	}

	err = Execute(t, "main.parameters.json", spec, filepath.Join(infraRoot, "main.parameters.json"))
	if err != nil {
		return fmt.Errorf("scaffolding main.parameters.json: %w", err)
	}

	return nil
}

// ExecInfraFs scaffolds infrastructure files for the given spec, using the loaded templates in t. The resulting files
// are written to the in-memory filesystem.
func ExecInfraFs(
	t *template.Template,
	spec InfraSpec) (*memfs.FS, error) {
	fs := memfs.New()

	// Pre-execution expansion. Additional parameters are added, derived from the initial spec.
	preExecExpand(&spec)

	err := copyFsToMemFs(resources.ScaffoldBase, fs, baseRoot, ".")
	if err != nil {
		return nil, err
	}

	err = executeToFS(fs, t, "main.bicep", "main.bicep", spec)
	if err != nil {
		return nil, fmt.Errorf("scaffolding main.bicep: %w", err)
	}

	err = executeToFS(fs, t, "resources.bicep", "resources.bicep", spec)
	if err != nil {
		return nil, fmt.Errorf("scaffolding resources.bicep: %w", err)
	}

	err = executeToFS(fs, t, "main.parameters.json", "main.parameters.json", spec)
	if err != nil {
		return nil, fmt.Errorf("scaffolding main.parameters.json: %w", err)
	}

	return fs, nil
}

func copyFsToMemFs(embedFs fs.FS, targetFs *memfs.FS, root string, target string) error {
	return fs.WalkDir(embedFs, root, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		targetPath := name[len(root):]
		if target != "" {
			targetPath = path.Join(target, name[len(root):])
		}

		if d.IsDir() {
			return targetFs.MkdirAll(targetPath, osutil.PermissionDirectory)
		}

		contents, err := fs.ReadFile(embedFs, name)
		if err != nil {
			return fmt.Errorf("reading file: %w", err)
		}
		return targetFs.WriteFile(targetPath, contents, osutil.PermissionFile)
	})
}

// executeToFS executes the given template with the given name and context, and writes the result to the given path in
// the given target filesystem.
func executeToFS(targetFS *memfs.FS, tmpl *template.Template, name string, path string, context any) error {
	buf := bytes.NewBufferString("")

	if err := tmpl.ExecuteTemplate(buf, name, context); err != nil {
		return fmt.Errorf("executing template: %w", err)
	}

	if err := targetFS.MkdirAll(filepath.Dir(path), osutil.PermissionDirectory); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}

	if err := targetFS.WriteFile(path, buf.Bytes(), osutil.PermissionFile); err != nil {
		return fmt.Errorf("writing file: %w", err)
	}

	return nil
}

func preExecExpand(spec *InfraSpec) {
	// postgres and mysql requires specific password seeding parameters
	if spec.DbPostgres != nil {
		spec.Parameters = append(spec.Parameters,
			Parameter{
				Name:   "postgreSqlDatabasePassword",
				Value:  "$(secretOrRandomPassword ${AZURE_KEY_VAULT_NAME} postgreSqlDatabasePassword)",
				Type:   "string",
				Secret: true,
			})
	}
	if spec.DbMySql != nil {
		spec.Parameters = append(spec.Parameters,
			Parameter{
				Name:   "mysqlDatabasePassword",
				Value:  "$(secretOrRandomPassword ${AZURE_KEY_VAULT_NAME} mysqlDatabasePassword)",
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
