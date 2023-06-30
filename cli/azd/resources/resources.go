package resources

import (
	_ "embed"
)

//go:embed templates.json
var TemplatesJson []byte

//go:embed alpha_features.yaml
var AlphaFeatures []byte

//go:embed minimal/main.bicep
var MinimalBicep []byte

//go:embed minimal/main.parameters.json
var MinimalBicepParameters []byte
