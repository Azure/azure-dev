package appdetect

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/stretchr/testify/require"
)

//go:embed testdata/*
var testDataFs embed.FS

func TestDetect(t *testing.T) {
	tests := []struct {
		name    string
		options []DetectOption
		want    []Project
	}{
		{
			"Full",
			nil,
			[]Project{
				{
					Language:      DotNet,
					Path:          "dotnet",
					DetectionRule: "Inferred by presence of: program.cs, dotnettestapp.csproj",
				},
				{
					Language:      Java,
					Path:          "java",
					DetectionRule: "Inferred by presence of: pom.xml",
				},
				{
					Language:      JavaScript,
					Path:          "javascript",
					DetectionRule: "Inferred by presence of: package.json",
				},
				{
					Language:      JavaScript,
					Path:          "javascript-full",
					DetectionRule: "Inferred by presence of: package.json",
					Dependencies: []Dependency{
						JsAngular,
						JsJQuery,
						JsReact,
						JsVue,
					},
					DatabaseDeps: []DatabaseDep{
						DbMongo,
						DbMySql,
						DbPostgres,
						DbSqlServer,
					},
				},
				{
					Language:      Python,
					Path:          "python",
					DetectionRule: "Inferred by presence of: requirements.txt",
				},
				{
					Language:      Python,
					Path:          "python-full",
					DetectionRule: "Inferred by presence of: requirements.txt",
					Dependencies: []Dependency{
						PyDjango,
						PyFastApi,
						PyFlask,
					},
					DatabaseDeps: []DatabaseDep{
						DbMongo,
						DbMySql,
						DbPostgres,
					},
				},
				{
					Language:      TypeScript,
					Path:          "typescript",
					DetectionRule: "Inferred by presence of: package.json",
				},
			},
		},
		{
			"IncludeExcludeLanguages",
			[]DetectOption{
				WithDotNet(),
				WithJava(),
				WithJavaScript(),
				WithoutJavaScript(),
			},
			[]Project{
				{
					Language:      DotNet,
					Path:          "dotnet",
					DetectionRule: "Inferred by presence of: program.cs, dotnettestapp.csproj",
				},
				{
					Language:      Java,
					Path:          "java",
					DetectionRule: "Inferred by presence of: pom.xml",
				},
			},
		},
		{
			"ExcludeLanguages",
			[]DetectOption{
				WithoutJavaScript(),
				WithoutPython(),
			},
			[]Project{
				{
					Language:      DotNet,
					Path:          "dotnet",
					DetectionRule: "Inferred by presence of: program.cs, dotnettestapp.csproj",
				},
				{
					Language:      Java,
					Path:          "java",
					DetectionRule: "Inferred by presence of: pom.xml",
				},
			},
		},
		{
			"ExcludePatterns",
			[]DetectOption{
				WithExcludePatterns([]string{
					"**/*-full",
					"**/javascript",
					"typescript",
				}, false),
			},
			[]Project{

				{
					Language:      DotNet,
					Path:          "dotnet",
					DetectionRule: "Inferred by presence of: program.cs, dotnettestapp.csproj",
				},
				{
					Language:      Java,
					Path:          "java",
					DetectionRule: "Inferred by presence of: pom.xml",
				},
				{
					Language:      Python,
					Path:          "python",
					DetectionRule: "Inferred by presence of: requirements.txt",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			err := copyTestDataDir(t, "**", dir)
			require.NoError(t, err)

			projects, err := Detect(dir, tt.options...)
			require.NoError(t, err)

			// Convert relative to absolute paths
			for i := range tt.want {
				tt.want[i].Path = filepath.Join(dir, tt.want[i].Path)
			}

			require.Equal(t, tt.want, projects)
		})
	}
}

func copyTestDataDir(t *testing.T, glob string, dst string) error {
	root := "testdata"
	return fs.WalkDir(testDataFs, root, func(name string, d fs.DirEntry, err error) error {
		// If there was some error that was preventing is from walking into the directory, just fail now,
		// not much we can do to recover.
		if err != nil {
			return err
		}
		targetPath := filepath.Join(dst, name[len(root):])

		if d.IsDir() {
			return os.MkdirAll(targetPath, osutil.PermissionDirectory)
		}

		contents, err := fs.ReadFile(testDataFs, name)
		if err != nil {
			return err
		}

		return os.WriteFile(targetPath, contents, osutil.PermissionFile)
	})
}
