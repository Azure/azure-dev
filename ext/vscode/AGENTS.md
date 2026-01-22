# Agent Development Guide

A file for [guiding coding agents](https://agents.md/).

## Commands

- **Install dependencies:** `npm install`
- **Build:** `npm run build`
- **Lint:** `npm run lint`
- **Spell Check:** `npx cspell "src/**/*.ts" --config .vscode/cspell.yaml`
- **Unit Tests:** `npm run unit-test`
- **Watch mode:** `npm run watch`
- **Package extension:** `npm run package`

## Directory Structure

- Extension entry point: `src/extension.ts`
- Commands: `src/commands/`
- Language features: `src/language/` (IntelliSense, diagnostics, etc.)
- Views & tree providers: `src/views/`
- Utilities: `src/utils/`
- Tests: `src/test/`
- Constants: `src/constants/`
- Services: `src/services/`

## Pre-Commit Checklist

**IMPORTANT**: Always run these checks before committing to avoid pipeline failures:

1. Run `npm run lint` and fix all issues
2. Run `npx cspell "src/**/*.ts" --config .vscode/cspell.yaml` and fix spelling errors
3. Run `npm run unit-test` and ensure all tests pass
4. Update README.md if functionality changed
5. Verify no merge conflict markers in code

**Testing Requirements**:
- If you modify existing code, ensure related tests still pass
- If you add new functionality, add corresponding unit tests
- Add new words to `.vscode/cspell-dictionary.txt` if cspell flags valid technical terms

## Code Conventions

### Copyright Headers
All TypeScript source files MUST include the Microsoft copyright header:
```typescript
// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
```

### Localization
- All user-facing strings shown in the UI, error messages, etc. must use `vscode.l10n.t()`
- All user-facing strings in package.json must be extracted into package.nls.json

### UI Best Practices
- Instead of `vscode.window.showQuickPick`, use `IActionContext.ui.showQuickPick`
- Instead of `vscode.window.showInputBox`, use `IActionContext.ui.showInputBox`
- Same for `showWarningMessage`, `showOpenDialog`, and `showWorkspaceFolderPick`

### Resource Management
- FileSystemWatchers are a scarce resource on some systems - use the shared `FileSystemWatcherService`
- Dispose resources properly using `vscode.Disposable` pattern
- Use `ExtensionContext.subscriptions` for cleanup

### Testing
- Use Mocha for test framework
- Use Chai for assertions
- Mock VS Code APIs using Sinon
- Keep tests focused and isolated
- Use `UserCancelledError` from `@microsoft/vscode-azext-utils` for testing user cancellation scenarios
