import * as vscode from 'vscode';

export class HelpAndFeedbackTreeDataProvider implements vscode.TreeDataProvider<vscode.TreeItem> {
    getTreeItem(element: vscode.TreeItem): vscode.TreeItem {
        return element;
    }

    getChildren(element?: vscode.TreeItem): vscode.ProviderResult<vscode.TreeItem[]> {
        if (element) {
            return [];
        }

        const items: vscode.TreeItem[] = [];

        const documentation = new vscode.TreeItem('Documentation', vscode.TreeItemCollapsibleState.None);
        documentation.iconPath = new vscode.ThemeIcon('book');
        documentation.command = {
            command: 'vscode.open',
            title: 'Open Documentation',
            arguments: [vscode.Uri.parse('https://learn.microsoft.com/azure/developer/azure-developer-cli/')]
        };
        items.push(documentation);

        const blogPosts = new vscode.TreeItem('AZD Blog Posts', vscode.TreeItemCollapsibleState.None);
        blogPosts.iconPath = new vscode.ThemeIcon('library');
        blogPosts.command = {
            command: 'vscode.open',
            title: 'Open AZD Blog Posts',
            arguments: [vscode.Uri.parse('https://devblogs.microsoft.com/azure-sdk/tag/azure-developer-cli/')]
        };
        items.push(blogPosts);

        const getStarted = new vscode.TreeItem('Get Started', vscode.TreeItemCollapsibleState.None);
        getStarted.iconPath = new vscode.ThemeIcon('rocket');
        getStarted.command = {
            command: 'workbench.action.openWalkthrough',
            title: 'Get Started',
            arguments: ['ms-azuretools.azure-dev#azd.start']
        };
        items.push(getStarted);

        const whatsNew = new vscode.TreeItem("What's New", vscode.TreeItemCollapsibleState.None);
        whatsNew.iconPath = new vscode.ThemeIcon('sparkle');
        whatsNew.command = {
            command: 'vscode.open',
            title: "What's New",
            arguments: [vscode.Uri.parse('https://github.com/Azure/azure-dev/releases')]
        };
        items.push(whatsNew);

        const reportIssues = new vscode.TreeItem('Report Issues on GitHub', vscode.TreeItemCollapsibleState.None);
        reportIssues.iconPath = new vscode.ThemeIcon('github');
        reportIssues.command = {
            command: 'vscode.open',
            title: 'Report Issues',
            arguments: [vscode.Uri.parse('https://github.com/Azure/azure-dev/issues')]
        };
        items.push(reportIssues);

        return items;
    }
}
