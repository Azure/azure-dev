// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { expect } from 'chai';
import * as vscode from 'vscode';
import * as sinon from 'sinon';
import { EnvironmentsTreeDataProvider, EnvironmentTreeItem, EnvironmentItem, EnvironmentVariableItem } from '../../../views/environments/EnvironmentsTreeDataProvider';
import { WorkspaceAzureDevApplicationProvider } from '../../../services/AzureDevApplicationProvider';
import { WorkspaceAzureDevEnvListProvider } from '../../../services/AzureDevEnvListProvider';
import { WorkspaceAzureDevEnvValuesProvider } from '../../../services/AzureDevEnvValuesProvider';
import { FileSystemWatcherService } from '../../../services/FileSystemWatcherService';

suite('EnvironmentsTreeDataProvider', () => {
    let provider: EnvironmentsTreeDataProvider;
    let sandbox: sinon.SinonSandbox;
    let appProviderStub: sinon.SinonStubbedInstance<WorkspaceAzureDevApplicationProvider>;
    let envListProviderStub: sinon.SinonStubbedInstance<WorkspaceAzureDevEnvListProvider>;
    let envValuesProviderStub: sinon.SinonStubbedInstance<WorkspaceAzureDevEnvValuesProvider>;
    let fileSystemWatcherService: FileSystemWatcherService;

    setup(() => {
        sandbox = sinon.createSandbox();
        fileSystemWatcherService = new FileSystemWatcherService();
        provider = new EnvironmentsTreeDataProvider(fileSystemWatcherService);

        // Stub the providers
        appProviderStub = sandbox.stub(WorkspaceAzureDevApplicationProvider.prototype);
        envListProviderStub = sandbox.stub(WorkspaceAzureDevEnvListProvider.prototype);
        envValuesProviderStub = sandbox.stub(WorkspaceAzureDevEnvValuesProvider.prototype);
    });

    teardown(() => {
        provider.dispose();
        fileSystemWatcherService.dispose();
        sandbox.restore();
    });

    suite('getChildren', () => {
        test('returns empty array when no applications are found', async () => {
            appProviderStub.getApplications.resolves([]);

            const children = await provider.getChildren();

            expect(children).to.have.lengthOf(0);
        });

        test('returns environment items when applications exist', async () => {
            const mockConfigPath = vscode.Uri.file('/test/azure.yaml');
            const mockWorkspaceFolder = { uri: vscode.Uri.file('/test'), name: 'test', index: 0 };
            appProviderStub.getApplications.resolves([
                {
                    configurationPath: mockConfigPath,
                    configurationFolder: '/test',
                    workspaceFolder: mockWorkspaceFolder as vscode.WorkspaceFolder
                }
            ]);

            envListProviderStub.getEnvListResults.resolves([
                { Name: 'dev', IsDefault: true, DotEnvPath: '.azure/dev/.env' },
                { Name: 'prod', IsDefault: false, DotEnvPath: '.azure/prod/.env' }
            ]);

            const children = await provider.getChildren();

            expect(children).to.have.lengthOf(2);
            expect(children[0].label).to.equal('dev');
            expect(children[0].type).to.equal('Environment');
            expect(children[0].description).to.equal('(Current)');
            expect(children[1].label).to.equal('prod');
        });

        test('marks default environment with appropriate icon and description', async () => {
            const mockConfigPath = vscode.Uri.file('/test/azure.yaml');
            const mockWorkspaceFolder = { uri: vscode.Uri.file('/test'), name: 'test', index: 0 };
            appProviderStub.getApplications.resolves([
                {
                    configurationPath: mockConfigPath,
                    configurationFolder: '/test',
                    workspaceFolder: mockWorkspaceFolder as vscode.WorkspaceFolder
                }
            ]);

            envListProviderStub.getEnvListResults.resolves([
                { Name: 'dev', IsDefault: true, DotEnvPath: '.azure/dev/.env' }
            ]);

            const children = await provider.getChildren();

            expect(children).to.have.lengthOf(1);
            expect(children[0].description).to.equal('(Current)');
            expect(children[0].contextValue).to.include('default');
        });

        test('returns environment details when environment node is expanded', async () => {
            const mockEnvItem: EnvironmentItem = {
                name: 'dev',
                isDefault: true,
                configurationFile: vscode.Uri.file('/test/azure.yaml')
            };

            const envTreeItem = new EnvironmentTreeItem(
                'Environment',
                'dev',
                vscode.TreeItemCollapsibleState.Collapsed,
                mockEnvItem
            );

            const children = await provider.getChildren(envTreeItem);

            expect(children.length).to.be.greaterThan(0);
            expect(children[0].label).to.equal('Environment Variables');
            expect(children[0].type).to.equal('Group');
        });

        test('returns environment variables when variables group is expanded', async () => {
            const mockEnvItem: EnvironmentItem = {
                name: 'dev',
                isDefault: true,
                configurationFile: vscode.Uri.file('/test/azure.yaml')
            };

            const variablesGroup = new EnvironmentTreeItem(
                'Group',
                'Environment Variables',
                vscode.TreeItemCollapsibleState.Collapsed,
                mockEnvItem
            );

            envValuesProviderStub.getEnvValues.resolves({
                // eslint-disable-next-line @typescript-eslint/naming-convention
                'AZURE_SUBSCRIPTION_ID': 'test-sub-id',
                // eslint-disable-next-line @typescript-eslint/naming-convention
                'AZURE_LOCATION': 'eastus'
            });

            const children = await provider.getChildren(variablesGroup);

            expect(children).to.have.lengthOf(2);
            expect(children[0].type).to.equal('Variable');
            expect(typeof children[0].label === 'string' && children[0].label.includes('Hidden value')).to.be.true;
        });
    });

    suite('toggleVisibility', () => {
        test('toggles environment variable visibility from hidden to visible', () => {
            const mockEnvVarItem: EnvironmentVariableItem = {
                name: 'dev',
                isDefault: true,
                configurationFile: vscode.Uri.file('/test/azure.yaml'),
                key: 'AZURE_SUBSCRIPTION_ID',
                value: 'test-sub-id'
            };

            const varTreeItem = new EnvironmentTreeItem(
                'Variable',
                'AZURE_SUBSCRIPTION_ID=Hidden value. Click to view.',
                vscode.TreeItemCollapsibleState.None,
                mockEnvVarItem
            );

            provider.toggleVisibility(varTreeItem);

            // After toggling, getTreeItem should return a new tree item with visible value
            const updatedTreeItem = provider.getTreeItem(varTreeItem);
            expect(typeof updatedTreeItem.label === 'string' && updatedTreeItem.label.includes('test-sub-id')).to.be.true;
            expect(typeof updatedTreeItem.tooltip === 'string' && updatedTreeItem.tooltip.includes('test-sub-id')).to.be.true;
        });

        test('toggles environment variable visibility from visible to hidden', () => {
            const mockEnvVarItem: EnvironmentVariableItem = {
                name: 'dev',
                isDefault: true,
                configurationFile: vscode.Uri.file('/test/azure.yaml'),
                key: 'AZURE_SUBSCRIPTION_ID',
                value: 'test-sub-id'
            };

            const varTreeItem = new EnvironmentTreeItem(
                'Variable',
                'AZURE_SUBSCRIPTION_ID=test-sub-id',
                vscode.TreeItemCollapsibleState.None,
                mockEnvVarItem
            );

            // First toggle to visible
            provider.toggleVisibility(varTreeItem);
            // Second toggle to hidden
            provider.toggleVisibility(varTreeItem);

            // After toggling back, getTreeItem should return a new tree item with hidden value
            const updatedTreeItem = provider.getTreeItem(varTreeItem);
            expect(typeof updatedTreeItem.label === 'string' && updatedTreeItem.label.includes('Hidden value')).to.be.true;
            expect(typeof updatedTreeItem.tooltip === 'string' && updatedTreeItem.tooltip.includes('Click to view value')).to.be.true;
        });

        test('does not toggle visibility for non-variable items', () => {
            const mockEnvItem: EnvironmentItem = {
                name: 'dev',
                isDefault: true,
                configurationFile: vscode.Uri.file('/test/azure.yaml')
            };

            const envTreeItem = new EnvironmentTreeItem(
                'Environment',
                'dev',
                vscode.TreeItemCollapsibleState.Collapsed,
                mockEnvItem
            );

            const originalLabel = envTreeItem.label;
            provider.toggleVisibility(envTreeItem);

            expect(envTreeItem.label).to.equal(originalLabel);
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

    suite('getTreeItem', () => {
        test('returns the same tree item passed in', () => {
            const mockEnvItem: EnvironmentItem = {
                name: 'dev',
                isDefault: true,
                configurationFile: vscode.Uri.file('/test/azure.yaml')
            };

            const treeItem = new EnvironmentTreeItem(
                'Environment',
                'dev',
                vscode.TreeItemCollapsibleState.Collapsed,
                mockEnvItem
            );

            const result = provider.getTreeItem(treeItem);

            expect(result).to.equal(treeItem);
        });
    });
});
