package azdpath

import "path/filepath"

// EnvironmentConfigPath returns the path to the .azure directory in the root of the azd project.
func EnvironmentConfigPath(c *Root) string {
	return filepath.Join(c.Directory(), EnvironmentConfigDirectoryName)
}

// ProjectPath returns the path to the azure.yaml file in the root of the azd project.
func ProjectPath(c *Root) string {
	return filepath.Join(c.Directory(), ProjectFileName)
}
