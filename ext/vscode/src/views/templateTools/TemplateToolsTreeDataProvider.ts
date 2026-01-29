// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';
import { AzureDevTemplateProvider, Template, TemplateCategory } from '../../services/AzureDevTemplateProvider';
import { FileSystemWatcherService } from '../../services/FileSystemWatcherService';

export class TemplateToolsTreeDataProvider implements vscode.TreeDataProvider<TreeItemModel> {
    private _onDidChangeTreeData: vscode.EventEmitter<TreeItemModel | undefined | null | void> = new vscode.EventEmitter<TreeItemModel | undefined | null | void>();
    readonly onDidChangeTreeData: vscode.Event<TreeItemModel | undefined | null | void> = this._onDidChangeTreeData.event;

    private readonly templateProvider: AzureDevTemplateProvider;
    private configFileWatcherDisposable: vscode.Disposable;

    constructor(private fileSystemWatcherService: FileSystemWatcherService) {
        this.templateProvider = new AzureDevTemplateProvider();

        // Listen to azure.yaml file changes to toggle Quick Start visibility
        const onFileChange = () => {
            this.refresh();
        };

        this.configFileWatcherDisposable = this.fileSystemWatcherService.watch(
            '**/azure.{yml,yaml}',
            onFileChange
        );
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
            return templates.map(t => new TemplateItem(t));
        }

        if (element instanceof AITemplatesItem) {
            const templates = await this.templateProvider.getAITemplates();
            return templates.map(t => new TemplateItem(t));
        }

        return [];
    }

    private async getRootItems(): Promise<TreeItemModel[]> {
        const items: TreeItemModel[] = [];
        const hasAzureYaml = await this.hasAzureYamlInWorkspace();

        if (!hasAzureYaml) {
            items.push(new QuickStartGroupItem());
        }

        items.push(new CategoryGroupItem());
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

    private getCategoryItems(): TreeItemModel[] {
        const categories = this.templateProvider.getCategories();
        return categories.map(c => new CategoryItem(c, this.templateProvider));
    }

    private async hasAzureYamlInWorkspace(): Promise<boolean> {
        const files = await vscode.workspace.findFiles('**/azure.{yml,yaml}', '**/node_modules/**', 1);
        return files.length > 0;
    }

    dispose(): void {
        this.configFileWatcherDisposable.dispose();
        this._onDidChangeTreeData.dispose();
    }
}

// Base tree item model
abstract class TreeItemModel extends vscode.TreeItem {}

// Root level items
class QuickStartGroupItem extends TreeItemModel {
    constructor() {
        super(vscode.l10n.t('Quick Start'), vscode.TreeItemCollapsibleState.Expanded);
        this.contextValue = 'quickStartGroup';
        this.tooltip = vscode.l10n.t('Get started with Azure Developer CLI');
        this.iconPath = new vscode.ThemeIcon('rocket');
    }
}

class CategoryGroupItem extends TreeItemModel {
    constructor() {
        super(vscode.l10n.t('Browse by Category'), vscode.TreeItemCollapsibleState.Collapsed);
        this.contextValue = 'categoryGroup';
        this.tooltip = vscode.l10n.t('Browse templates by category');
        this.iconPath = new vscode.ThemeIcon('folder-library');
    }
}

class AITemplatesItem extends TreeItemModel {
    constructor(private templateProvider: AzureDevTemplateProvider) {
        super(vscode.l10n.t('AI Templates'), vscode.TreeItemCollapsibleState.Collapsed);
        this.contextValue = 'aiTemplates';
        this.tooltip = vscode.l10n.t('AI and Machine Learning focused templates');
        this.iconPath = new vscode.ThemeIcon('sparkle');

        // Async description update
        void this.templateProvider.getAITemplates().then(templates => {
            this.description = vscode.l10n.t('{0} templates', templates.length);
        });
    }
}

class SearchTemplatesItem extends TreeItemModel {
    constructor() {
        super(vscode.l10n.t('Search Templates...'), vscode.TreeItemCollapsibleState.None);
        this.contextValue = 'searchTemplates';
        this.tooltip = vscode.l10n.t('Search for templates');
        this.iconPath = new vscode.ThemeIcon('search');
        this.command = {
            command: 'azure-dev.views.templateTools.search',
            title: vscode.l10n.t('Search Templates')
        };
    }
}

// Quick start items
class InitFromCodeItem extends TreeItemModel {
    constructor() {
        super(vscode.l10n.t('Initialize from Current Code'), vscode.TreeItemCollapsibleState.None);
        this.contextValue = 'initFromCode';
        this.tooltip = vscode.l10n.t('Scan your current directory and generate Azure infrastructure');
        this.iconPath = new vscode.ThemeIcon('code');
        this.command = {
            command: 'azure-dev.views.templateTools.initFromCode',
            title: vscode.l10n.t('Initialize from Code')
        };
    }
}

class InitMinimalItem extends TreeItemModel {
    constructor() {
        super(vscode.l10n.t('Create Minimal Project'), vscode.TreeItemCollapsibleState.None);
        this.contextValue = 'initMinimal';
        this.tooltip = vscode.l10n.t('Create a minimal azure.yaml project file');
        this.iconPath = new vscode.ThemeIcon('file');
        this.command = {
            command: 'azure-dev.views.templateTools.initMinimal',
            title: vscode.l10n.t('Create Minimal Project')
        };
    }
}

class BrowseGalleryItem extends TreeItemModel {
    constructor() {
        super(vscode.l10n.t('Browse Template Gallery'), vscode.TreeItemCollapsibleState.None);
        this.contextValue = 'browseGallery';
        this.tooltip = vscode.l10n.t('Open Azure Developer CLI templates gallery in browser');
        this.iconPath = new vscode.ThemeIcon('globe');
        this.command = {
            command: 'azure-dev.views.templateTools.openGallery',
            title: vscode.l10n.t('Browse Gallery')
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
        this.tooltip = vscode.l10n.t('Browse {0} templates', category.displayName);
        this.iconPath = new vscode.ThemeIcon('folder');

        // Async description update
        void this.templateProvider.getTemplatesByCategory(category.name).then(templates => {
            this.description = vscode.l10n.t('{0} templates', templates.length);
        });
    }

    get categoryName(): string {
        return this.category.name;
    }
}

// Template item
class TemplateItem extends TreeItemModel {
    constructor(
        public readonly template: Template
    ) {
        super(template.title, vscode.TreeItemCollapsibleState.None);
        this.contextValue = 'ms-azuretools.azure-dev.views.templateTools.template';
        this.tooltip = new vscode.MarkdownString(
            `**${template.title}**\n\n${template.description}\n\n` +
            `${vscode.l10n.t('Author')}: ${template.author}\n\n` +
            `[${vscode.l10n.t('View on GitHub')}](${template.source})`
        );
        this.description = template.author;
        this.iconPath = new vscode.ThemeIcon('symbol-class');

        // Click to open README
        this.command = {
            command: 'azure-dev.views.templateTools.openReadme',
            title: vscode.l10n.t('View README'),
            arguments: [template]
        };
    }
}
