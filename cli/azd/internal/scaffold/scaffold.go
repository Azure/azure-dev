package scaffold

import (
	"bytes"
	"fmt"
	"github.com/azure/azure-dev/cli/azd/internal"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"text/template"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/resources"
	"github.com/psanford/memfs"
)

const baseRoot = "scaffold/base"
const templateRoot = "scaffold/templates"

// Load loads all templates as a template.Template.
//
// To execute a named template, call Execute with the defined name.
func Load() (*template.Template, error) {
	funcMap := template.FuncMap{
		"bicepName":            BicepName,
		"containerAppName":     ContainerAppName,
		"upper":                strings.ToUpper,
		"lower":                strings.ToLower,
		"alphaSnakeUpper":      AlphaSnakeUpper,
		"formatParam":          FormatParameter,
		"hasPrefix":            strings.HasPrefix,
		"toBicepEnv":           ToBicepEnv,
		"shouldAddToBicepFile": ShouldAddToBicepFile,
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

func supportingFiles(spec InfraSpec) []string {
	files := []string{"/abbreviations.json"}

	if spec.DbRedis != nil {
		files = append(files, "/modules/set-redis-conn.bicep")
	}

	if len(spec.Services) > 0 {
		files = append(files, "/modules/fetch-container-image.bicep")
	}

	if spec.AzureServiceBus != nil && spec.AzureServiceBus.AuthType == internal.AuthTypeConnectionString {
		files = append(files, "/modules/set-servicebus-namespace-connection-string.bicep")
	}

	if spec.AzureEventHubs != nil && spec.AzureEventHubs.AuthType == internal.AuthTypeConnectionString {
		files = append(files, "/modules/set-event-hubs-namespace-connection-string.bicep")
	}

	if spec.AzureStorageAccount != nil && spec.AzureStorageAccount.AuthType == internal.AuthTypeConnectionString {
		files = append(files, "/modules/set-storage-account-connection-string.bicep")
	}

	return files
}

// ExecInfra scaffolds infrastructure files for the given spec, using the loaded templates in t. The resulting files
// are written to the target directory.
func ExecInfra(
	t *template.Template,
	spec InfraSpec,
	target string) error {
	infraRoot := target
	files, err := ExecInfraFs(t, spec)
	if err != nil {
		return err
	}

	err = fs.WalkDir(files, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		target := filepath.Join(infraRoot, path)
		if err := os.MkdirAll(filepath.Dir(target), osutil.PermissionDirectoryOwnerOnly); err != nil {
			return err
		}

		contents, err := fs.ReadFile(files, path)
		if err != nil {
			return err
		}

		return os.WriteFile(target, contents, d.Type().Perm())
	})
	if err != nil {
		return fmt.Errorf("writing infrastructure: %w", err)
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

	files := supportingFiles(spec)
	err := copyFsToMemFs(resources.ScaffoldBase, fs, baseRoot, ".", files)
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

func copyFsToMemFs(embedFs fs.FS, targetFs *memfs.FS, root string, target string, files []string) error {
	return fs.WalkDir(embedFs, root, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		targetPath := name[len(root):]
		contains := slices.Contains(files, targetPath)
		if !contains {
			return nil
		}

		if target != "" {
			targetPath = path.Join(target, name[len(root):])
		}

		if err := targetFs.MkdirAll(filepath.Dir(targetPath), osutil.PermissionDirectory); err != nil {
			return err
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
