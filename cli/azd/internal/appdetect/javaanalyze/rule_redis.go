package javaanalyze

type ruleRedis struct {
}

func (r *ruleRedis) match(javaProject *javaProject) bool {
	return false
}

func (r *ruleRedis) apply(azureYaml *AzureYaml) {
	azureYaml.Resources = append(azureYaml.Resources, &Resource{
		Name: "Redis",
		Type: "Redis",
	})
}
