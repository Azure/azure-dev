package templates

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewTemplateManager(t *testing.T) {
	templateManager := NewTemplateManager()

	require.NotNil(t, templateManager)
}

func TestListTemplates(t *testing.T) {
	templateManager := NewTemplateManager()
	templates, err := templateManager.ListTemplates()

	require.Greater(t, len(templates), 0)
	require.Nil(t, err)
}

func TestGetTemplateWithValidName(t *testing.T) {
	templateName := "Azure-Samples/todo-nodejs-mongo"
	templateManager := NewTemplateManager()
	template, err := templateManager.GetTemplate(templateName)

	require.NotNil(t, template)
	require.Equal(t, template.Name, templateName)
	require.Nil(t, err)
}

func TestGetTemplateWithInvalidName(t *testing.T) {
	templateName := "not-a-valid-template-name"
	templateManager := NewTemplateManager()
	template, err := templateManager.GetTemplate(templateName)

	require.Equal(t, template, Template{})
	require.NotNil(t, err)
	require.Equal(t, err.Error(), fmt.Sprintf("template with name '%s' was not found", templateName))
}
