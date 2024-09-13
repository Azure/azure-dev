package javaanalyze

import (
	"os"
)

func Analyze(path string) []JavaProject {
	result := []JavaProject{}
	rules := []rule{
		&ruleService{},
		&ruleMysql{},
		&ruleStorage{},
	}

	entries, err := os.ReadDir(path)
	if err == nil {
		for _, entry := range entries {
			if "pom.xml" == entry.Name() {
				mavenProject, _ := ParsePOM(path + "/" + entry.Name())

				// if it has submodules
				if len(mavenProject.Modules) > 0 {
					for _, m := range mavenProject.Modules {
						// analyze the submodules
						subModule, _ := ParsePOM(path + "/" + m + "/pom.xml")
						javaProject, _ := ApplyRules(subModule, rules)
						result = append(result, *javaProject)
					}
				} else {
					// analyze the maven project
					javaProject, _ := ApplyRules(mavenProject, rules)
					result = append(result, *javaProject)
				}
			}
			//fmt.Printf("\tentry: %s", entry.Name())
		}
	}

	return result
}
