package azdpath

import "path/filepath"

// EnvironmentConfigPath returns the path to the .azure directory in a given Root.
func EnvironmentConfigPath(c *Root) string {
	return filepath.Join(c.Directory(), EnvironmentConfigDirectoryName)
}

// ProjectPath returns the path to the azure.yaml file in a given Root.
func ProjectPath(c *Root) string {
	return filepath.Join(c.Directory(), ProjectFileName)
}
