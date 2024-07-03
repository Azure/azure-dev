package project

type ResourceType string

const (
	ResourceTypeDbRedis    ResourceType = "db.redis"
	ResourceTypeDbPostgres ResourceType = "db.postgres"
	ResourceTypeDbMongo    ResourceType = "db.mongo"
)

func (r ResourceType) String() string {
	switch r {
	case ResourceTypeDbRedis:
		return "[Database] Redis"
	case ResourceTypeDbPostgres:
		return "[Database] PostgreSQL"
	case ResourceTypeDbMongo:
		return "[Database] MongoDB"
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
}
