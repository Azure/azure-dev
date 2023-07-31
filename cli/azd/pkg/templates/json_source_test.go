package templates

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

var testTemplates []*Template = []*Template{
	{
		Name:           "template1",
		Description:    "Description of template 1",
		RepositoryPath: "path/to/template1",
	},
	{
		Name:           "template2",
		Description:    "Description of template 2",
		RepositoryPath: "path/to/template2",
	},
}

func jsonTemplates() string {
	jsonTemplates, err := json.Marshal(testTemplates)
	if err != nil {
		panic(err)
	}

	return string(jsonTemplates)
}

func Test_NewJsonTemplateSource(t *testing.T) {
	name := "test"
	source, err := NewJsonTemplateSource(name, jsonTemplates())
	require.Nil(t, err)

	require.Equal(t, name, source.Name())
}

func Test_NewJsonTemplateSource_InvalidJson(t *testing.T) {
	name := "test"
	jsonTemplates := `invalid json`
	_, err := NewJsonTemplateSource(name, jsonTemplates)
	require.Error(t, err)
}

func Test_JsonTemplateSource_ListTemplates(t *testing.T) {
	name := "test"
	source, err := NewJsonTemplateSource(name, jsonTemplates())
	require.Nil(t, err)

	templates, err := source.ListTemplates(context.Background())
	require.Nil(t, err)
	require.Equal(t, 2, len(templates))

	expectedTemplate1 := &Template{Name: "template1", RepositoryPath: "path/to/template1", Source: name}
	require.Equal(t, expectedTemplate1, templates[0])

	expectedTemplate2 := &Template{Name: "template2", RepositoryPath: "path/to/template2", Source: name}
	require.Equal(t, expectedTemplate2, templates[1])
}

func Test_JsonTemplateSource_GetTemplate_MatchFound(t *testing.T) {
	name := "test"
	source, err := NewJsonTemplateSource(name, jsonTemplates())
	require.Nil(t, err)

	template, err := source.GetTemplate(context.Background(), "path/to/template1")
	require.Nil(t, err)

	expectedTemplate := &Template{Name: "template1", RepositoryPath: "path/to/template1", Source: name}
	require.Equal(t, expectedTemplate, template)
}

func Test_JsonTemplateSource_GetTemplate_NoMatchFound(t *testing.T) {
	name := "test"
	source, err := NewJsonTemplateSource(name, jsonTemplates())
	require.Nil(t, err)

	template, err := source.GetTemplate(context.Background(), "path/to/template3")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrTemplateNotFound)

	require.Nil(t, template)
}
