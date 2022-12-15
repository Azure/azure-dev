package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/templates"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/exp/maps"
)

func TestTemplateList(t *testing.T) {
	var result bytes.Buffer
	templatesManager := templates.NewTemplateManager()
	templateList := newTemplatesListAction(
		&output.JsonFormatter{},
		&result,
		templatesManager,
	)

	_, err := templateList.Run(context.Background())
	require.NoError(t, err)

	// Should be parsable JSON and non-empty
	templates := make([]templates.Template, 0)
	err = json.Unmarshal(result.Bytes(), &templates)
	require.NoError(t, err)
	assert.NotEmpty(t, templates)

	// Should be sorted
	names := make([]string, 0, len(templates))
	for _, template := range templates {
		names = append(names, template.Name)
	}
	sorted := sort.StringsAreSorted(names)
	assert.True(t, sorted, "Templates are not sorted")

	// Should match what template manager shows
	templatesSet, err := templatesManager.ListTemplates()
	assert.NoError(t, err)
	assert.ElementsMatch(t, names, maps.Keys(templatesSet))
}
