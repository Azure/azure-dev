Create a new Microsoft Foundry agent in this project at {{.ProjectPath}}.

Use azd ai to set it up, run and test it locally first, then deploy it to Azure, and verify it works.

Ask me before any step that creates or changes Azure resources.
{{- if .FoundryProjectId}}

Use Foundry project {{.FoundryProjectId}}
{{- end}}
{{- if .ModelDeployment}}
If a model deployment is needed, use {{.ModelDeployment}}
{{- end}}
