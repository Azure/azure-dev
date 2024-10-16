package javaanalyze

import (
	"bufio"
	"fmt"
	"gopkg.in/yaml.v3"
	"log"
	"os"
	"path/filepath"
	"strings"
)

type springProject struct {
	applicationProperties map[string]string
}

func analyzeSpringProject(projectPath string) springProject {
	return springProject{
		applicationProperties: getProperties(projectPath),
	}
}

func getProperties(projectPath string) map[string]string {
	result := make(map[string]string)
	getPropertiesInPropertiesFile(filepath.Join(projectPath, "/src/main/resources/application.properties"), result)
	getPropertiesInYamlFile(filepath.Join(projectPath, "/src/main/resources/application.yml"), result)
	getPropertiesInYamlFile(filepath.Join(projectPath, "/src/main/resources/application.yaml"), result)
	profile, profileSet := result["spring.profiles.active"]
	if profileSet {
		getPropertiesInPropertiesFile(filepath.Join(projectPath, "/src/main/resources/application-"+profile+".properties"), result)
		getPropertiesInYamlFile(filepath.Join(projectPath, "/src/main/resources/application-"+profile+".yml"), result)
		getPropertiesInYamlFile(filepath.Join(projectPath, "/src/main/resources/application-"+profile+".yaml"), result)
	}
	return result
}

func getPropertiesInYamlFile(yamlFilePath string, result map[string]string) {
	data, err := os.ReadFile(yamlFilePath)
	if err != nil {
		// Ignore the error if file not exist.
		return
	}

	// Parse the YAML into a yaml.Node
	var root yaml.Node
	err = yaml.Unmarshal(data, &root)
	if err != nil {
		log.Fatalf("error unmarshalling YAML: %v", err)
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
		result[prefix] = node.Value
	default:
		// Handle other node types if necessary
	}
}

func getPropertiesInPropertiesFile(propertiesFilePath string, result map[string]string) {
	file, err := os.Open(propertiesFilePath)
	if err != nil {
		// Ignore the error if file not exist.
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
			value := strings.TrimSpace(parts[1])
			result[key] = value
		}
	}
}
