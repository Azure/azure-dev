// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { expect } from 'chai';
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

        expect(children, 'Should return an array').to.be.an('array');
        expect(children.length, 'Should have root items').to.be.greaterThan(0);

        // Should have Quick Start section when no azure.yaml
        const hasQuickStart = children.some(child => child.label === 'Quick Start');
        expect(hasQuickStart, 'Should have Quick Start section when no azure.yaml').to.be.true;
    });

    test('getChildren does not show Quick Start when azure.yaml exists', async () => {
        // Simulate azure.yaml exists in workspace
        const mockUri = vscode.Uri.file('/test/azure.yaml');
        workspaceFindFilesStub.resolves([mockUri]);

        const children = await provider.getChildren();

        expect(children, 'Should return an array').to.be.an('array');

        // Should NOT have Quick Start section when azure.yaml exists
        const hasQuickStart = children.some(child => child.label === 'Quick Start');
        expect(hasQuickStart, 'Should not have Quick Start section when azure.yaml exists').to.be.false;
    });

    test('getTreeItem returns the same tree item', async () => {
        workspaceFindFilesStub.resolves([]);
        const children = await provider.getChildren();

        if (children.length > 0) {
            const treeItem = provider.getTreeItem(children[0]);
            expect(treeItem, 'Should return the same tree item').to.equal(children[0]);
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
        expect(hasCategoryGroup, 'Should have category group').to.be.true;
    });

    test('root items include AI templates', async () => {
        workspaceFindFilesStub.resolves([]);

        const children = await provider.getChildren();

        const hasAITemplates = children.some(child => child.label === 'AI Templates');
        expect(hasAITemplates, 'Should have AI templates section').to.be.true;
    });

    test('root items include search', async () => {
        workspaceFindFilesStub.resolves([]);

        const children = await provider.getChildren();

        const hasSearch = children.some(child => child.label === 'Search Templates...');
        expect(hasSearch, 'Should have search option').to.be.true;
    });

    test('Quick Start items have correct properties', async () => {
        workspaceFindFilesStub.resolves([]);

        const rootChildren = await provider.getChildren();
        const quickStartGroup = rootChildren.find(child => child.label === 'Quick Start');

        expect(quickStartGroup, 'Should have Quick Start group').to.exist;

        if (quickStartGroup) {
            const quickStartItems = await provider.getChildren(quickStartGroup);

            expect(quickStartItems.length, 'Should have at least 3 Quick Start items').to.be.at.least(3);

            const initFromCode = quickStartItems.find(item =>
                (item.label as string).includes('Initialize from Current Code')
            );
            expect(initFromCode, 'Should have Initialize from Code option').to.exist;
            expect(initFromCode!.command, 'Should have command').to.exist;

            const initMinimal = quickStartItems.find(item =>
                (item.label as string).includes('Create Minimal Project')
            );
            expect(initMinimal, 'Should have Create Minimal option').to.exist;
            expect(initMinimal!.command, 'Should have command').to.exist;

            const browseGallery = quickStartItems.find(item =>
                (item.label as string).includes('Browse Template Gallery')
            );
            expect(browseGallery, 'Should have Browse Gallery option').to.exist;
            expect(browseGallery!.command, 'Should have command').to.exist;
        }
    });

    test('search item has command configured', async () => {
        workspaceFindFilesStub.resolves([]);

        const children = await provider.getChildren();
        const searchItem = children.find(child => child.label === 'Search Templates...');

        expect(searchItem, 'Should have search item').to.exist;
        expect(searchItem!.command, 'Search item should have command').to.exist;
        expect(
            searchItem!.command!.command,
            'Should have correct command ID'
        ).to.equal('azure-dev.views.templateTools.search');
    });

    test('template item opens README on click', async () => {
        const provider = new TemplateToolsTreeDataProvider();
        workspaceFindFilesStub.resolves([]);

        // Get AI templates section children
        const rootItems = await provider.getChildren();
        const aiSection = rootItems.find((item: vscode.TreeItem) => item.contextValue === 'aiTemplates');
        const templateItems = await provider.getChildren(aiSection);
        const templateItem = templateItems[0] as vscode.TreeItem & { command?: vscode.Command };

        expect(
            templateItem.command?.command,
            'Should open README on click'
        ).to.equal('azure-dev.views.templateTools.openReadme');
        expect(
            templateItem.command?.arguments,
            'Should have command arguments'
        ).to.exist;
    });

    test('template item has correct context value for inline actions', async () => {
        const provider = new TemplateToolsTreeDataProvider();
        workspaceFindFilesStub.resolves([]);

        // Get AI templates section children
        const rootItems = await provider.getChildren();
        const aiSection = rootItems.find((item: vscode.TreeItem) => item.contextValue === 'aiTemplates');
        const templateItems = await provider.getChildren(aiSection);
        const templateItem = templateItems[0] as vscode.TreeItem;

        expect(
            templateItem.contextValue,
            'Should have template context value for inline menu actions'
        ).to.equal('ms-azuretools.azure-dev.views.templateTools.template');
    });
});
