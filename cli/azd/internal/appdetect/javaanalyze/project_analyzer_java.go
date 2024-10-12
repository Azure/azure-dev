package javaanalyze

import "os"

type javaProject struct {
	springProject springProject
	mavenProject  mavenProject
}

func Analyze(path string) []AzureYaml {
	var result []AzureYaml
	rules := []rule{
		&ruleService{},
		&ruleMysql{},
		&rulePostgresql{},
		&ruleMongo{},
		&ruleStorage{},
		&ruleServiceBusScsb{},
	}

	entries, err := os.ReadDir(path)
	if err == nil {
		for _, entry := range entries {
			if "pom.xml" == entry.Name() {
				mavenProjects, _ := analyzeMavenProject(path)

				for _, mavenProject := range mavenProjects {
					javaProject := &javaProject{
						mavenProject:  mavenProject,
						springProject: analyzeSpringProject(mavenProject.path),
					}
					azureYaml, _ := applyRules(javaProject, rules)
					result = append(result, *azureYaml)
				}
			}
		}
	}

	return result
}
