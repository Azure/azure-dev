import * as vscode from 'vscode';
import { HelpAndFeedbackTreeDataProvider } from './helpAndFeedback/HelpAndFeedbackTreeDataProvider';
import { MyProjectTreeDataProvider } from './myProject/MyProjectTreeDataProvider';

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
}
