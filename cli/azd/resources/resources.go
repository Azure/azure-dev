package resources

import (
	"embed"
	_ "embed"
)

//go:embed templates.json
var TemplatesJson []byte

//go:embed app-types/*
var AppTypes embed.FS

//go:embed snippets/*
var Snippets embed.FS
