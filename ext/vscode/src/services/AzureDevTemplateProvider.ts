// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as vscode from 'vscode';

export interface Template {
    id: string;
    title: string;
    description: string;
    source: string;
    preview?: string;
    author: string;
    authorUrl?: string;
    tags?: string[];
    languages?: string[];
    frameworks?: string[];
    azureServices?: string[];
    IaC?: string[];
}

export interface TemplateCategory {
    name: string;
    displayName: string;
    icon: string;
    filter: (template: Template) => boolean;
}

export class AzureDevTemplateProvider {
    private templatesCache: Template[] | undefined;
    // eslint-disable-next-line @typescript-eslint/naming-convention
    private readonly TEMPLATES_URL = 'https://raw.githubusercontent.com/Azure/awesome-azd/main/website/static/templates.json';
    // eslint-disable-next-line @typescript-eslint/naming-convention
    private readonly CACHE_DURATION_MS = 3600000; // 1 hour
    private lastFetchTime: number = 0;

    private readonly categories: TemplateCategory[] = [
        {
            name: 'ai',
            displayName: vscode.l10n.t('AI & Machine Learning'),
            icon: '🤖',
            filter: (t) => t.tags?.some(tag => ['ai', 'gpt', 'aicollection'].includes(tag)) ?? false
        },
        {
            name: 'webapp',
            displayName: vscode.l10n.t('Web Applications'),
            icon: '🌐',
            filter: (t) => t.tags?.some(tag => ['webapps', 'reactjs', 'angular', 'vuejs'].includes(tag)) ?? false
        },
        {
            name: 'api',
            displayName: vscode.l10n.t('APIs & Functions'),
            icon: '🔧',
            filter: (t) => (t.tags?.includes('functions') || t.azureServices?.includes('functions')) ?? false
        },
        {
            name: 'container',
            displayName: vscode.l10n.t('Containers & Kubernetes'),
            icon: '📦',
            filter: (t) => (t.tags?.includes('kubernetes') || t.azureServices?.some(s => ['aks', 'aca'].includes(s))) ?? false
        },
        {
            name: 'database',
            displayName: vscode.l10n.t('Databases & Storage'),
            icon: '💾',
            filter: (t) => t.azureServices?.some(s => ['cosmosdb', 'azuresql', 'azuredb-postgreSQL', 'azuredb-mySQL'].includes(s)) ?? false
        },
        {
            name: 'starter',
            displayName: vscode.l10n.t('Starter Templates'),
            icon: '🚀',
            filter: (t) => t.title.toLowerCase().includes('starter') || t.title.toLowerCase().includes('quickstart')
        }
    ];

    public async getTemplates(forceRefresh: boolean = false): Promise<Template[]> {
        const now = Date.now();
        const cacheExpired = (now - this.lastFetchTime) > this.CACHE_DURATION_MS;

        if (!this.templatesCache || forceRefresh || cacheExpired) {
            try {
                const response = await fetch(this.TEMPLATES_URL);
                if (!response.ok) {
                    throw new Error(`Failed to fetch templates: ${response.statusText}`);
                }
                this.templatesCache = await response.json() as Template[];
                this.lastFetchTime = now;
            } catch (error) {
                vscode.window.showErrorMessage(vscode.l10n.t('Failed to load templates: {0}', error instanceof Error ? error.message : String(error)));
                return this.templatesCache ?? [];
            }
        }

        return this.templatesCache || [];
    }

    public async getAITemplates(): Promise<Template[]> {
        const templates = await this.getTemplates();
        return templates.filter(t => t.tags?.includes('aicollection'));
    }

    public async getTemplatesByCategory(categoryName: string): Promise<Template[]> {
        const templates = await this.getTemplates();
        const category = this.categories.find(c => c.name === categoryName);
        if (!category) {
            return [];
        }
        return templates.filter(category.filter);
    }

    public async searchTemplates(query: string): Promise<Template[]> {
        const templates = await this.getTemplates();
        const lowerQuery = query.toLowerCase();

        return templates.filter(t => {
            const titleMatch = t.title.toLowerCase().includes(lowerQuery);
            const descMatch = t.description?.toLowerCase().includes(lowerQuery);
            const tagMatch = t.tags?.some(tag => tag.toLowerCase().includes(lowerQuery));
            const langMatch = t.languages?.some(lang => lang.toLowerCase().includes(lowerQuery));
            const serviceMatch = t.azureServices?.some(svc => svc.toLowerCase().includes(lowerQuery));

            return titleMatch || descMatch || tagMatch || langMatch || serviceMatch;
        });
    }

    public getCategories(): TemplateCategory[] {
        return this.categories;
    }

    public extractTemplatePath(sourceUrl: string): string {
        // Convert GitHub URL to format accepted by azd init
        // https://github.com/Azure-Samples/todo-csharp-cosmos-sql -> Azure-Samples/todo-csharp-cosmos-sql
        return sourceUrl.replace(/^https?:\/\/github\.com\//, '');
    }

    public async getTemplateCount(): Promise<number> {
        const templates = await this.getTemplates();
        return templates.length;
    }
}
