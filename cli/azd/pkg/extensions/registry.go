package extensions

type ExtensionExample struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Usage       string `json:"usage"`
}

// Registry represents the registry.json structure
type Registry struct {
	Extensions []*ExtensionMetadata `json:"extensions"`
	Signature  string               `json:"signature,omitempty"`
}

// Extension represents an extension in the registry
type ExtensionMetadata struct {
	Name        string             `json:"name"`
	Namespace   string             `json:"namespace,omitempty"`
	DisplayName string             `json:"displayName"`
	Description string             `json:"description"`
	Versions    []ExtensionVersion `json:"versions"`
	Source      string             `json:"source,omitempty"`
	Tags        []string           `json:"tags,omitempty"`
}

// ExtensionDependency represents a dependency of an extension
type ExtensionDependency struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// ExtensionVersion represents a version of an extension
type ExtensionVersion struct {
	Version      string                     `json:"version"`
	Usage        string                     `json:"usage"`
	Examples     []ExtensionExample         `json:"examples"`
	Binaries     map[string]ExtensionBinary `json:"binaries,omitempty"`
	Dependencies []ExtensionDependency      `json:"dependencies,omitempty"`
}

// ExtensionBinary represents the binary information of an extension
type ExtensionBinary struct {
	URL      string            `json:"url"`
	Checksum ExtensionChecksum `json:"checksum"`
}

type ExtensionChecksum struct {
	Algorithm string `json:"algorithm"`
	Value     string `json:"value"`
}
