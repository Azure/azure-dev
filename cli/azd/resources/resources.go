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

//go:embed scaffold/templates/api.bicept
var ApiBicepTempl []byte

//go:embed scaffold/templates/main.bicept
var MainBicepTempl []byte

//go:embed scaffold/templates/main.parameters.jsont
var MainParametersTempl []byte

//go:embed scaffold/base/*
var ScaffoldBase embed.FS

//go:embed scaffold/templates/db-cosmos.bicept
var DbCosmosTempl []byte

//go:embed scaffold/templates/db-postgres.bicept
var DbPostgresTempl []byte
