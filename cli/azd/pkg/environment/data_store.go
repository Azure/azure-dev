package environment

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
)

// DataStore is the interface for the interacting with the persistent storage of environments.

type RemoteKind string

const (
	RemoteKindAzureBlobStorage RemoteKind = "AzureBlobStorage"
)

var ValidRemoteKinds = []string{
	string(RemoteKindAzureBlobStorage),
}

// SaveOptions provide additional metadata for the save operation
type SaveOptions struct {
	// Whether or not the environment is new
	IsNew bool
}

type DataStore interface {
	// Gets the path to the environment .env file
	EnvPath(env *Environment) string

	// Gets the path to the environment JSON config file
	ConfigPath(env *Environment) string

	// Gets a list of all environments within the stat store
	List(ctx context.Context) ([]*contracts.EnvListEnvironment, error)

	// Gets the environment instance for the specified environment name
	Get(ctx context.Context, name string) (*Environment, error)

	// Reloads the environment from the persistent data store
	Reload(ctx context.Context, env *Environment) error

	// Saves the environment to the persistent data store
	Save(ctx context.Context, env *Environment, options *SaveOptions) error

	// Deletes the environment from the persistent data store
	Delete(ctx context.Context, name string) error
}

type LocalDataStore DataStore
type RemoteDataStore DataStore
