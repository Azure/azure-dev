// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as assert from 'assert';
import * as sinon from 'sinon';
import * as vscode from 'vscode';
import { AzureResourcesExtensionApi } from '@microsoft/vscode-azureresources-api';
import { RevealStep } from '../../../commands/azureWorkspace/wizard/RevealStep';
import { RevealResourceWizardContext } from '../../../commands/azureWorkspace/wizard/PickResourceStep';
import * as getAzureResourceExtensionApiModule from '../../../utils/getAzureResourceExtensionApi';
import ext from '../../../ext';

suite('RevealStep', () => {
    let step: RevealStep;
    let sandbox: sinon.SinonSandbox;

    setup(() => {
        sandbox = sinon.createSandbox();
        step = new RevealStep();
        
        // Mock ext.outputChannel
        ext.outputChannel = {
            appendLog: sandbox.stub()
        } as Partial<typeof ext.outputChannel> as typeof ext.outputChannel;
    });

    teardown(() => {
        sandbox.restore();
    });

    suite('shouldExecute', () => {
        test('returns true when azureResourceId is present', () => {
            const context: Partial<RevealResourceWizardContext> = {
                azureResourceId: '/subscriptions/test-sub-id/resourceGroups/test-rg/providers/Microsoft.Web/sites/test-app'
            };

            const result = step.shouldExecute(context as RevealResourceWizardContext);

            assert.strictEqual(result, true);
        });

        test('returns false when azureResourceId is missing', () => {
            const context: Partial<RevealResourceWizardContext> = {};

            const result = step.shouldExecute(context as RevealResourceWizardContext);

            assert.strictEqual(result, false);
        });

        test('returns false when azureResourceId is empty string', () => {
            const context: Partial<RevealResourceWizardContext> = {
                azureResourceId: ''
            };

            const result = step.shouldExecute(context as RevealResourceWizardContext);

            assert.strictEqual(result, false);
        });
    });

    suite('execute', () => {
        let executeCommandStub: sinon.SinonStub;
        let getExtensionStub: sinon.SinonStub;
        let getAzureResourceExtensionApiStub: sinon.SinonStub;

        setup(() => {
            executeCommandStub = sandbox.stub(vscode.commands, 'executeCommand');
            getExtensionStub = sandbox.stub(vscode.extensions, 'getExtension');

            // Mock the Azure Resource Extension API
            const mockApi: Partial<AzureResourcesExtensionApi> = {
                resources: {
                    revealAzureResource: sandbox.stub().resolves(true)
                } as unknown as AzureResourcesExtensionApi['resources']
            };
            getAzureResourceExtensionApiStub = sandbox.stub(getAzureResourceExtensionApiModule, 'getAzureResourceExtensionApi').resolves(mockApi as AzureResourcesExtensionApi);
        });

        test('focuses Azure Resources view', async () => {
            const context: Partial<RevealResourceWizardContext> = {
                azureResourceId: '/subscriptions/test-sub-id/resourceGroups/test-rg/providers/Microsoft.Web/sites/test-app'
            };

            executeCommandStub.resolves();

            await step.execute(context as RevealResourceWizardContext);

            assert.ok(executeCommandStub.calledWith('azureResourceGroups.focus'));
        });

        test('activates appropriate extension for Microsoft.Web provider', async () => {
            const context: Partial<RevealResourceWizardContext> = {
                azureResourceId: '/subscriptions/test-sub-id/resourceGroups/test-rg/providers/Microsoft.Web/sites/test-app'
            };

            const mockExtension = {
                isActive: false,
                activate: sandbox.stub().resolves()
            };

            getExtensionStub.withArgs('ms-azuretools.vscode-azurefunctions').returns(mockExtension);
            executeCommandStub.resolves();

            await step.execute(context as RevealResourceWizardContext);

            assert.ok(mockExtension.activate.calledOnce);
        });

        test('activates appropriate extension for Microsoft.Storage provider', async () => {
            const context: Partial<RevealResourceWizardContext> = {
                azureResourceId: '/subscriptions/test-sub-id/resourceGroups/test-rg/providers/Microsoft.Storage/storageAccounts/test-storage'
            };

            const mockExtension = {
                isActive: false,
                activate: sandbox.stub().resolves()
            };

            getExtensionStub.withArgs('ms-azuretools.vscode-azurestorage').returns(mockExtension);
            executeCommandStub.resolves();

            await step.execute(context as RevealResourceWizardContext);

            assert.ok(mockExtension.activate.calledOnce);
        });

        test('activates appropriate extension for Microsoft.DocumentDB provider', async () => {
            const context: Partial<RevealResourceWizardContext> = {
                azureResourceId: '/subscriptions/test-sub-id/resourceGroups/test-rg/providers/Microsoft.DocumentDB/databaseAccounts/test-cosmos'
            };

            const mockExtension = {
                isActive: false,
                activate: sandbox.stub().resolves()
            };

            getExtensionStub.withArgs('ms-azuretools.azure-cosmos').returns(mockExtension);
            executeCommandStub.resolves();

            await step.execute(context as RevealResourceWizardContext);

            assert.ok(mockExtension.activate.calledOnce);
        });

        test('activates appropriate extension for Microsoft.App provider', async () => {
            const context: Partial<RevealResourceWizardContext> = {
                azureResourceId: '/subscriptions/test-sub-id/resourceGroups/test-rg/providers/Microsoft.App/containerApps/test-app'
            };

            const mockExtension = {
                isActive: false,
                activate: sandbox.stub().resolves()
            };

            getExtensionStub.withArgs('ms-azuretools.vscode-azurecontainerapps').returns(mockExtension);
            executeCommandStub.resolves();

            await step.execute(context as RevealResourceWizardContext);

            assert.ok(mockExtension.activate.calledOnce);
        });

        test('does not activate extension if already active', async () => {
            const context: Partial<RevealResourceWizardContext> = {
                azureResourceId: '/subscriptions/test-sub-id/resourceGroups/test-rg/providers/Microsoft.Web/sites/test-app'
            };

            const mockExtension = {
                isActive: true,
                activate: sandbox.stub().resolves()
            };

            getExtensionStub.withArgs('ms-azuretools.vscode-azurefunctions').returns(mockExtension);
            executeCommandStub.resolves();

            await step.execute(context as RevealResourceWizardContext);

            assert.ok(mockExtension.activate.notCalled);
        });

        test('attempts to refresh Azure Resources tree', async () => {
            const context: Partial<RevealResourceWizardContext> = {
                azureResourceId: '/subscriptions/test-sub-id/resourceGroups/test-rg'
            };

            executeCommandStub.resolves();

            await step.execute(context as RevealResourceWizardContext);

            assert.ok(executeCommandStub.calledWith('azureResourceGroups.refresh'));
        });

        test('calls revealAzureResource with correct resource ID and options', async () => {
            const azureResourceId = '/subscriptions/test-sub-id/resourceGroups/test-rg/providers/Microsoft.Web/sites/test-app';
            const context: Partial<RevealResourceWizardContext> = {
                azureResourceId
            };

            const mockRevealAzureResource = sandbox.stub().resolves(true);
            const mockApi: Partial<AzureResourcesExtensionApi> = {
                resources: {
                    revealAzureResource: mockRevealAzureResource
                } as unknown as AzureResourcesExtensionApi['resources']
            };
            getAzureResourceExtensionApiStub.resolves(mockApi as AzureResourcesExtensionApi);
            executeCommandStub.resolves();

            await step.execute(context as RevealResourceWizardContext);

            assert.ok(mockRevealAzureResource.called);
            assert.ok(mockRevealAzureResource.calledWith(azureResourceId, { select: true, focus: true, expand: true }));
        });

        test('attempts to reveal resource group first when resource has RG in path', async () => {
            const azureResourceId = '/subscriptions/test-sub-id/resourceGroups/test-rg/providers/Microsoft.Web/sites/test-app';
            const context: Partial<RevealResourceWizardContext> = {
                azureResourceId
            };

            const mockRevealAzureResource = sandbox.stub().resolves(true);
            const mockApi: Partial<AzureResourcesExtensionApi> = {
                resources: {
                    revealAzureResource: mockRevealAzureResource
                } as unknown as AzureResourcesExtensionApi['resources']
            };
            getAzureResourceExtensionApiStub.resolves(mockApi as AzureResourcesExtensionApi);
            executeCommandStub.resolves();

            await step.execute(context as RevealResourceWizardContext);

            // Should be called twice: once for RG, once for the resource
            assert.ok(mockRevealAzureResource.callCount >= 2);

            // First call should be for the resource group
            const rgResourceId = '/subscriptions/test-sub-id/resourceGroups/test-rg';
            assert.ok(mockRevealAzureResource.calledWith(rgResourceId, { select: false, focus: false, expand: true }));
        });

        test('shows error message when reveal fails', async () => {
            const context: Partial<RevealResourceWizardContext> = {
                azureResourceId: '/subscriptions/test-sub-id/resourceGroups/test-rg/providers/Microsoft.Web/sites/test-app'
            };

            const mockApi: Partial<AzureResourcesExtensionApi> = {
                resources: {
                    revealAzureResource: sandbox.stub().rejects(new Error('Reveal failed'))
                } as unknown as AzureResourcesExtensionApi['resources']
            };
            getAzureResourceExtensionApiStub.resolves(mockApi as AzureResourcesExtensionApi);
            executeCommandStub.resolves();

            const showErrorMessageStub = sandbox.stub(vscode.window, 'showErrorMessage').resolves();

            await assert.rejects(
                async () => await step.execute(context as RevealResourceWizardContext),
                (error: Error) => error.message === 'Reveal failed'
            );

            assert.ok(showErrorMessageStub.called);
        });

        test('shows info message with Copy and Portal options when reveal returns undefined', async () => {
            const azureResourceId = '/subscriptions/test-sub-id/resourceGroups/test-rg/providers/Microsoft.Web/sites/test-app';
            const context: Partial<RevealResourceWizardContext> = {
                azureResourceId
            };

            const mockApi: Partial<AzureResourcesExtensionApi> = {
                resources: {
                    revealAzureResource: sandbox.stub().resolves(undefined)
                } as unknown as AzureResourcesExtensionApi['resources']
            };
            getAzureResourceExtensionApiStub.resolves(mockApi as AzureResourcesExtensionApi);
            // Make executeCommand fail for the alternative reveal command but succeed for others
            executeCommandStub.callsFake((command: string) => {
                if (command === 'azureResourceGroups.revealResource') {
                    return Promise.reject(new Error('Alternative reveal failed'));
                }
                return Promise.resolve();
            });

            const showInfoMessageStub = sandbox.stub(vscode.window, 'showInformationMessage').resolves();

            await step.execute(context as RevealResourceWizardContext);

            assert.ok(showInfoMessageStub.called);
            assert.ok(showInfoMessageStub.firstCall.args[0].includes('Unable to automatically reveal resource'));
        });
    });

    suite('priority', () => {
        test('has correct priority value', () => {
            assert.strictEqual(step.priority, 100);
        });
    });
});
