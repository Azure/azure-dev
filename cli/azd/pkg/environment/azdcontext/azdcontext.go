package azdcontext

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

const ProjectFileName = "azure.yaml"
const EnvironmentDirectoryName = ".azure"
const ConfigFileName = "config.json"
const ConfigFileVersion = 1
const InfraDirectoryName = "infra"

type AzdContext struct {
	projectDirectory string
}

type EnvironmentView struct {
	Name       string
	IsDefault  bool
	DotEnvPath string
}

func (c *AzdContext) ProjectDirectory() string {
	return c.projectDirectory
}

func (c *AzdContext) SetProjectDirectory(dir string) {
	c.projectDirectory = dir
}

func (c *AzdContext) ProjectPath() string {
	return filepath.Join(c.ProjectDirectory(), ProjectFileName)
}

func (c *AzdContext) EnvironmentDirectory() string {
	return filepath.Join(c.ProjectDirectory(), EnvironmentDirectoryName)
}

func (c *AzdContext) InfrastructureDirectory() string {
	return filepath.Join(c.ProjectDirectory(), InfraDirectoryName)
}

func (c *AzdContext) GetDefaultProjectName() string {
	return filepath.Base(c.ProjectDirectory())
}

func (c *AzdContext) GetEnvironmentFilePath(name string) string {
	return filepath.Join(c.EnvironmentDirectory(), name, ".env")
}

// BicepModulePath gets the path to the bicep file for a given module.
func (c *AzdContext) BicepModulePath(module string) string {
	return filepath.Join(c.InfrastructureDirectory(), module+".bicep")
}

// BicepParameters reads the parameters from the deployment parameter file for a module in
// an environment.
func (c *AzdContext) BicepParameters(env string, module string) (map[string]interface{}, error) {
	byts, err := os.ReadFile(c.BicepParametersFilePath(env, module))
	switch {
	case errors.Is(err, os.ErrNotExist):
		return make(map[string]interface{}), nil
	case err != nil:
		return nil, fmt.Errorf("reading parameters file: %w", err)
	}

	var unmarshalled map[string]interface{}
	err = json.Unmarshal(byts, &unmarshalled)
	if err != nil {
		return nil, fmt.Errorf("unmarshalling parameters file: %w", err)
	}

	ret := make(map[string]interface{})
	for key, value := range unmarshalled["parameters"].(map[string]interface{}) {
		ret[key] = (value.(map[string]interface{}))["value"]
	}

	return ret, nil
}

// BicepParametersTemplateFilePath gets the path to the deployment parameter file template for
// a module.
func (c *AzdContext) BicepParametersTemplateFilePath(module string) string {
	return filepath.Join(c.InfrastructureDirectory(), module+".parameters.json")
}

// BicepParametersFilePath gets the path to the deployment parameter files for a module in
// an environment.
func (c *AzdContext) BicepParametersFilePath(env string, module string) string {
	return filepath.Join(c.EnvironmentDirectory(), env, module+".parameters.json")
}

// WriteBicepParameters creates a deployment parameters file which may be passed to the az CLI to
// set parameters for a deployment. The file is scoped to a given environment.
func (c *AzdContext) WriteBicepParameters(env string, module string, parameters map[string]interface{}) error {
	doc := make(map[string]interface{})
	doc["$schema"] = "https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json#"
	doc["contentVersion"] = "1.0.0.0"

	doc["parameters"] = make(map[string]interface{})
	for name, value := range parameters {
		valueObj := make(map[string]interface{})
		valueObj["value"] = value
		(doc["parameters"].(map[string]interface{}))[name] = valueObj
	}

	byts, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling parameters: %w", err)
	}

	err = os.WriteFile(c.BicepParametersFilePath(env, module), byts, osutil.PermissionFile)
	if err != nil {
		return fmt.Errorf("writing parameters file: %w", err)
	}

	return nil
}

func (c *AzdContext) GetEnvironmentWorkDirectory(name string) string {
	return filepath.Join(c.GetEnvironmentFilePath(name), "wd")
}

func (c *AzdContext) GetInfrastructurePath() string {
	return filepath.Join(c.ProjectDirectory(), InfraDirectoryName)
}

func (c *AzdContext) ListEnvironments() ([]EnvironmentView, error) {
	defaultEnv, err := c.GetDefaultEnvironmentName()
	if err != nil {
		return nil, err
	}

	ents, err := os.ReadDir(c.EnvironmentDirectory())
	if err != nil {
		return nil, fmt.Errorf("listing entries: %w", err)
	}

	var envs []EnvironmentView
	for _, ent := range ents {
		if ent.IsDir() {
			ev := EnvironmentView{
				Name:       ent.Name(),
				IsDefault:  ent.Name() == defaultEnv,
				DotEnvPath: c.GetEnvironmentFilePath(ent.Name()),
			}
			envs = append(envs, ev)
		}
	}

	sort.Slice(envs, func(i, j int) bool {
		return envs[i].Name < envs[j].Name
	})
	return envs, nil
}

// GetDefaultEnvironmentName returns the name of the default environment. Returns
// an empty string if a default environment has not been set.
func (c *AzdContext) GetDefaultEnvironmentName() (string, error) {
	path := filepath.Join(c.EnvironmentDirectory(), ConfigFileName)
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

func (c *AzdContext) SetDefaultEnvironmentName(name string) error {
	path := filepath.Join(c.EnvironmentDirectory(), ConfigFileName)
	byts, err := json.Marshal(configFile{
		Version:            ConfigFileVersion,
		DefaultEnvironment: name,
	})
	if err != nil {
		return fmt.Errorf("serializing config file: %w", err)
	}

	if err := os.WriteFile(path, byts, osutil.PermissionFile); err != nil {
		return fmt.Errorf("writing config file: %w", err)
	}

	return nil
}

var ErrEnvironmentExists = errors.New("environment already exists")

func (c *AzdContext) NewEnvironment(name string) error {
	if err := os.MkdirAll(c.EnvironmentDirectory(), osutil.PermissionDirectory); err != nil {
		return fmt.Errorf("creating environment root: %w", err)
	}

	if err := os.Mkdir(filepath.Join(c.EnvironmentDirectory(), name), osutil.PermissionDirectory); err != nil {
		if errors.Is(err, os.ErrExist) {
			return ErrEnvironmentExists
		}

		return fmt.Errorf("creating environment directory: %w", err)
	}

	return nil
}

func NewAzdContext() (*AzdContext, error) {
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

	return &AzdContext{
		projectDirectory: searchDir,
	}, nil
}

type configFile struct {
	Version            int    `json:"version"`
	DefaultEnvironment string `json:"defaultEnvironment"`
}

type contextKey string

const (
	azdContextKey contextKey = "azd"
)

func WithAzdContext(ctx context.Context, azContext *AzdContext) context.Context {
	return context.WithValue(ctx, azdContextKey, azContext)
}

// GetAzdContext attempts to retrieve the AzdContext from the go context
func GetAzdContext(ctx context.Context) (*AzdContext, error) {
	azdCtx, ok := ctx.Value(azdContextKey).(*AzdContext)
	if !ok {
		return nil, errors.New("cannot find AzdContext on go context")
	}

	return azdCtx, nil
}
