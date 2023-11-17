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
		RepositoryPath: "owner/template1",
	},
	{
		Name:           "template2",
		Description:    "Description of template 2",
		RepositoryPath: "owner/template2",
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
}

func Test_JsonTemplateSource_GetTemplate_MatchFound(t *testing.T) {
	name := "test"
	source, err := NewJsonTemplateSource(name, jsonTemplates())
	require.Nil(t, err)

	template, err := source.GetTemplate(context.Background(), "owner/template1")
	require.Nil(t, err)

	expectedTemplate := &Template{
		Name:           "template1",
		Description:    "Description of template 1",
		RepositoryPath: "owner/template1",
		Source:         name,
	}
	require.Equal(t, expectedTemplate, template)
}

func Test_JsonTemplateSource_GetTemplate_NoMatchFound(t *testing.T) {
	name := "test"
	source, err := NewJsonTemplateSource(name, jsonTemplates())
	require.Nil(t, err)

	template, err := source.GetTemplate(context.Background(), "owner/notfound")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrTemplateNotFound)

	require.Nil(t, template)
}
