// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

import * as assert from 'assert';
import * as sinon from 'sinon';
import * as vscode from 'vscode';
import { OpenInPortalStep } from '../../../commands/azureWorkspace/wizard/OpenInPortalStep';
import { RevealResourceWizardContext } from '../../../commands/azureWorkspace/wizard/PickResourceStep';

suite('OpenInPortalStep', () => {
    let step: OpenInPortalStep;
    let sandbox: sinon.SinonSandbox;

    setup(() => {
        sandbox = sinon.createSandbox();
        step = new OpenInPortalStep();
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
        test('constructs correct portal URL for Web App resource', async () => {
            const azureResourceId = '/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/my-rg/providers/Microsoft.Web/sites/my-app';
            const context: Partial<RevealResourceWizardContext> = {
                azureResourceId
            };

            const openExternalStub = sandbox.stub(vscode.env, 'openExternal').resolves(true);

            await step.execute(context as RevealResourceWizardContext);

            assert.ok(openExternalStub.calledOnce);
            const calledUri = openExternalStub.firstCall.args[0] as vscode.Uri;
            const expectedUri = vscode.Uri.parse(`https://portal.azure.com/#@/resource${azureResourceId}`);
            assert.strictEqual(calledUri.toString(), expectedUri.toString());
        });

        test('constructs correct portal URL for Storage Account resource', async () => {
            const azureResourceId = '/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/my-rg/providers/Microsoft.Storage/storageAccounts/mystorageaccount';
            const context: Partial<RevealResourceWizardContext> = {
                azureResourceId
            };

            const openExternalStub = sandbox.stub(vscode.env, 'openExternal').resolves(true);

            await step.execute(context as RevealResourceWizardContext);

            assert.ok(openExternalStub.calledOnce);
            const calledUri = openExternalStub.firstCall.args[0] as vscode.Uri;
            const expectedUri = vscode.Uri.parse(`https://portal.azure.com/#@/resource${azureResourceId}`);
            assert.strictEqual(calledUri.toString(), expectedUri.toString());
        });

        test('constructs correct portal URL for Cosmos DB resource', async () => {
            const azureResourceId = '/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/my-rg/providers/Microsoft.DocumentDB/databaseAccounts/mycosmosdb';
            const context: Partial<RevealResourceWizardContext> = {
                azureResourceId
            };

            const openExternalStub = sandbox.stub(vscode.env, 'openExternal').resolves(true);

            await step.execute(context as RevealResourceWizardContext);

            assert.ok(openExternalStub.calledOnce);
            const calledUri = openExternalStub.firstCall.args[0] as vscode.Uri;
            const expectedUri = vscode.Uri.parse(`https://portal.azure.com/#@/resource${azureResourceId}`);
            assert.strictEqual(calledUri.toString(), expectedUri.toString());
        });

        test('constructs correct portal URL for Resource Group', async () => {
            const azureResourceId = '/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/my-rg';
            const context: Partial<RevealResourceWizardContext> = {
                azureResourceId
            };

            const openExternalStub = sandbox.stub(vscode.env, 'openExternal').resolves(true);

            await step.execute(context as RevealResourceWizardContext);

            assert.ok(openExternalStub.calledOnce);
            const calledUri = openExternalStub.firstCall.args[0] as vscode.Uri;
            const expectedUri = vscode.Uri.parse(`https://portal.azure.com/#@/resource${azureResourceId}`);
            assert.strictEqual(calledUri.toString(), expectedUri.toString());
        });

        test('constructs correct portal URL for Container Apps resource', async () => {
            const azureResourceId = '/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/my-rg/providers/Microsoft.App/containerApps/my-container-app';
            const context: Partial<RevealResourceWizardContext> = {
                azureResourceId
            };

            const openExternalStub = sandbox.stub(vscode.env, 'openExternal').resolves(true);

            await step.execute(context as RevealResourceWizardContext);

            assert.ok(openExternalStub.calledOnce);
            const calledUri = openExternalStub.firstCall.args[0] as vscode.Uri;
            const expectedUri = vscode.Uri.parse(`https://portal.azure.com/#@/resource${azureResourceId}`);
            assert.strictEqual(calledUri.toString(), expectedUri.toString());
        });

        test('throws error when azureResourceId is missing', async () => {
            const context: Partial<RevealResourceWizardContext> = {};

            await assert.rejects(
                async () => await step.execute(context as RevealResourceWizardContext),
                (error: Error) => {
                    return error.message.includes('azureResourceId');
                }
            );
        });
    });

    suite('priority', () => {
        test('has correct priority value', () => {
            assert.strictEqual(step.priority, 100);
        });
    });
});
