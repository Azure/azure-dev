package azdpath

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

// configFileName is the name of the file that stores the default environment and is located in the root of the .azure
// directory.
const configFileName = "config.json"

// configFileVersion is the version of the config file format that we understand and write.
const configFileVersion = 1

// Root is wrapper around the root of an azd project on a file system. It is the directory that contains the azure.yaml
// and .azure folder. Create a *Root with [FindRoot] or [FindRootFromWd] which will search for a project file
// to determine the root directory or use [NewRootFromDirectory] if you know the path to the project root already.
//
// `azd init` creates a new Root in the current directory by creating a new [ProjectFileName] (i.e. azure.yaml) file
// (or fetching one as part of a template) and creates the [EnvironmentConfigDirectoryName] (i.e. .azure) directory.
type Root string

// Directory is the the file system path of the directory that contains .azure and azure.yaml.
func (c *Root) Directory() string {
	return string(*c)
}

// DefaultEnvironmentName returns the name of the default environment or an empty string if no default environment is set.
func (c *Root) DefaultEnvironmentName() (string, error) {
	path := filepath.Join(EnvironmentConfigPath(c), configFileName)
	file, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "", nil
	case err != nil:
		return "", fmt.Errorf("reading config file: %w", err)
	}

	var config configFile
	if err := json.Unmarshal(file, &config); err != nil {
		return "", fmt.Errorf("deserializing config file: %w", err)
	}

	return config.DefaultEnvironment, nil
}

// SetDefaultEnvironmentName saves the environment that is used by default when azd is run without a `-e` flag. Using "" as
// the name will cause azd to prompt the user to select an environment in the future.
func (c *Root) SetDefaultEnvironmentName(name string) error {
	path := filepath.Join(EnvironmentConfigPath(c), configFileName)
	config := configFile{
		Version:            configFileVersion,
		DefaultEnvironment: name,
	}

	bytes, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("serializing config file: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), osutil.PermissionDirectory); err != nil {
		return fmt.Errorf("creating environment root: %w", err)
	}

	if err := os.WriteFile(path, bytes, osutil.PermissionFile); err != nil {
		return fmt.Errorf("writing config file: %w", err)
	}

	// make sure to ignore the environment directory
	path = filepath.Join(EnvironmentConfigPath(c), ".gitignore")
	return os.WriteFile(path, []byte("# .azure is not intended to be committed\n*"), osutil.PermissionFile)
}

// Creates context with project directory set to the desired directory.
func NewRootFromDirectory(projectRoot string) *Root {
	return to.Ptr(Root(projectRoot))
}

var (
	// ErrNoProject is returned by NewRootFromWd when no project file is found.
	ErrNoProject = errors.New("no project exists; to create a new project, run `azd init`")
)

// FindRoot calls FindRootFromWd with the current working directory as returned by [os.Getwd].
func FindRoot() (*Root, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get the current directory: %w", err)
	}

	return FindRootFromWd(wd)
}

// FindRootFromWd searches upwards from the given directory to find the root of an existing azd project. The root is
// determined by the presence of a project file (i.e. azure.yaml) in the directory. If a project is not found in the current
// directory, the parent directory is searched recursively up to the root. If no project file is found, an error that matches
// [ErrNoProject] with [errors.Is] is returned.
func FindRootFromWd(wd string) (*Root, error) {
	// Walk up from the wd to the root, looking for a project file. If we find one, that's
	// the root project directory.
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
				return nil, ErrNoProject
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

	return to.Ptr(Root(searchDir)), nil
}

// configFile is the model type for the config file that is stored in the root of the .azure directory. It can be read and
// written using the json.Marshal and json.Unmarshal functions.
type configFile struct {
	Version            int    `json:"version"`
	DefaultEnvironment string `json:"defaultEnvironment,omitempty"`
}
