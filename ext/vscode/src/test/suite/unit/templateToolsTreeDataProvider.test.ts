// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as assert from 'assert';
import * as vscode from 'vscode';
import * as sinon from 'sinon';
import { TemplateToolsTreeDataProvider } from '../../../views/templateTools/TemplateToolsTreeDataProvider';

suite('TemplateToolsTreeDataProvider', () => {
    let provider: TemplateToolsTreeDataProvider;
    let workspaceFindFilesStub: sinon.SinonStub;

    setup(() => {
        provider = new TemplateToolsTreeDataProvider();
        workspaceFindFilesStub = sinon.stub(vscode.workspace, 'findFiles');
    });

    teardown(() => {
        provider.dispose();
        sinon.restore();
    });

    test('getChildren returns root items when no element provided', async () => {
        // Simulate no azure.yaml in workspace
        workspaceFindFilesStub.resolves([]);

        const children = await provider.getChildren();

        assert.ok(Array.isArray(children), 'Should return an array');
        assert.ok(children.length > 0, 'Should have root items');

        // Should have Quick Start section when no azure.yaml
        const hasQuickStart = children.some(child => child.label === 'Quick Start');
        assert.ok(hasQuickStart, 'Should have Quick Start section when no azure.yaml');
    });

    test('getChildren does not show Quick Start when azure.yaml exists', async () => {
        // Simulate azure.yaml exists in workspace
        const mockUri = vscode.Uri.file('/test/azure.yaml');
        workspaceFindFilesStub.resolves([mockUri]);

        const children = await provider.getChildren();

        assert.ok(Array.isArray(children), 'Should return an array');

        // Should NOT have Quick Start section when azure.yaml exists
        const hasQuickStart = children.some(child => child.label === 'Quick Start');
        assert.ok(!hasQuickStart, 'Should not have Quick Start section when azure.yaml exists');
    });

    test('getTreeItem returns the same tree item', async () => {
        workspaceFindFilesStub.resolves([]);
        const children = await provider.getChildren();

        if (children.length > 0) {
            const treeItem = provider.getTreeItem(children[0]);
            assert.strictEqual(treeItem, children[0], 'Should return the same tree item');
        }
    });

    test('refresh fires onDidChangeTreeData event', (done) => {
        workspaceFindFilesStub.resolves([]);

        provider.onDidChangeTreeData(() => {
            done();
        });

        provider.refresh();
    });

    test('root items include category group', async () => {
        workspaceFindFilesStub.resolves([]);

        const children = await provider.getChildren();

        const hasCategoryGroup = children.some(child => child.label === 'Browse by Category');
        assert.ok(hasCategoryGroup, 'Should have category group');
    });

    test('root items include AI templates', async () => {
        workspaceFindFilesStub.resolves([]);

        const children = await provider.getChildren();

        const hasAITemplates = children.some(child => child.label === 'AI Templates');
        assert.ok(hasAITemplates, 'Should have AI templates section');
    });

    test('root items include search', async () => {
        workspaceFindFilesStub.resolves([]);

        const children = await provider.getChildren();

        const hasSearch = children.some(child => child.label === 'Search Templates...');
        assert.ok(hasSearch, 'Should have search option');
    });

    test('Quick Start items have correct properties', async () => {
        workspaceFindFilesStub.resolves([]);

        const rootChildren = await provider.getChildren();
        const quickStartGroup = rootChildren.find(child => child.label === 'Quick Start');

        assert.ok(quickStartGroup, 'Should have Quick Start group');

        if (quickStartGroup) {
            const quickStartItems = await provider.getChildren(quickStartGroup);

            assert.ok(quickStartItems.length >= 3, 'Should have at least 3 Quick Start items');

            const initFromCode = quickStartItems.find(item =>
                (item.label as string).includes('Initialize from Current Code')
            );
            assert.ok(initFromCode, 'Should have Initialize from Code option');
            assert.ok(initFromCode.command, 'Should have command');

            const initMinimal = quickStartItems.find(item =>
                (item.label as string).includes('Create Minimal Project')
            );
            assert.ok(initMinimal, 'Should have Create Minimal option');
            assert.ok(initMinimal.command, 'Should have command');

            const browseGallery = quickStartItems.find(item =>
                (item.label as string).includes('Browse Template Gallery')
            );
            assert.ok(browseGallery, 'Should have Browse Gallery option');
            assert.ok(browseGallery.command, 'Should have command');
        }
    });

    test('search item has command configured', async () => {
        workspaceFindFilesStub.resolves([]);

        const children = await provider.getChildren();
        const searchItem = children.find(child => child.label === 'Search Templates...');

        assert.ok(searchItem, 'Should have search item');
        assert.ok(searchItem.command, 'Search item should have command');
        assert.strictEqual(
            searchItem.command.command,
            'azure-dev.views.templateTools.search',
            'Should have correct command ID'
        );
    });

    test('template item opens README on click', async () => {
        const provider = new TemplateToolsTreeDataProvider();
        workspaceFindFilesStub.resolves([]);

        // Get AI templates section children
        const rootItems = await provider.getChildren();
        const aiSection = rootItems.find((item: vscode.TreeItem) => item.contextValue === 'aiTemplates');
        const templateItems = await provider.getChildren(aiSection);
        const templateItem = templateItems[0] as vscode.TreeItem & { command?: vscode.Command };

        assert.strictEqual(
            templateItem.command?.command,
            'azure-dev.views.templateTools.openReadme',
            'Should open README on click'
        );
        assert.ok(
            templateItem.command?.arguments,
            'Should have command arguments'
        );
    });

    test('template item has correct context value for inline actions', async () => {
        const provider = new TemplateToolsTreeDataProvider();
        workspaceFindFilesStub.resolves([]);

        // Get AI templates section children
        const rootItems = await provider.getChildren();
        const aiSection = rootItems.find((item: vscode.TreeItem) => item.contextValue === 'aiTemplates');
        const templateItems = await provider.getChildren(aiSection);
        const templateItem = templateItems[0] as vscode.TreeItem;

        assert.strictEqual(
            templateItem.contextValue,
            'template',
            'Should have template context value for inline menu actions'
        );
    });
});
