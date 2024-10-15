package javaanalyze

type ruleRedis struct {
}

func (r *ruleRedis) match(javaProject *javaProject) bool {
	if javaProject.mavenProject.Dependencies != nil {
		for _, dep := range javaProject.mavenProject.Dependencies {
			if dep.GroupId == "org.springframework.boot" && dep.ArtifactId == "spring-boot-starter-data-redis" {
				return true
			}
			if dep.GroupId == "org.springframework.boot" && dep.ArtifactId == "spring-boot-starter-data-redis-reactive" {
				return true
			}
		}
	}
	return false
}

func (r *ruleRedis) apply(azureYaml *AzureYaml) {
	azureYaml.Resources = append(azureYaml.Resources, &Resource{
		Name: "Redis",
		Type: "Redis",
	})
}
