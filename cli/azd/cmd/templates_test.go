package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
	"testing"
	"text/template"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/templates"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemplateList(t *testing.T) {
	var result bytes.Buffer
	templateList := newTemplatesListAction(
		templatesListFlags{
			outputFormat: string(output.JsonFormat),
			global:       &internal.GlobalCommandOptions{},
		},
		&output.JsonFormatter{},
		&result,
		templates.NewTemplateManager(),
	)

	err := templateList.Run(context.Background())
	require.NoError(t, err)

	templates := make([]template.Template, 0)
	err = json.Unmarshal(result.Bytes(), &templates)
	require.NoError(t, err)
	assert.NotEmpty(t, templates)

	names := make([]string, 0, len(templates))
	for _, template := range templates {
		names = append(names, template.Name())
	}
	sort.StringsAreSorted(names)
}
