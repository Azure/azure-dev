package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/templates"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemplateList(t *testing.T) {
	var result bytes.Buffer
	templatesManager, err := templates.NewTemplateManager(config.NewUserConfigManager())
	require.NoError(t, err)

	templateList := newTemplatesListAction(
		&templateListFlags{},
		&output.JsonFormatter{},
		&result,
		templatesManager,
	)

	_, err = templateList.Run(context.Background())
	require.NoError(t, err)

	// The result should be parsable JSON and non-empty
	storedTemplates := make([]templates.Template, 0)
	err = json.Unmarshal(result.Bytes(), &storedTemplates)
	require.NoError(t, err)
	assert.NotEmpty(t, storedTemplates)

	// Should match what template manager shows
	templates, err := templatesManager.ListTemplates(context.Background(), nil)
	assert.NoError(t, err)
	assert.Len(t, templates, len(storedTemplates))
	for i, template := range templates {
		assert.Equal(t, template.Name, storedTemplates[i].Name)
	}
}
