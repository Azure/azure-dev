// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { expect } from 'chai';
import * as sinon from 'sinon';
import { ExtensionsTreeDataProvider, ExtensionTreeItem } from '../../../views/extensions/ExtensionsTreeDataProvider';
import { WorkspaceAzureDevExtensionProvider, AzureDevExtension } from '../../../services/AzureDevExtensionProvider';

suite('ExtensionsTreeDataProvider', () => {
    let provider: ExtensionsTreeDataProvider;
    let sandbox: sinon.SinonSandbox;
    let extensionProviderStub: sinon.SinonStubbedInstance<WorkspaceAzureDevExtensionProvider>;

    setup(() => {
        sandbox = sinon.createSandbox();
        provider = new ExtensionsTreeDataProvider();

        // Stub the extension provider
        extensionProviderStub = sandbox.stub(WorkspaceAzureDevExtensionProvider.prototype);
    });

    teardown(() => {
        sandbox.restore();
    });

    suite('getChildren', () => {
        test('returns empty array when no extensions are installed', async () => {
            extensionProviderStub.getExtensionListResults.resolves([]);

            const children = await provider.getChildren();

            expect(children).to.have.lengthOf(0);
        });

        test('returns extension items when extensions are installed', async () => {
            const mockExtensions: AzureDevExtension[] = [
                { id: 'test-ext-1', name: 'test-extension-1', version: '1.0.0' },
                { id: 'test-ext-2', name: 'test-extension-2', version: '2.1.3' }
            ];

            extensionProviderStub.getExtensionListResults.resolves(mockExtensions);

            const children = await provider.getChildren();

            expect(children).to.have.lengthOf(2);
            expect(children[0].extension.name).to.equal('test-extension-1');
            expect(children[0].extension.version).to.equal('1.0.0');
            expect(children[0].description).to.equal('1.0.0');
            expect(children[1].extension.name).to.equal('test-extension-2');
            expect(children[1].extension.version).to.equal('2.1.3');
        });

        test('returns empty array for children of extension items', async () => {
            const mockExtension: AzureDevExtension = {
                id: 'test-ext',
                name: 'test-extension',
                version: '1.0.0'
            };

            const extensionTreeItem = new ExtensionTreeItem(mockExtension);

            const children = await provider.getChildren(extensionTreeItem);

            expect(children).to.have.lengthOf(0);
        });
    });

    suite('getTreeItem', () => {
        test('returns the same tree item passed in', () => {
            const mockExtension: AzureDevExtension = {
                id: 'test-ext',
                name: 'test-extension',
                version: '1.0.0'
            };

            const treeItem = new ExtensionTreeItem(mockExtension);
            const result = provider.getTreeItem(treeItem);

            expect(result).to.equal(treeItem);
        });
    });

    suite('refresh', () => {
        test('fires onDidChangeTreeData event when refresh is called', (done) => {
            provider.onDidChangeTreeData(() => {
                done();
            });

            provider.refresh();
        });
    });

    suite('ExtensionTreeItem', () => {
        test('creates tree item with correct properties', () => {
            const mockExtension: AzureDevExtension = {
                id: 'my-ext',
                name: 'my-extension',
                version: '3.2.1'
            };

            const treeItem = new ExtensionTreeItem(mockExtension);

            expect(treeItem.label).to.equal('my-extension');
            expect(treeItem.description).to.equal('3.2.1');
            expect(treeItem.contextValue).to.equal('ms-azuretools.azure-dev.views.extensions.extension');
            expect(treeItem.collapsibleState).to.equal(0); // None
        });
    });
});
