package javaanalyze

import (
	"fmt"
	"gopkg.in/yaml.v3"
	"io/ioutil"
	"log"
)

type springProject struct {
	applicationProperties map[string]interface{}
}

func analyzeSpringProject(projectPath string) springProject {
	return springProject{
		applicationProperties: findSpringApplicationProperties(projectPath),
	}
}

func findSpringApplicationProperties(projectPath string) map[string]interface{} {
	yamlFilePath := projectPath + "/src/main/resources/application.yml"
	data, err := ioutil.ReadFile(yamlFilePath)
	if err != nil {
		log.Fatalf("error reading YAML file: %v", err)
	}

	// Parse the YAML into a yaml.Node
	var root yaml.Node
	err = yaml.Unmarshal(data, &root)
	if err != nil {
		log.Fatalf("error unmarshalling YAML: %v", err)
	}

	result := make(map[string]interface{})
	parseYAML("", &root, result)

	return result
}

// Recursively parse the YAML and build dot-separated keys into a map
func parseYAML(prefix string, node *yaml.Node, result map[string]interface{}) {
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
