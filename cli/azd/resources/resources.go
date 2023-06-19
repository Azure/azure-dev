package resources

import (
	"embed"
	_ "embed"
)

//go:embed templates.json
var TemplatesJson []byte

//go:embed alpha_features.yaml
var AlphaFeatures []byte

//go:embed app-types/*
var AppTypes embed.FS

//go:embed snippets/*
var Snippets embed.FS

//go:embed minimal/main.bicep
var MinimalBicep []byte

//go:embed minimal/main.parameters.json
var MinimalBicepParameters []byte
