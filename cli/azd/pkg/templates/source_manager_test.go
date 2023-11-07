package templates

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func Test_sourceManager_List(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	configManager := &mockUserConfigManager{}
	sm := NewSourceManager(NewSourceOptions(), mockContext.Container, configManager, mockContext.HttpClient)

	config := config.NewConfig(nil)
	_ = config.Set("template.sources", map[string]interface{}{
		"test": map[string]interface{}{
			"type":     "file",
			"location": "testdata/templates.json",
		},
	})
	configManager.On("Load").Return(config, nil)

	sources, err := sm.List(*mockContext.Context)
	require.Nil(t, err)

	require.Len(t, sources, 1)
	require.Equal(t, "test", sources[0].Key)
}

// Test simulates an experience where user has explicitly removed all azd template sources
func Test_sourceManager_List_EmptySources(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	configManager := &mockUserConfigManager{}
	sm := NewSourceManager(NewSourceOptions(), mockContext.Container, configManager, mockContext.HttpClient)

	config := config.NewConfig(nil)
	_ = config.Set("template.sources", map[string]interface{}{})
	configManager.On("Load").Return(config, nil)

	// Empty source list should still return default azd template source
	sources, err := sm.List(*mockContext.Context)
	require.Nil(t, err)

	require.Len(t, sources, 0)
}

// Test simulates a first run experience where `template.sources` is not defined in the azd config
func Test_sourceManager_List_UndefinedSources(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	configManager := &mockUserConfigManager{}
	sm := NewSourceManager(NewSourceOptions(), mockContext.Container, configManager, mockContext.HttpClient)

	config := config.NewConfig(nil)
	configManager.On("Load").Return(config, nil)
	configManager.On("Save", mock.Anything).Return(nil)

	// Undefined source list should still return awesome azd template source
	sources, err := sm.List(*mockContext.Context)
	require.Nil(t, err)

	require.Len(t, sources, 1)
	require.Equal(t, SourceAwesomeAzd.Key, sources[0].Key)
}

func Test_sourceManager_Get(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	configManager := &mockUserConfigManager{}
	sm := NewSourceManager(NewSourceOptions(), mockContext.Container, configManager, mockContext.HttpClient)

	config := config.NewConfig(nil)
	_ = config.Set("template.sources", map[string]interface{}{
		"test": map[string]interface{}{
			"type":     "file",
			"location": "testdata/templates.json",
		},
	})
	configManager.On("Load").Return(config, nil)

	source, err := sm.Get(*mockContext.Context, "test")
	require.Nil(t, err)
	require.NotNil(t, source)

	require.Equal(t, "test", source.Key)
}

func Test_sourceManager_Add(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	configManager := &mockUserConfigManager{}
	sm := NewSourceManager(NewSourceOptions(), mockContext.Container, configManager, mockContext.HttpClient)

	config := config.NewConfig(defaultTemplateSourceData)
	configManager.On("Load").Return(config, nil)
	configManager.On("Save", mock.Anything).Return(nil)

	key := "test"
	source := &SourceConfig{
		Type:     SourceKindFile,
		Location: "testdata/templates.json",
	}
	err := sm.Add(*mockContext.Context, key, source)
	require.Nil(t, err)
}

func Test_sourceManager_Add_DuplicateKey(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	configManager := &mockUserConfigManager{}
	sm := NewSourceManager(NewSourceOptions(), mockContext.Container, configManager, mockContext.HttpClient)

	key := "test"
	config := config.NewConfig(nil)
	_ = config.Set("template.sources.test", map[string]interface{}{})
	configManager.On("Load").Return(config, nil)

	source := &SourceConfig{
		Type:     SourceKindFile,
		Location: "testdata/templates.json",
	}
	err := sm.Add(*mockContext.Context, key, source)
	require.NotNil(t, err)
	require.ErrorIs(t, err, ErrSourceExists)
}

func Test_sourceManager_Remove(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	configManager := &mockUserConfigManager{}
	sm := NewSourceManager(NewSourceOptions(), mockContext.Container, configManager, mockContext.HttpClient)

	key := "test"
	config := config.NewConfig(defaultTemplateSourceData)
	_ = config.Set("template.sources.test", map[string]interface{}{})
	configManager.On("Load").Return(config, nil)
	configManager.On("Save", mock.Anything).Return(nil)

	err := sm.Remove(*mockContext.Context, key)
	require.Nil(t, err)
}

func Test_sourceManager_Remove_SourceNotFound(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	configManager := &mockUserConfigManager{}
	sm := NewSourceManager(NewSourceOptions(), mockContext.Container, configManager, mockContext.HttpClient)

	key := "invalid"
	config := config.NewConfig(defaultTemplateSourceData)
	configManager.On("Load").Return(config, nil)

	err := sm.Remove(*mockContext.Context, key)
	require.NotNil(t, err)
	require.ErrorIs(t, err, ErrSourceNotFound)
}

func Test_sourceManager_CreateSource(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	mockAwesomeAzdTemplateSource(mockContext)

	configManager := &mockUserConfigManager{}
	sm := NewSourceManager(NewSourceOptions(), mockContext.Container, configManager, mockContext.HttpClient)

	configDir, err := config.GetUserConfigDir()
	require.NoError(t, err)

	path := filepath.Join(configDir, "test-templates.json")
	err = os.WriteFile(path, []byte(jsonTemplates()), osutil.PermissionFile)
	require.Nil(t, err)

	sourceUrl := "https://example.com/valid.json"
	mockContext.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet && req.URL.String() == sourceUrl
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK, testTemplates)
	})

	configs := []*SourceConfig{
		{
			Key:      "test-file",
			Type:     SourceKindFile,
			Name:     "file",
			Location: "./test-templates.json",
		},
		{
			Key:      "test-url",
			Type:     SourceKindUrl,
			Name:     "url",
			Location: sourceUrl,
		},
		{
			Key:  "default",
			Type: SourceKindResource,
			Name: "default",
		},
		{
			Key:  "awesome-azd",
			Type: SourceKindAwesomeAzd,
			Name: "awesome-azd",
		},
		{
			Key:  "invalid",
			Type: "invalid",
			Name: "invalid",
		},
	}

	for _, config := range configs {
		t.Run(config.Name, func(t *testing.T) {
			source, err := sm.CreateSource(*mockContext.Context, config)
			if config.Type == "invalid" {
				require.NotNil(t, err)
				require.ErrorIs(t, err, ErrSourceTypeInvalid)
				require.Nil(t, source)
			} else {
				require.Nil(t, err)
				require.NotNil(t, source)
			}
		})
	}
}

type mockUserConfigManager struct {
	mock.Mock
}

func (m *mockUserConfigManager) Load() (config.Config, error) {
	args := m.Called()
	return args.Get(0).(config.Config), args.Error(1)
}

func (m *mockUserConfigManager) Save(config config.Config) error {
	args := m.Called(config)
	return args.Error(0)
}
