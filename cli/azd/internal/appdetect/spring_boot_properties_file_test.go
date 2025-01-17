package appdetect

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
)

const postgresPropertiesOriginalContent = `spring.datasource.url=jdbc:postgresql://localhost:5432/dbname?sslmode=require
spring.datasource.username=admin
spring.datasource.password=secret
`

const postgresPropertiesUpdatedContent = `spring.datasource.url=` + placeholderPostgresUrl + `
spring.datasource.username=` + placeholderPostgresUsername + `
spring.datasource.password=` + placeholderPostgresPassword + `
`

var postgresPropertiesOriginalMap = []property{
	{"spring.datasource.url", "jdbc:postgresql://localhost:5432/dbname?sslmode=require"},
	{"spring.datasource.username", "admin"},
	{"spring.datasource.password", "secret"},
}

func TestAddPostgresqlConnectionProperties(t *testing.T) {
	tests := []struct {
		name                                    string
		inputApplicationPropertiesContent       string
		inputApplicationAzurePropertiesContent  string
		outputApplicationPropertiesContent      string
		outputApplicationAzurePropertiesContent string
	}{
		{
			name:                                    "no content",
			inputApplicationPropertiesContent:       "",
			inputApplicationAzurePropertiesContent:  "",
			outputApplicationPropertiesContent:      "spring.profiles.active=" + azureProfileName + "\n",
			outputApplicationAzurePropertiesContent: postgresPropertiesUpdatedContent,
		},
		{
			name:                                    "override original content",
			inputApplicationPropertiesContent:       "spring.profiles.active=" + azureProfileName,
			inputApplicationAzurePropertiesContent:  postgresPropertiesOriginalContent,
			outputApplicationPropertiesContent:      "spring.profiles.active=" + azureProfileName + "\n",
			outputApplicationAzurePropertiesContent: postgresPropertiesUpdatedContent,
		},
		{
			name:                                    "append original content",
			inputApplicationPropertiesContent:       "aaa=xxx",
			inputApplicationAzurePropertiesContent:  "bbb=yyy\n" + postgresPropertiesOriginalContent,
			outputApplicationPropertiesContent:      "aaa=xxx\n" + "spring.profiles.active=" + azureProfileName + "\n",
			outputApplicationAzurePropertiesContent: "bbb=yyy\n" + postgresPropertiesUpdatedContent,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir := t.TempDir()
			applicationPropertiesPath := filepath.Join(tempDir, applicationPropertiesRelativePath)
			createFileIfContentIsNotEmpty(t, applicationPropertiesPath, tt.inputApplicationPropertiesContent)
			applicationAzurePropertiesPath := filepath.Join(tempDir, applicationAzurePropertiesRelativePath)
			createFileIfContentIsNotEmpty(t, applicationAzurePropertiesPath, tt.inputApplicationAzurePropertiesContent)
			err := addPostgresqlConnectionProperties(tempDir)
			assert.NoError(t, err)
			assertFileContent(t, applicationPropertiesPath, tt.outputApplicationPropertiesContent)
			assertFileContent(t, applicationAzurePropertiesPath, tt.outputApplicationAzurePropertiesContent)
		})
	}
}

func createFileIfContentIsNotEmpty(t *testing.T, path string, content string) {
	if content == "" {
		return
	}

	dir := filepath.Dir(path)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		err := os.MkdirAll(dir, os.ModePerm)
		assert.NoError(t, err)
	}
	file, err := os.Create(path)
	assert.NoError(t, err)
	defer file.Close()
	err = os.WriteFile(path, []byte(content), 0600)
	assert.NoError(t, err)
}

func assertFileContent(t *testing.T, path string, content string) {
	actualContent, err := os.ReadFile(path)
	assert.NoError(t, err)
	assert.Equal(t, content, string(actualContent))
}

func TestReadProperties(t *testing.T) {
	tempFile, err := os.CreateTemp("", "test.properties")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tempFile.Name())

	if _, err := tempFile.Write([]byte(postgresPropertiesOriginalContent)); err != nil {
		t.Fatal(err)
	}
	if err := tempFile.Close(); err != nil {
		t.Fatal(err)
	}

	properties, err := readProperties(tempFile.Name())
	if err != nil {
		t.Fatalf("readProperties() error = %v", err)
	}

	if !reflect.DeepEqual(properties, postgresPropertiesOriginalMap) {
		t.Errorf("readProperties() = %v, want %v", properties, postgresPropertiesOriginalMap)
	}
}

func TestWriteProperties(t *testing.T) {
	tempFile, err := os.CreateTemp("", "test.properties")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tempFile.Name())

	if err := writeProperties(tempFile.Name(), postgresPropertiesOriginalMap); err != nil {
		t.Fatalf("writeProperties() error = %v", err)
	}

	content, err := os.ReadFile(tempFile.Name())
	if err != nil {
		t.Fatal(err)
	}

	if string(content) != postgresPropertiesOriginalContent {
		t.Errorf("writeProperties() = %v, want %v", string(content), postgresPropertiesOriginalContent)
	}
}
