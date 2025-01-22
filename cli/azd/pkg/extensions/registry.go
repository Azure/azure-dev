package extensions

import "encoding/json"

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
	Id          string                       `json:"id"`
	Namespace   string                       `json:"namespace,omitempty"`
	DisplayName string                       `json:"displayName"`
	Description string                       `json:"description"`
	Versions    []ExtensionVersion           `json:"versions"`
	Source      string                       `json:"source,omitempty"`
	Tags        []string                     `json:"tags,omitempty"`
	Platforms   map[string]map[string]string `json:"platforms,omitempty"`
}

// ExtensionDependency represents a dependency of an extension
type ExtensionDependency struct {
	Id      string `json:"id"`
	Version string `json:"version,omitempty"`
}

// ExtensionVersion represents a version of an extension
type ExtensionVersion struct {
	Version      string                       `json:"version"`
	Usage        string                       `json:"usage"`
	Examples     []ExtensionExample           `json:"examples"`
	Artifacts    map[string]ExtensionArtifact `json:"artifacts,omitempty"`
	Dependencies []ExtensionDependency        `json:"dependencies,omitempty"`
	EntryPoint   string                       `json:"entryPoint,omitempty"`
}

// ExtensionArtifact represents the artifact information of an extension
type ExtensionArtifact struct {
	URL                string            `json:"url"`
	Checksum           ExtensionChecksum `json:"checksum"`
	AdditionalMetadata map[string]any    `json:"-"`
}

type ExtensionChecksum struct {
	Algorithm string `json:"algorithm"`
	Value     string `json:"value"`
}

func (c ExtensionArtifact) MarshalJSON() ([]byte, error) {
	type Alias ExtensionArtifact

	baseMap := map[string]any{}
	data, err := json.Marshal(Alias(c))
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(data, &baseMap); err != nil {
		return nil, err
	}

	for k, v := range c.AdditionalMetadata {
		baseMap[k] = v
	}

	return json.Marshal(baseMap)
}

func (c *ExtensionArtifact) UnmarshalJSON(data []byte) error {
	// Create an alias type to avoid recursion
	type Alias ExtensionArtifact

	// Deserialize the known fields into the alias
	alias := Alias{}
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}

	// Copy the fields from the alias back into the struct
	*c = ExtensionArtifact(alias)

	// Deserialize the remaining fields into a map
	temp := make(map[string]interface{})
	if err := json.Unmarshal(data, &temp); err != nil {
		return err
	}

	// Remove known fields from the temp map
	delete(temp, "url")
	delete(temp, "checksum")

	// Convert the remaining fields to Extras
	c.AdditionalMetadata = map[string]any{}
	for k, v := range temp {
		if strValue, ok := v.(string); ok {
			c.AdditionalMetadata[k] = strValue
		}
	}

	return nil
}
