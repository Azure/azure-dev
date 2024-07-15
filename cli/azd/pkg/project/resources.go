package project

import "fmt"

type ResourceType string

const (
	ResourceTypeDbRedis    ResourceType = "db.redis"
	ResourceTypeDbPostgres ResourceType = "db.postgres"
	ResourceTypeDbMongo    ResourceType = "db.mongo"
)

func (r ResourceType) String() string {
	switch r {
	case ResourceTypeDbRedis:
		return "Redis"
	case ResourceTypeDbPostgres:
		return "PostgreSQL"
	case ResourceTypeDbMongo:
		return "MongoDB"
	}

	return ""
}

func AllResources() []ResourceType {
	return []ResourceType{
		ResourceTypeDbRedis,
		ResourceTypeDbPostgres,
		ResourceTypeDbMongo,
	}
}

type ResourceConfig struct {
	// Reference to the parent project configuration
	Project *ProjectConfig `yaml:"-"`
	// Type of service
	Type ResourceType `yaml:"type"`
	// The friendly name/key of the project from the azure.yaml file
	Name string `yaml:"-"`
	// The optional bicep module override for the resource
	Module string `yaml:"module,omitempty"`
}

func (r *ResourceConfig) DefaultModule() (bicepModule string, bicepVersion string) {
	switch r.Type {
	case ResourceTypeDbMongo:
		bicepModule = "avm/res/document-db/database-account"
		bicepVersion = "0.4.0"
	case ResourceTypeDbPostgres:
		bicepModule = "avm/res/db-for-postgre-sql/flexible-server"
		bicepVersion = "0.1.6"
	case ResourceTypeDbRedis:
		bicepModule = "avm/res/cache/redis"
		bicepVersion = "0.3.2"
	default:
		panic(fmt.Sprintf("unsupported resource type %s", r.Type))
	}
	return
}
