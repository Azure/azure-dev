package contracts

type PlatformKind string

type PlatformConfig struct {
	Type   PlatformKind   `yaml:"type"`
	Config map[string]any `yaml:"config"`
}
