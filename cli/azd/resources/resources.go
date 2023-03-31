package resources

import _ "embed"

//go:embed templates.json
var TemplatesJson []byte

//go:embed alpha_features.yaml
var AlphaFeatures []byte
