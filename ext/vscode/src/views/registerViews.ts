import * as vscode from 'vscode';
import { HelpAndFeedbackTreeDataProvider } from './helpAndFeedback/HelpAndFeedbackTreeDataProvider';
import { MyProjectTreeDataProvider } from './myProject/MyProjectTreeDataProvider';
import { EnvironmentsTreeDataProvider } from './environments/EnvironmentsTreeDataProvider';

export function registerViews(context: vscode.ExtensionContext): void {
    const helpAndFeedbackProvider = new HelpAndFeedbackTreeDataProvider();
    context.subscriptions.push(
        vscode.window.registerTreeDataProvider('azure-dev.views.helpAndFeedback', helpAndFeedbackProvider)
    );

    const myProjectProvider = new MyProjectTreeDataProvider();
    context.subscriptions.push(
        vscode.window.registerTreeDataProvider('azure-dev.views.myProject', myProjectProvider)
    );
    context.subscriptions.push(myProjectProvider);

    const environmentsProvider = new EnvironmentsTreeDataProvider();
    context.subscriptions.push(
        vscode.window.registerTreeDataProvider('azure-dev.views.environments', environmentsProvider)
    );
    context.subscriptions.push(environmentsProvider);
}
