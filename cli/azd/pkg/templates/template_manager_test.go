package templates

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/resources"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTemplateManager(t *testing.T) {
	templateManager, err := NewTemplateManager(NewSourceManager(config.NewUserConfigManager()))
	require.NoError(t, err)
	require.NotNil(t, templateManager)
}

func TestListTemplates(t *testing.T) {
	templateManager, err := NewTemplateManager(NewSourceManager(config.NewUserConfigManager()))
	require.NoError(t, err)
	templates, err := templateManager.ListTemplates(context.Background(), nil)

	require.Greater(t, len(templates), 0)
	require.Nil(t, err)

	// Should be parsable JSON and non-empty
	var storedTemplates []Template
	err = json.Unmarshal(resources.TemplatesJson, &storedTemplates)
	require.NoError(t, err)
	require.NotEmpty(t, storedTemplates)

	// Should match what is stored in current resources JSON
	// This also tests that the templates are in the precise order we defined
	assert.NoError(t, err)
	assert.Len(t, templates, len(storedTemplates))
	for i, template := range templates {
		assert.Equal(t, template.Name, storedTemplates[i].Name)
		assert.Equal(t, template.Description, storedTemplates[i].Description)
		assert.Equal(t, template.RepositoryPath, storedTemplates[i].RepositoryPath)
	}

	// Try listing multiple times to naively verify that the list is in a deterministic order
	for i := 0; i < 10; i++ {
		templates, err = templateManager.ListTemplates(context.Background(), nil)
		assert.NoError(t, err)
		assert.Len(t, templates, len(storedTemplates))
		for i, template := range templates {
			assert.Equal(t, template.Name, storedTemplates[i].Name)
		}
	}
}

func TestGetTemplateWithValidPath(t *testing.T) {
	rel := "todo-nodejs-mongo"
	full := "Azure-Samples/" + rel
	templateManager, err := NewTemplateManager(NewSourceManager(config.NewUserConfigManager()))
	require.NoError(t, err)
	template, err := templateManager.GetTemplate(context.Background(), rel)
	assert.NoError(t, err)
	assert.Equal(t, rel, template.RepositoryPath)

	template, err = templateManager.GetTemplate(context.Background(), full)
	assert.NoError(t, err)
	require.Equal(t, rel, template.RepositoryPath)
}

func TestGetTemplateWithInvalidPath(t *testing.T) {
	templateName := "not-a-valid-template-name"
	templateManager, err := NewTemplateManager(NewSourceManager(config.NewUserConfigManager()))
	require.NoError(t, err)
	template, err := templateManager.GetTemplate(context.Background(), templateName)

	require.Equal(t, template, Template{})
	require.NotNil(t, err)
	require.Equal(t, err.Error(), fmt.Sprintf("template with name '%s' was not found", templateName))
}
