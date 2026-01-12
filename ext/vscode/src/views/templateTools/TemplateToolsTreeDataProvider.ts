// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { AzureDevTemplateProvider, Template, TemplateCategory } from '../../services/AzureDevTemplateProvider';

export class TemplateToolsTreeDataProvider implements vscode.TreeDataProvider<TreeItemModel> {
    private _onDidChangeTreeData: vscode.EventEmitter<TreeItemModel | undefined | null | void> = new vscode.EventEmitter<TreeItemModel | undefined | null | void>();
    readonly onDidChangeTreeData: vscode.Event<TreeItemModel | undefined | null | void> = this._onDidChangeTreeData.event;

    private readonly templateProvider: AzureDevTemplateProvider;
    private configFileWatcher: vscode.FileSystemWatcher;

    constructor() {
        this.templateProvider = new AzureDevTemplateProvider();

        // Listen to azure.yaml file changes to toggle Quick Start visibility
        this.configFileWatcher = vscode.workspace.createFileSystemWatcher(
            '**/azure.{yml,yaml}',
            false, false, false
        );

        const onFileChange = () => {
            this.refresh();
        };

        this.configFileWatcher.onDidCreate(onFileChange);
        this.configFileWatcher.onDidDelete(onFileChange);
    }

    refresh(): void {
        this._onDidChangeTreeData.fire();
    }

    getTreeItem(element: TreeItemModel): vscode.TreeItem {
        return element;
    }

    async getChildren(element?: TreeItemModel): Promise<TreeItemModel[]> {
        if (!element) {
            return this.getRootItems();
        }

        if (element instanceof QuickStartGroupItem) {
            return this.getQuickStartItems();
        }

        if (element instanceof CategoryGroupItem) {
            return this.getCategoryItems();
        }

        if (element instanceof CategoryItem) {
            const templates = await this.templateProvider.getTemplatesByCategory(element.categoryName);
            return templates.map(t => new TemplateItem(t, this.templateProvider));
        }

        if (element instanceof AITemplatesItem) {
            const templates = await this.templateProvider.getAITemplates();
            return templates.map(t => new TemplateItem(t, this.templateProvider));
        }

        return [];
    }

    private async getRootItems(): Promise<TreeItemModel[]> {
        const items: TreeItemModel[] = [];
        const hasAzureYaml = await this.hasAzureYamlInWorkspace();

        if (!hasAzureYaml) {
            items.push(new QuickStartGroupItem());
        }

        items.push(new CategoryGroupItem(this.templateProvider));
        items.push(new AITemplatesItem(this.templateProvider));
        items.push(new SearchTemplatesItem());

        return items;
    }

    private getQuickStartItems(): TreeItemModel[] {
        return [
            new InitFromCodeItem(),
            new InitMinimalItem(),
            new BrowseGalleryItem()
        ];
    }

    private async getCategoryItems(): Promise<TreeItemModel[]> {
        const categories = this.templateProvider.getCategories();
        return categories.map(c => new CategoryItem(c, this.templateProvider));
    }

    private async hasAzureYamlInWorkspace(): Promise<boolean> {
        const files = await vscode.workspace.findFiles('**/azure.{yml,yaml}', '**/node_modules/**', 1);
        return files.length > 0;
    }

    dispose(): void {
        this.configFileWatcher.dispose();
        this._onDidChangeTreeData.dispose();
    }
}

// Base tree item model
abstract class TreeItemModel extends vscode.TreeItem {}

// Root level items
class QuickStartGroupItem extends TreeItemModel {
    constructor() {
        super('Quick Start', vscode.TreeItemCollapsibleState.Expanded);
        this.contextValue = 'quickStartGroup';
        this.tooltip = 'Get started with Azure Developer CLI';
        this.iconPath = new vscode.ThemeIcon('rocket');
    }
}

class CategoryGroupItem extends TreeItemModel {
    constructor(private templateProvider: AzureDevTemplateProvider) {
        super('Browse by Category', vscode.TreeItemCollapsibleState.Collapsed);
        this.contextValue = 'categoryGroup';
        this.tooltip = 'Browse templates by category';
        this.iconPath = new vscode.ThemeIcon('folder-library');
    }
}

class AITemplatesItem extends TreeItemModel {
    constructor(private templateProvider: AzureDevTemplateProvider) {
        super('AI Templates', vscode.TreeItemCollapsibleState.Collapsed);
        this.contextValue = 'aiTemplates';
        this.tooltip = 'AI and Machine Learning focused templates';
        this.iconPath = new vscode.ThemeIcon('sparkle');

        // Async description update
        void this.templateProvider.getAITemplates().then(templates => {
            this.description = `${templates.length} templates`;
        });
    }
}

class SearchTemplatesItem extends TreeItemModel {
    constructor() {
        super('Search Templates...', vscode.TreeItemCollapsibleState.None);
        this.contextValue = 'searchTemplates';
        this.tooltip = 'Search for templates';
        this.iconPath = new vscode.ThemeIcon('search');
        this.command = {
            command: 'azure-dev.views.templateTools.search',
            title: 'Search Templates'
        };
    }
}

// Quick start items
class InitFromCodeItem extends TreeItemModel {
    constructor() {
        super('Initialize from Current Code', vscode.TreeItemCollapsibleState.None);
        this.contextValue = 'initFromCode';
        this.tooltip = 'Scan your current directory and generate Azure infrastructure';
        this.iconPath = new vscode.ThemeIcon('code');
        this.command = {
            command: 'azure-dev.views.templateTools.initFromCode',
            title: 'Initialize from Code'
        };
    }
}

class InitMinimalItem extends TreeItemModel {
    constructor() {
        super('Create Minimal Project', vscode.TreeItemCollapsibleState.None);
        this.contextValue = 'initMinimal';
        this.tooltip = 'Create a minimal azure.yaml project file';
        this.iconPath = new vscode.ThemeIcon('file');
        this.command = {
            command: 'azure-dev.views.templateTools.initMinimal',
            title: 'Create Minimal Project'
        };
    }
}

class BrowseGalleryItem extends TreeItemModel {
    constructor() {
        super('Browse Template Gallery', vscode.TreeItemCollapsibleState.None);
        this.contextValue = 'browseGallery';
        this.tooltip = 'Open Azure Developer CLI templates gallery in browser';
        this.iconPath = new vscode.ThemeIcon('globe');
        this.command = {
            command: 'azure-dev.views.templateTools.openGallery',
            title: 'Browse Gallery'
        };
    }
}

// Category item
class CategoryItem extends TreeItemModel {
    constructor(
        public readonly category: TemplateCategory,
        private templateProvider: AzureDevTemplateProvider
    ) {
        super(category.displayName, vscode.TreeItemCollapsibleState.Collapsed);
        this.contextValue = 'templateCategory';
        this.tooltip = `Browse ${category.displayName} templates`;
        this.iconPath = new vscode.ThemeIcon('folder');

        // Async description update
        void this.templateProvider.getTemplatesByCategory(category.name).then(templates => {
            this.description = `${templates.length} templates`;
        });
    }

    get categoryName(): string {
        return this.category.name;
    }
}

// Template item
class TemplateItem extends TreeItemModel {
    constructor(
        public readonly template: Template,
        private templateProvider: AzureDevTemplateProvider
    ) {
        super(template.title, vscode.TreeItemCollapsibleState.None);
        this.contextValue = 'ms-azuretools.azure-dev.views.templateTools.template';
        this.tooltip = new vscode.MarkdownString(
            `**${template.title}**\n\n${template.description}\n\n` +
            `Author: ${template.author}\n\n` +
            `[View on GitHub](${template.source})`
        );
        this.description = template.author;
        this.iconPath = new vscode.ThemeIcon('symbol-class');

        // Click to open README
        this.command = {
            command: 'azure-dev.views.templateTools.openReadme',
            title: 'View README',
            arguments: [template]
        };
    }
}
