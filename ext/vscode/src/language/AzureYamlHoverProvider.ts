// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';

/**
 * Provides hover documentation for azure.yaml files
 */
export class AzureYamlHoverProvider implements vscode.HoverProvider {
    private readonly documentation: Map<string, { title: string; description: string; example?: string }> = new Map([
        ['name', {
            title: 'Application Name',
            description: 'The name of your Azure application. This is used for display and identification purposes.',
            example: 'name: my-awesome-app'
        }],
        ['services', {
            title: 'Services',
            description: 'Defines the services that make up your application. Each service represents a deployable component.',
            example: 'services:\n  api:\n    project: ./src/api\n    language: python\n    host: containerapp'
        }],
        ['project', {
            title: 'Project Path',
            description: 'Relative path to the service project directory from the azure.yaml file. Should point to the folder containing your application code.',
            example: 'project: ./src/api'
        }],
        ['language', {
            title: 'Programming Language',
            description: 'The programming language used by the service. Supported values: js, ts, python, csharp, java, go, php.',
            example: 'language: python'
        }],
        ['host', {
            title: 'Azure Host',
            description: 'The Azure platform where the service will be deployed.\n\n**Options:**\n- `containerapp` - Azure Container Apps\n- `appservice` - Azure App Service\n- `function` - Azure Functions\n- `aks` - Azure Kubernetes Service\n- `staticwebapp` - Azure Static Web Apps',
            example: 'host: containerapp'
        }],
        ['hooks', {
            title: 'Lifecycle Hooks',
            description: 'Commands to run at specific points in the deployment lifecycle.\n\n**Available hooks:**\n- `prerestore` - Before restoring dependencies\n- `postrestore` - After restoring dependencies\n- `preprovision` - Before provisioning infrastructure\n- `postprovision` - After provisioning infrastructure\n- `predeploy` - Before deploying application\n- `postdeploy` - After deploying application',
            example: 'hooks:\n  postdeploy:\n    run: npm run migrate\n    shell: sh\n    continueOnError: false'
        }],
        ['docker', {
            title: 'Docker Configuration',
            description: 'Docker-specific settings for containerized services.',
            example: 'docker:\n  path: ./Dockerfile\n  context: .'
        }],
        ['resourceName', {
            title: 'Resource Name Override',
            description: 'Override the default Azure resource name. By default, azd generates resource names based on environment and service names.',
            example: 'resourceName: my-custom-resource-name'
        }],
        ['pipeline', {
            title: 'CI/CD Pipeline',
            description: 'Configuration for continuous integration and deployment pipelines.',
            example: 'pipeline:\n  provider: github'
        }],
        ['metadata', {
            title: 'Metadata',
            description: 'Additional metadata about the application, such as template information.',
            example: 'metadata:\n  template: todo-python-mongo'
        }]
    ]);

    public provideHover(
        document: vscode.TextDocument,
        position: vscode.Position,
        token: vscode.CancellationToken
    ): vscode.ProviderResult<vscode.Hover> {
        const range = document.getWordRangeAtPosition(position);
        if (!range) {
            return null;
        }

        const word = document.getText(range);
        const doc = this.documentation.get(word);

        if (!doc) {
            return null;
        }

        const markdown = new vscode.MarkdownString();
        markdown.appendMarkdown(`### ${doc.title}\n\n`);
        markdown.appendMarkdown(doc.description);

        if (doc.example) {
            markdown.appendMarkdown('\n\n**Example:**\n```yaml\n' + doc.example + '\n```');
        }

        markdown.appendMarkdown('\n\n[View Documentation](https://learn.microsoft.com/azure/developer/azure-developer-cli/azd-schema)');

        return new vscode.Hover(markdown, range);
    }
}
