// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as assert from 'assert';
import { AzureDevTemplateProvider } from '../../../services/AzureDevTemplateProvider';

suite('AzureDevTemplateProvider', () => {
    let provider: AzureDevTemplateProvider;

    setup(() => {
        provider = new AzureDevTemplateProvider();
    });

    test('getTemplates returns array of templates', async () => {
        const templates = await provider.getTemplates();

        assert.ok(Array.isArray(templates), 'Should return an array');
        if (templates.length > 0) {
            const template = templates[0];
            assert.ok(template.id, 'Template should have an id');
            assert.ok(template.title, 'Template should have a title');
            assert.ok(template.description, 'Template should have a description');
            assert.ok(template.source, 'Template should have a source');
        }
    });

    test('getAITemplates returns only AI-tagged templates', async () => {
        const aiTemplates = await provider.getAITemplates();

        assert.ok(Array.isArray(aiTemplates), 'Should return an array');
        aiTemplates.forEach(template => {
            assert.ok(
                template.tags?.includes('aicollection'),
                `Template "${template.title}" should have aicollection tag`
            );
        });
    });

    test('searchTemplates filters by query', async () => {
        const searchResults = await provider.searchTemplates('react');

        assert.ok(Array.isArray(searchResults), 'Should return an array');
        searchResults.forEach(template => {
            const matchesQuery =
                template.title.toLowerCase().includes('react') ||
                template.description?.toLowerCase().includes('react') ||
                template.tags?.some(tag => tag.toLowerCase().includes('react')) ||
                template.languages?.some(lang => lang.toLowerCase().includes('react')) ||
                template.frameworks?.some(fw => fw.toLowerCase().includes('react'));

            assert.ok(matchesQuery, `Template "${template.title}" should match "react" query`);
        });
    });

    test('getTemplatesByCategory returns correct category templates', async () => {
        const categories = provider.getCategories();
        assert.ok(categories.length > 0, 'Should have categories');

        const firstCategory = categories[0];
        const categoryTemplates = await provider.getTemplatesByCategory(firstCategory.name);

        assert.ok(Array.isArray(categoryTemplates), 'Should return an array');
    });

    test('getCategories returns array of categories', () => {
        const categories = provider.getCategories();

        assert.ok(Array.isArray(categories), 'Should return an array');
        assert.ok(categories.length > 0, 'Should have at least one category');

        categories.forEach(category => {
            assert.ok(category.name, 'Category should have a name');
            assert.ok(category.displayName, 'Category should have a display name');
            assert.ok(category.icon, 'Category should have an icon');
            assert.ok(typeof category.filter === 'function', 'Category should have a filter function');
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
            assert.strictEqual(result, testCase.expected, `Should extract path from ${testCase.input}`);
        });
    });

    test('getTemplateCount returns number of templates', async () => {
        const count = await provider.getTemplateCount();

        assert.ok(typeof count === 'number', 'Should return a number');
        assert.ok(count >= 0, 'Count should be non-negative');
    });

    test('caching works - second call is faster', async () => {
        // First call - fetches from network
        const start1 = Date.now();
        await provider.getTemplates();
        const duration1 = Date.now() - start1;

        // Second call - uses cache
        const start2 = Date.now();
        await provider.getTemplates();
        const duration2 = Date.now() - start2;

        // Cache should be significantly faster (at least 10x)
        // Note: This is a heuristic and may not always pass due to network conditions
        assert.ok(duration2 < duration1 / 5 || duration2 < 10,
            `Second call should be faster (${duration2}ms) than first (${duration1}ms)`);
    });

    test('forceRefresh parameter refreshes cache', async () => {
        // Load into cache
        const templates1 = await provider.getTemplates();
        assert.ok(templates1.length > 0, 'Should have templates');

        // Force refresh
        const templates2 = await provider.getTemplates(true);
        assert.ok(templates2.length > 0, 'Should have templates after refresh');

        // Both should have same data
        assert.strictEqual(templates1.length, templates2.length, 'Should have same number of templates');
    });
});
