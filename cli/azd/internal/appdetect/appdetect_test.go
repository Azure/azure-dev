package appdetect

import (
	"context"
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/bmatcuk/doublestar/v4"
	"github.com/stretchr/testify/require"
)

//go:embed testdata/*
var testDataFs embed.FS

// Verify standard detection for all languages and dependencies.
func TestDetect(t *testing.T) {
	dir := t.TempDir()
	err := copyTestDataDir(t, "**", dir)
	require.NoError(t, err)

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
					DetectionRule: "Inferred by presence of: dotnettestapp.csproj, Program.cs",
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
						JsVite,
					},
					DatabaseDeps: []DatabaseDep{
						DbMongo,
						DbMySql,
						DbPostgres,
						DbRedis,
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
						DbRedis,
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
					DetectionRule: "Inferred by presence of: dotnettestapp.csproj, Program.cs",
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
					DetectionRule: "Inferred by presence of: dotnettestapp.csproj, Program.cs",
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
					DetectionRule: "Inferred by presence of: dotnettestapp.csproj, Program.cs",
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
			projects, err := Detect(context.Background(), dir, tt.options...)
			require.NoError(t, err)

			// Convert relative to absolute paths
			for i := range tt.want {
				tt.want[i].Path = filepath.Join(dir, tt.want[i].Path)
			}

			require.Equal(t, tt.want, projects)
		})
	}
}

// Verify docker detection.
func TestDetectDocker(t *testing.T) {
	dir := t.TempDir()
	err := copyTestDataDir(t, "**/dotnet/**", dir)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(dir, "dotnet", "Dockerfile"), []byte{}, 0600)
	require.NoError(t, err)

	projects, err := Detect(context.Background(), dir)
	require.NoError(t, err)

	require.Len(t, projects, 1)
	require.Equal(t, projects[0], Project{
		Language:      DotNet,
		Path:          filepath.Join(dir, "dotnet"),
		DetectionRule: "Inferred by presence of: dotnettestapp.csproj, Program.cs",
		Docker: &Docker{
			Path: filepath.Join(dir, "dotnet", "Dockerfile"),
		},
	})
}

// Verifies detection of nested projects.
func TestDetectNested(t *testing.T) {
	dir := t.TempDir()

	// Use 'src' under root to create further nesting
	src := filepath.Join(dir, "src")
	err := copyTestDataDir(t, "**/dotnet/**", src)
	require.NoError(t, err)

	// nested directory, but is skipped because of dotnet being one level up
	err = copyTestDataDir(t, "**/javascript/**", filepath.Join(src, "dotnet"))
	require.NoError(t, err)

	projects, err := Detect(context.Background(), dir)
	require.NoError(t, err)

	require.Len(t, projects, 1)
	require.Equal(t, projects[0], Project{
		Language:      DotNet,
		Path:          filepath.Join(src, "dotnet"),
		DetectionRule: "Inferred by presence of: dotnettestapp.csproj, Program.cs",
	})
}

func copyTestDataDir(t *testing.T, glob string, dst string) error {
	root := "testdata"
	return fs.WalkDir(testDataFs, root, func(name string, d fs.DirEntry, err error) error {
		// If there was some error that was preventing is from walking into the directory, just fail now,
		// not much we can do to recover.
		if err != nil {
			return err
		}
		rel := name[len(root):]
		match, err := doublestar.Match(glob, rel)
		if err != nil {
			return err
		}

		if !match {
			return nil
		}

		targetPath := filepath.Join(dst, rel)

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
