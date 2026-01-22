// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import { expect } from 'chai';
import * as sinon from 'sinon';
import * as vscode from 'vscode';
import { AzureResourcesExtensionApi } from '@microsoft/vscode-azureresources-api';
import { RevealStep } from '../../../commands/azureWorkspace/wizard/RevealStep';
import { RevealResourceWizardContext } from '../../../commands/azureWorkspace/wizard/PickResourceStep';
import * as getAzureResourceExtensionApiModule from '../../../utils/getAzureResourceExtensionApi';

suite('RevealStep', () => {
    let step: RevealStep;
    let sandbox: sinon.SinonSandbox;

    setup(() => {
        sandbox = sinon.createSandbox();
        step = new RevealStep();
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

            expect(result).to.equal(true);
        });

        test('returns false when azureResourceId is missing', () => {
            const context: Partial<RevealResourceWizardContext> = {};

            const result = step.shouldExecute(context as RevealResourceWizardContext);

            expect(result).to.equal(false);
        });

        test('returns false when azureResourceId is empty string', () => {
            const context: Partial<RevealResourceWizardContext> = {
                azureResourceId: ''
            };

            const result = step.shouldExecute(context as RevealResourceWizardContext);

            expect(result).to.equal(false);
        });
    });

    suite('execute', () => {
        let executeCommandStub: sinon.SinonStub;
        let getAzureResourceExtensionApiStub: sinon.SinonStub;

        setup(() => {
            executeCommandStub = sandbox.stub(vscode.commands, 'executeCommand');

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

            expect(executeCommandStub.calledWith('azureResourceGroups.focus')).to.be.true;
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

            expect(mockRevealAzureResource.called).to.be.true;
            expect(mockRevealAzureResource.calledWith(azureResourceId, { select: true, focus: true, expand: true })).to.be.true;
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
            executeCommandStub.resolves();

            const showInfoMessageStub = sandbox.stub(vscode.window, 'showInformationMessage').resolves();

            await step.execute(context as RevealResourceWizardContext);

            expect(showInfoMessageStub.called).to.be.true;
        });

        test('opens portal when Open in Portal is selected', async () => {
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
            executeCommandStub.resolves();

            // Return the "Open in Portal" option
            const showInfoMessageStub = sandbox.stub(vscode.window, 'showInformationMessage');
            // eslint-disable-next-line @typescript-eslint/no-explicit-any
            (showInfoMessageStub as any).resolves(vscode.l10n.t('Open in Portal'));

            await step.execute(context as RevealResourceWizardContext);

            expect(executeCommandStub.calledWith('azureResourceGroups.openInPortal', azureResourceId)).to.be.true;
        });
    });

    suite('priority', () => {
        test('has correct priority value', () => {
            expect(step.priority).to.equal(100);
        });
    });
});
