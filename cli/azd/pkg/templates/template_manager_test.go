package templates

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/resources"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var defaultTemplateSourceData = map[string]interface{}{
	"template": map[string]interface{}{
		"sources": map[string]interface{}{
			"default": map[string]interface{}{},
		},
	},
}

func Test_Templates_NewTemplateManager(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	templateManager, err := NewTemplateManager(
		NewSourceManager(
			NewSourceOptions(),
			mockContext.Container,
			config.NewUserConfigManager(config.NewFileConfigManager(config.NewManager())),
			mockContext.HttpClient,
		),
		mockContext.Console,
	)
	require.NoError(t, err)
	require.NotNil(t, templateManager)
}

func Test_Templates_ListTemplates(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	mockAwesomeAzdTemplateSource(mockContext)

	configManager := &mockUserConfigManager{}
	configManager.On("Load").Return(config.NewConfig(defaultTemplateSourceData), nil)

	templateManager, err := NewTemplateManager(
		NewSourceManager(NewSourceOptions(), mockContext.Container, configManager, mockContext.HttpClient),
		mockContext.Console,
	)
	require.NoError(t, err)

	templates, err := templateManager.ListTemplates(*mockContext.Context, nil)
	require.Greater(t, len(templates), 0)
	require.Nil(t, err)

	// Should be parsable JSON and non-empty
	var storedTemplates []Template
	err = json.Unmarshal(resources.TemplatesJson, &storedTemplates)
	require.NoError(t, err)
	require.NotEmpty(t, storedTemplates)
}

func Test_Templates_ListTemplates_WithTagFilter(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	mockAwesomeAzdTemplateSource(mockContext)

	configManager := &mockUserConfigManager{}
	configManager.On("Load").Return(config.NewConfig(defaultTemplateSourceData), nil)

	templateManager, err := NewTemplateManager(
		NewSourceManager(NewSourceOptions(), mockContext.Container, configManager, mockContext.HttpClient),
		mockContext.Console,
	)
	require.NoError(t, err)

	t.Run("WithMatchingTags", func(t *testing.T) {
		listOptions := &ListOptions{
			Tags: []string{"nodejs", "mongo"},
		}
		templates, err := templateManager.ListTemplates(*mockContext.Context, listOptions)
		require.Len(t, templates, 5)
		require.Nil(t, err)
	})

	t.Run("NoMatchingTags", func(t *testing.T) {
		listOptions := &ListOptions{
			Tags: []string{"foo", "bar"},
		}
		templates, err := templateManager.ListTemplates(*mockContext.Context, listOptions)
		require.Len(t, templates, 0)
		require.Nil(t, err)
	})
}

func Test_Templates_ListTemplates_SourceError(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	invalidUrl := "https://www.example.com/invalid.json"

	mockContext.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet && req.URL.String() == invalidUrl
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateEmptyHttpResponse(req, http.StatusNotFound)
	})

	configManager := &mockUserConfigManager{}
	config := config.NewConfig(nil)
	_ = config.Set(baseConfigKey, map[string]interface{}{
		"default": map[string]interface{}{},
		"invalid": map[string]interface{}{
			"type":     "url",
			"name":     "invalid",
			"location": invalidUrl,
		},
	})
	configManager.On("Load").Return(config, nil)

	templateManager, err := NewTemplateManager(
		NewSourceManager(NewSourceOptions(), mockContext.Container, configManager, mockContext.HttpClient),
		mockContext.Console,
	)
	require.NoError(t, err)

	// An invalid source should not cause an unrecoverable error
	templates, err := templateManager.ListTemplates(*mockContext.Context, nil)
	require.NotNil(t, templates)
	require.Greater(t, len(templates), 0)
	require.Nil(t, err)
}

func Test_Templates_GetTemplate_WithValidPath(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	configManager := &mockUserConfigManager{}
	configManager.On("Load").Return(config.NewConfig(defaultTemplateSourceData), nil)

	templateManager, err := NewTemplateManager(
		NewSourceManager(NewSourceOptions(), mockContext.Container, configManager, mockContext.HttpClient),
		mockContext.Console,
	)
	require.NoError(t, err)

	rel := "todo-nodejs-mongo"
	full := "Azure-Samples/" + rel
	template, err := templateManager.GetTemplate(*mockContext.Context, rel)
	assert.NoError(t, err)
	assert.Equal(t, rel, template.RepositoryPath)

	template, err = templateManager.GetTemplate(*mockContext.Context, full)
	assert.NoError(t, err)
	require.Equal(t, rel, template.RepositoryPath)
}

func Test_Templates_GetTemplate_WithInvalidPath(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	configManager := &mockUserConfigManager{}
	configManager.On("Load").Return(config.NewConfig(defaultTemplateSourceData), nil)

	templateManager, err := NewTemplateManager(
		NewSourceManager(NewSourceOptions(), mockContext.Container, configManager, mockContext.HttpClient),
		mockContext.Console,
	)
	require.NoError(t, err)

	templateName := "not-a-valid-template-name"
	template, err := templateManager.GetTemplate(*mockContext.Context, templateName)

	require.NotNil(t, err)
	require.Nil(t, template)
}

func Test_Templates_GetTemplate_WithNotFoundPath(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	configManager := &mockUserConfigManager{}
	configManager.On("Load").Return(config.NewConfig(defaultTemplateSourceData), nil)

	templateManager, err := NewTemplateManager(
		NewSourceManager(NewSourceOptions(), mockContext.Container, configManager, mockContext.HttpClient),
		mockContext.Console,
	)
	require.NoError(t, err)

	templateName := "not-a-valid-template-path"
	template, err := templateManager.GetTemplate(*mockContext.Context, templateName)

	require.NotNil(t, err)
	require.ErrorIs(t, err, ErrTemplateNotFound)
	require.Nil(t, template)
}
