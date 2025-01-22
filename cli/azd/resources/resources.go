package resources

import (
	"embed"
)

//go:embed templates.json
var TemplatesJson []byte

//go:embed alpha_features.yaml
var AlphaFeatures []byte

//go:embed minimal/main.bicep
var MinimalBicep []byte

//go:embed minimal/main.parameters.json
var MinimalBicepParameters []byte

//go:embed scaffold/base/*
var ScaffoldBase embed.FS

//go:embed scaffold/templates/*
var ScaffoldTemplates embed.FS

//go:embed apphost/templates/*
var AppHostTemplates embed.FS

//go:embed ai-python/*
var AiPythonApp embed.FS

//go:embed pipeline/*
var PipelineFiles embed.FS
