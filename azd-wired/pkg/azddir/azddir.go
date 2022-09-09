package azddir

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const ProjectFileName = "azure.yaml"
const EnvironmentDirectoryName = ".azure"
const ConfigFileName = "config.json"
const ConfigFileVersion = 1
const InfraDirectoryName = "infra"

type AzdDirectoryService struct {
	projectDirectory string
}

type EnvironmentView struct {
	Name       string
	IsDefault  bool
	DotEnvPath string
}

func (c *AzdDirectoryService) ProjectDirectory() string {
	return c.projectDirectory
}

func (c *AzdDirectoryService) SetProjectDirectory(dir string) {
	c.projectDirectory = dir
}

func (c *AzdDirectoryService) ProjectPath() string {
	return filepath.Join(c.ProjectDirectory(), ProjectFileName)
}

func (c *AzdDirectoryService) EnvironmentDirectory() string {
	return filepath.Join(c.ProjectDirectory(), EnvironmentDirectoryName)
}

func (c *AzdDirectoryService) InfrastructureDirectory() string {
	return filepath.Join(c.ProjectDirectory(), InfraDirectoryName)
}

func (c *AzdDirectoryService) GetDefaultProjectName() string {
	return filepath.Base(c.ProjectDirectory())
}

func (c *AzdDirectoryService) GetEnvironmentFilePath(name string) string {
	return filepath.Join(c.EnvironmentDirectory(), name, ".env")
}

func (c *AzdDirectoryService) GetEnvironmentWorkDirectory(name string) string {
	return filepath.Join(c.GetEnvironmentFilePath(name), "wd")
}

func (c *AzdDirectoryService) GetInfrastructurePath() string {
	return filepath.Join(c.ProjectDirectory(), InfraDirectoryName)
}

func New() (*AzdDirectoryService, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get the current directory: %w", err)
	}

	// Walk up from the CWD to the root, looking for a project file. If we find one, that's
	// the root for our project. If we don't use the CWD as the root (as it's the place that
	// we would `init` into).
	searchDir, err := filepath.Abs(wd)
	if err != nil {
		return nil, fmt.Errorf("resolving path: %w", err)
	}

	for {
		projectFilePath := filepath.Join(searchDir, ProjectFileName)
		stat, err := os.Stat(projectFilePath)
		if os.IsNotExist(err) || (err == nil && stat.IsDir()) {
			parent := filepath.Dir(searchDir)
			if parent == searchDir {
				// hit the root without finding anything. Behave as if we had
				// found an `azure.yaml` file in the CWD.
				searchDir = wd
				break
			}
			searchDir = parent
		} else if err == nil {
			// We found our azure.yaml file, and searchDir is the directory
			// that contains it.
			break
		} else {
			return nil, fmt.Errorf("searching for project file: %w", err)
		}
	}

	if err := ensureProject(searchDir); err != nil {
		return nil, err
	}

	return &AzdDirectoryService{
		projectDirectory: searchDir,
	}, nil
}

var (
	errNoProject = errors.New("no project exists; to create a new project, run `azd init`.")
)

// ensureProject ensures that a project file exists, using the given
// context. If a project is missing, errNoProject is returned.
func ensureProject(path string) error {
	_, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return errNoProject
	} else if err != nil {
		return fmt.Errorf("checking for project: %w", err)
	}

	return nil
}
