// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { expect } from 'chai';
import { AzureDevTemplateProvider } from '../../../services/AzureDevTemplateProvider';

suite('AzureDevTemplateProvider', () => {
    let provider: AzureDevTemplateProvider;

    setup(() => {
        provider = new AzureDevTemplateProvider();
    });

    test('getTemplates returns array of templates', async () => {
        const templates = await provider.getTemplates();

        expect(templates, 'Should return an array').to.be.an('array');
        if (templates.length > 0) {
            const template = templates[0];
            expect(template.id, 'Template should have an id').to.exist;
            expect(template.title, 'Template should have a title').to.exist;
            expect(template.description, 'Template should have a description').to.exist;
            expect(template.source, 'Template should have a source').to.exist;
        }
    });

    test('getAITemplates returns only AI-tagged templates', async () => {
        const aiTemplates = await provider.getAITemplates();

        expect(aiTemplates, 'Should return an array').to.be.an('array');
        aiTemplates.forEach(template => {
            expect(
                template.tags?.includes('aicollection'),
                `Template "${template.title}" should have aicollection tag`
            ).to.be.true;
        });
    });

    test('searchTemplates filters by query', async () => {
        const searchResults = await provider.searchTemplates('react');

        expect(searchResults, 'Should return an array').to.be.an('array');
        searchResults.forEach(template => {
            const matchesQuery =
                template.title.toLowerCase().includes('react') ||
                template.description?.toLowerCase().includes('react') ||
                template.tags?.some(tag => tag.toLowerCase().includes('react')) ||
                template.languages?.some(lang => lang.toLowerCase().includes('react')) ||
                template.frameworks?.some(fw => fw.toLowerCase().includes('react'));

            expect(matchesQuery, `Template "${template.title}" should match "react" query`).to.be.true;
        });
    });

    test('getTemplatesByCategory returns correct category templates', async () => {
        const categories = provider.getCategories();
        expect(categories.length, 'Should have categories').to.be.greaterThan(0);

        const firstCategory = categories[0];
        const categoryTemplates = await provider.getTemplatesByCategory(firstCategory.name);

        expect(categoryTemplates, 'Should return an array').to.be.an('array');
    });

    test('getCategories returns array of categories', () => {
        const categories = provider.getCategories();

        expect(categories, 'Should return an array').to.be.an('array');
        expect(categories.length, 'Should have at least one category').to.be.greaterThan(0);

        categories.forEach(category => {
            expect(category.name, 'Category should have a name').to.exist;
            expect(category.displayName, 'Category should have a display name').to.exist;
            expect(category.icon, 'Category should have an icon').to.exist;
            expect(typeof category.filter, 'Category should have a filter function').to.equal('function');
        });
    });

    test('extractTemplatePath extracts correct path from GitHub URL', () => {
        const testCases = [
            {
                input: 'https://github.com/Azure-Samples/todo-csharp-cosmos-sql',
                expected: 'Azure-Samples/todo-csharp-cosmos-sql'
            },
            {
                input: 'https://github.com/Azure/azure-dev',
                expected: 'Azure/azure-dev'
            }
        ];

        testCases.forEach(testCase => {
            const result = provider.extractTemplatePath(testCase.input);
            expect(result, `Should extract path from ${testCase.input}`).to.equal(testCase.expected);
        });
    });

    test('getTemplateCount returns number of templates', async () => {
        const count = await provider.getTemplateCount();

        expect(count, 'Should return a number').to.be.a('number');
        expect(count, 'Count should be non-negative').to.be.at.least(0);
    });

    test('forceRefresh parameter refreshes cache', async () => {
        // Load into cache
        const templates1 = await provider.getTemplates();
        expect(templates1.length, 'Should have templates').to.be.greaterThan(0);

        // Force refresh
        const templates2 = await provider.getTemplates(true);
        expect(templates2.length, 'Should have templates after refresh').to.be.greaterThan(0);

        // Both should have same data
        expect(templates1.length, 'Should have same number of templates').to.equal(templates2.length);
    });
});
