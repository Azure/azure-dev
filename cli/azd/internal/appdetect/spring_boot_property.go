package appdetect

import (
	"bufio"
	"fmt"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/braydonk/yaml"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func readProperties(projectPath string) map[string]string {
	// todo: do we need to consider the bootstrap.properties
	result := make(map[string]string)
	readPropertiesInPropertiesFile(filepath.Join(projectPath, "/src/main/resources/application.properties"), result)
	readPropertiesInYamlFile(filepath.Join(projectPath, "/src/main/resources/application.yml"), result)
	readPropertiesInYamlFile(filepath.Join(projectPath, "/src/main/resources/application.yaml"), result)
	profile, profileSet := result["spring.profiles.active"]
	if profileSet {
		readPropertiesInPropertiesFile(
			filepath.Join(projectPath, "/src/main/resources/application-"+profile+".properties"), result)
		readPropertiesInYamlFile(filepath.Join(projectPath, "/src/main/resources/application-"+profile+".yml"), result)
		readPropertiesInYamlFile(filepath.Join(projectPath, "/src/main/resources/application-"+profile+".yaml"), result)
	}
	return result
}

func readPropertiesInYamlFile(yamlFilePath string, result map[string]string) {
	if !osutil.FileExists(yamlFilePath) {
		return
	}
	data, err := os.ReadFile(yamlFilePath)
	if err != nil {
		log.Fatalf("error reading YAML file: %v", err)
		return
	}

	// Parse the YAML into a yaml.Node
	var root yaml.Node
	err = yaml.Unmarshal(data, &root)
	if err != nil {
		log.Fatalf("error unmarshalling YAML: %v", err)
		return
	}

	parseYAML("", &root, result)
}

// Recursively parse the YAML and build dot-separated keys into a map
func parseYAML(prefix string, node *yaml.Node, result map[string]string) {
	switch node.Kind {
	case yaml.DocumentNode:
		// Process each document's content
		for _, contentNode := range node.Content {
			parseYAML(prefix, contentNode, result)
		}
	case yaml.MappingNode:
		// Process key-value pairs in a map
		for i := 0; i < len(node.Content); i += 2 {
			keyNode := node.Content[i]
			valueNode := node.Content[i+1]

			// Ensure the key is a scalar
			if keyNode.Kind != yaml.ScalarNode {
				continue
			}

			keyStr := keyNode.Value
			newPrefix := keyStr
			if prefix != "" {
				newPrefix = prefix + "." + keyStr
			}
			parseYAML(newPrefix, valueNode, result)
		}
	case yaml.SequenceNode:
		// Process items in a sequence (list)
		for i, item := range node.Content {
			newPrefix := fmt.Sprintf("%s[%d]", prefix, i)
			parseYAML(newPrefix, item, result)
		}
	case yaml.ScalarNode:
		// If it's a scalar value, add it to the result map
		result[prefix] = getEnvironmentVariablePlaceholderHandledValue(node.Value)
	default:
		// Handle other node types if necessary
	}
}

func readPropertiesInPropertiesFile(propertiesFilePath string, result map[string]string) {
	if !osutil.FileExists(propertiesFilePath) {
		return
	}
	file, err := os.Open(propertiesFilePath)
	if err != nil {
		log.Fatalf("error opening properties file: %v", err)
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := getEnvironmentVariablePlaceholderHandledValue(parts[1])
			result[key] = value
		}
	}
}

var environmentVariableRegex = regexp.MustCompile(`\$\{([^:}]+)(?::([^}]+))?}`)

func getEnvironmentVariablePlaceholderHandledValue(rawValue string) string {
	trimmedRawValue := strings.TrimSpace(rawValue)
	matches := environmentVariableRegex.FindAllStringSubmatch(trimmedRawValue, -1)
	result := trimmedRawValue
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		envVar := match[1]
		defaultValue := match[2]
		value := os.Getenv(envVar)
		if value == "" {
			value = defaultValue
		}
		placeholder := match[0]
		result = strings.Replace(result, placeholder, value, -1)
	}
	return result
}
