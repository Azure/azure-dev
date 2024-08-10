package azdpath

// ProjectFileName is the name of the file that stores the project configuration. This file is located in the root of the
// and contains the project name and other project specific configuration.
const ProjectFileName = "azure.yaml"

// EnvironmentConfigDirectoryName is the name of the directory that contains environment specific configuration.
// This directory is located in the root of the azd project and is not intended to be committed. Inside this directory
// is a folder for each environment and a config.json in the root file that stores the default environment.
const EnvironmentConfigDirectoryName = ".azure"
