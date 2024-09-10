package main

import (
	"fmt"
	"os"
)

// Main function.
func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: go run main.go [path-to-pom.xml]")
		os.Exit(1)
	}

	pomPath := os.Args[1]
	project, err := ParsePOM(pomPath)
	if err != nil {
		fmt.Printf("Failed to parse POM file: %s\n", err)
		os.Exit(1)
	}

	fmt.Println("Dependencies found:")
	for _, dep := range project.Dependencies {
		fmt.Printf("- GroupId: %s, ArtifactId: %s, Version: %s, Scope: %s\n",
			dep.GroupId, dep.ArtifactId, dep.Version, dep.Scope)
	}

	fmt.Println("Dependency Management:")
	for _, dep := range project.DependencyManagement.Dependencies {
		fmt.Printf("- GroupId: %s, ArtifactId: %s, Version: %s\n",
			dep.GroupId, dep.ArtifactId, dep.Version)
	}

	fmt.Println("Plugins used in Build:")
	for _, plugin := range project.Build.Plugins {
		fmt.Printf("- GroupId: %s, ArtifactId: %s, Version: %s\n",
			plugin.GroupId, plugin.ArtifactId, plugin.Version)
	}

	if project.Parent.GroupId != "" {
		fmt.Printf("Parent POM: GroupId: %s, ArtifactId: %s, Version: %s\n",
			project.Parent.GroupId, project.Parent.ArtifactId, project.Parent.Version)
	}

	//ApplyRules(project, []Rule{
	//	{
	//		Match: func(mavenProject MavenProject) bool {
	//			for _, dep := range mavenProject.Dependencies {
	//				if dep.GroupId == "com.mysql" && dep.ArtifactId == "mysql-connector-java" {
	//					return true
	//				}
	//			}
	//			return false
	//		},
	//		Apply: func(javaProject *JavaProject) {
	//			append(javaProject.Resources, Resource{
	//				Name: "mysql",
	//				Type: "mysql",
	//				BicepParameters: []BicepParameter{
	//					{
	//						Name:    "serverName",
	//					},
	//				}
	//			})
	//		},
	//	},
	//})

}
