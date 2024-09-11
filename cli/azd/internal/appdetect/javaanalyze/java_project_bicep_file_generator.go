package javaanalyze

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
)

func GenerateBicepFilesForJavaProject(outputDirectory string, project JavaProject) error {
	log.Printf("Generating bicep files for java project.")
	err := GenerateMainDotBicep(outputDirectory)
	if err != nil {
		return err
	}
	for _, resource := range project.Resources {
		err := GenerateBicepFileForResource(outputDirectory, resource)
		if err != nil {
			return err
		}
	}
	for _, service := range project.Services {
		err := GenerateBicepFileForService(outputDirectory, service)
		if err != nil {
			return err
		}
	}
	for _, binding := range project.ServiceBindings {
		err := GenerateBicepFileForBinding(outputDirectory, binding)
		if err != nil {
			return err
		}
	}
	return nil
}

func GenerateMainDotBicep(outputDirectory string) error {
	log.Printf("Generating main.bicep.")
	bicepFileName := filepath.Join(outputDirectory, "main.bicep")
	return GenerateBicepFile(bicepFileName, "placeholder")
}

func GenerateBicepFileForResource(outputDirectory string, resource Resource) error {
	log.Printf("Generating bicep file for resource: %s.", resource.Name)
	bicepFileName := filepath.Join(outputDirectory, resource.Name+".bicep")
	return GenerateBicepFile(bicepFileName, "placeholder")
}

func GenerateBicepFileForService(outputDirectory string, service ServiceConfig) error {
	log.Printf("Generating bicep file for service config: %s.", service.Name)
	bicepFileName := filepath.Join(outputDirectory, service.Name+".bicep")
	return GenerateBicepFile(bicepFileName, "placeholder")
}

func GenerateBicepFileForBinding(outputDirectory string, binding ServiceBinding) error {
	log.Printf("Generating bicep file for service binding: %s.", binding.Name)
	bicepFileName := filepath.Join(outputDirectory, binding.Name+".bicep")
	return GenerateBicepFile(bicepFileName, "placeholder")
}

func GenerateBicepFile(fileName string, content string) error {
	bicepFile, err := os.Create(fileName)
	if err != nil {
		return fmt.Errorf("creating %s: %w", fileName, err)
	}
	defer bicepFile.Close()
	if _, err := bicepFile.WriteString(content); err != nil {
		return fmt.Errorf("writing %s: %w", fileName, err)
	}
	return nil

}
