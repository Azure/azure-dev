package extensions

type Checksum struct {
	Algorithm string `json:"algorithm" yaml:"algorithm"`
	Value     string `json:"value"     yaml:"value"`
}

type Binary struct {
	Url      string    `json:"url"      yaml:"url"`
	Checksum *Checksum `json:"checksum" yaml:"checksum"`
}

type RegistryExtensionVersion struct {
	Version  string            `json:"version"  yaml:"version"`
	Usage    string            `json:"usage"    yaml:"usage"`
	Examples []string          `json:"examples" yaml:"examples"`
	Binaries map[string]Binary `json:"binaries" yaml:"binaries"` // Key: platform (windows, linux, macos)
}

type RegistryExtension struct {
	Name        string                     `json:"name"        yaml:"name"`
	DisplayName string                     `json:"displayName" yaml:"displayName"`
	Description string                     `json:"description" yaml:"description"`
	Versions    []RegistryExtensionVersion `json:"versions"    yaml:"versions"`
}

type ExtensionRegistry struct {
	Extensions []*RegistryExtension `json:"extensions" yaml:"extensions"`
	Signature  string               `json:"signature"  yaml:"signature"`
}
