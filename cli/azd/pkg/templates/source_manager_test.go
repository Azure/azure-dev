package templates

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func Test_sourceManager_List(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	configManager := &mockUserConfigManager{}
	sm := NewSourceManager(configManager, mockContext.HttpClient)

	config := config.NewConfig(nil)
	config.Set("template.sources", map[string]interface{}{
		"test": map[string]interface{}{
			"type":     "file",
			"location": "testdata/templates.json",
		},
	})
	configManager.On("Load").Return(config, nil)

	sources, err := sm.List(context.Background())
	require.Nil(t, err)

	require.Len(t, sources, 1)
	require.Equal(t, "test", sources[0].Key)
}

func Test_sourceManager_Get(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	configManager := &mockUserConfigManager{}
	sm := NewSourceManager(configManager, mockContext.HttpClient)

	config := config.NewConfig(nil)
	config.Set("template.sources", map[string]interface{}{
		"test": map[string]interface{}{
			"type":     "file",
			"location": "testdata/templates.json",
		},
	})
	configManager.On("Load").Return(config, nil)

	source, err := sm.Get(context.Background(), "test")
	require.Nil(t, err)
	require.NotNil(t, source)

	require.Equal(t, "test", source.Key)
}

func Test_sourceManager_Add(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	configManager := &mockUserConfigManager{}
	sm := NewSourceManager(configManager, mockContext.HttpClient)

	config := config.NewConfig(nil)
	config.Set("template.sources", map[string]interface{}{})
	configManager.On("Load").Return(config, nil)
	configManager.On("Save", mock.Anything).Return(nil)

	key := "test"
	source := &SourceConfig{
		Type:     SourceFile,
		Location: "testdata/templates.json",
	}
	err := sm.Add(context.Background(), key, source)
	require.Nil(t, err)
}

func Test_sourceManager_Remove(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	configManager := &mockUserConfigManager{}
	sm := NewSourceManager(configManager, mockContext.HttpClient)

	key := "test"
	config := config.NewConfig(nil)
	config.Set("template.sources.test", map[string]interface{}{})
	configManager.On("Load").Return(config, nil)
	configManager.On("Save", mock.Anything).Return(nil)

	err := sm.Remove(context.Background(), key)
	require.Nil(t, err)
}

func Test_sourceManager_CreateSource_InvalidType(t *testing.T) {
	config := &SourceConfig{
		Name: "test",
		Type: "invalid",
	}

	mockContext := mocks.NewMockContext(context.Background())
	configManager := &mockUserConfigManager{}
	sm := NewSourceManager(configManager, mockContext.HttpClient)

	_, err := sm.CreateSource(context.Background(), config)
	require.NotNil(t, err)
}

func Test_sourceManager_CreateSource_InvalidLocation(t *testing.T) {
	config := &SourceConfig{
		Name:     "test",
		Type:     SourceFile,
		Location: "invalid",
	}

	mockContext := mocks.NewMockContext(context.Background())
	configManager := &mockUserConfigManager{}
	sm := NewSourceManager(configManager, mockContext.HttpClient)

	_, err := sm.CreateSource(context.Background(), config)
	require.NotNil(t, err)
}

func Test_sourceManager_CreateSource_File(t *testing.T) {
	config := &SourceConfig{
		Name:     "test",
		Type:     SourceFile,
		Location: "testdata/templates.json",
	}

	mockContext := mocks.NewMockContext(context.Background())
	configManager := &mockUserConfigManager{}
	sm := NewSourceManager(configManager, mockContext.HttpClient)

	source, err := sm.CreateSource(context.Background(), config)
	require.Nil(t, err)

	require.Equal(t, config.Name, source.Name())
}

func Test_sourceManager_CreateSource_Url(t *testing.T) {
	config := &SourceConfig{
		Name:     "test",
		Type:     SourceUrl,
		Location: "https://raw.githubusercontent.com/github/gitignore/master/Python.gitignore",
	}

	mockContext := mocks.NewMockContext(context.Background())
	configManager := &mockUserConfigManager{}
	sm := NewSourceManager(configManager, mockContext.HttpClient)

	source, err := sm.CreateSource(context.Background(), config)
	require.Nil(t, err)

	require.Equal(t, config.Name, source.Name())
}

func Test_sourceManager_CreateSource_Resource(t *testing.T) {
	config := &SourceConfig{
		Name:     "test",
		Type:     SourceResource,
		Location: "",
	}

	mockContext := mocks.NewMockContext(context.Background())
	configManager := &mockUserConfigManager{}
	sm := NewSourceManager(configManager, mockContext.HttpClient)

	source, err := sm.CreateSource(context.Background(), config)
	require.Nil(t, err)

	require.Equal(t, config.Name, source.Name())
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
