package contracts

type StateConfig struct {
	Remote *RemoteConfig `json:"remote" yaml:"remote"`
}

// RemoteConfig is the state configuration for a remote backend
type RemoteConfig struct {
	Backend string         `json:"backend" yaml:"backend"`
	Config  map[string]any `json:"config"  yaml:"config"`
}
