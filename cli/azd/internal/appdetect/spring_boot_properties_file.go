package appdetect

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type property struct {
	key   string
	value string
}
type propertyMergeFunc func(string, string) string

const azureProfileName = "azure"

const placeholderPostgresUrl = "jdbc:${POSTGRES_URL}" // todo: manage placeholders here and in resources.bicept together.
const placeholderPostgresUsername = "${POSTGRES_USERNAME}"
const placeholderPostgresPassword = "${POSTGRES_PASSWORD}"

var postgresqlProperties = []property{
	{"spring.datasource.url", placeholderPostgresUrl},
	{"spring.datasource.username", placeholderPostgresUsername},
	{"spring.datasource.password", placeholderPostgresPassword},
}

var applicationPropertiesRelativePath = filepath.Join("src", "main", "resources",
	"application.properties")
var applicationAzurePropertiesRelativePath = filepath.Join("src", "main", "resources",
	"application-"+azureProfileName+".properties")

// todo: support other file suffix. Example: application.yml, application.yaml
func addPostgresqlConnectionProperties(projectPath string) error {
	err := addPostgresqlConnectionPropertiesIntoPropertyFile(projectPath)
	if err != nil {
		return err
	}
	return activeAzureProfile(projectPath)
}

func addPostgresqlConnectionPropertiesIntoPropertyFile(projectPath string) error {
	filePath := filepath.Join(projectPath, applicationAzurePropertiesRelativePath)
	return updatePropertyFile(filePath, postgresqlProperties, keepNewValue)
}

func keepNewValue(_ string, newValue string) string {
	return newValue
}

func activeAzureProfile(projectPath string) error {
	filePath := filepath.Join(projectPath, applicationPropertiesRelativePath)
	var newProperties = []property{
		{"spring.profiles.active", azureProfileName},
	}
	return updatePropertyFile(filePath, newProperties, appendIfExists)
}

func appendIfExists(originalValue string, newValue string) string {
	if originalValue == "" {
		return newValue
	}
	originalValues := strings.SplitN(originalValue, ",", -1)
	if contains(originalValues, azureProfileName) {
		return originalValue
	}
	return originalValue + "," + newValue
}

func contains(a []string, x string) bool {
	for _, n := range a {
		if x == n {
			return true
		}
	}
	return false
}

func updatePropertyFile(filePath string, newProperties []property, function propertyMergeFunc) error {
	err := createFileIfNotExist(filePath)
	if err != nil {
		return err
	}
	properties, err := readProperties(filePath)
	if err != nil {
		return err
	}
	properties = updateProperties(properties, newProperties, function)
	err = writeProperties(filePath, properties)
	if err != nil {
		return err
	}
	return nil
}

func createFileIfNotExist(filePath string) error {
	dir := filepath.Dir(filePath)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if err := os.MkdirAll(dir, os.ModePerm); err != nil {
			return fmt.Errorf("failed to create directory: %v", err)
		}
	}
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		file, err := os.Create(filePath)
		if err != nil {
			return fmt.Errorf("failed to create file: %v", err)
		}
		defer file.Close()
	}
	return nil
}

func readProperties(filePath string) ([]property, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var properties []property
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			properties = append(properties, property{key, value})
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return properties, nil
}

func updateProperties(properties []property,
	newProperties []property, function propertyMergeFunc) []property {
	for _, newProperty := range newProperties {
		if index := getKeyIndex(properties, newProperty.key); index != -1 {
			properties[index].value = function(properties[index].value, newProperty.value)
		} else {
			properties = append(properties, newProperty)
		}
	}
	return properties
}

func getKeyIndex(properties []property, key string) int {
	for i, prop := range properties {
		if prop.key == key {
			return i
		}
	}
	return -1
}

func writeProperties(filePath string, properties []property) error {
	file, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	for _, p := range properties {
		_, err := writer.WriteString(fmt.Sprintf("%s=%s\n", p.key, p.value))
		if err != nil {
			return err
		}
	}
	return writer.Flush()
}
