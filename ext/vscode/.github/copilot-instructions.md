# Azure Developer CLI VS Code Extension - Copilot Instructions

<!-- cspell:ignore Chdir azext azureresources -->

## Project overview
This is the official Visual Studio Code extension for the Azure Developer CLI (azd). It provides an integrated development experience for building, deploying, and managing Azure applications.

Run the commands below from `ext/vscode/`. Install dependencies with `npm install`
when needed.

## Core development principles

### Documentation
- **Always keep the [README.md](../README.md) up to date** with any changes to:
  - Features and functionality
  - Commands and usage
  - Configuration options
  - Installation instructions
  - Prerequisites
  - Known issues or limitations

### Code quality and testing
Before committing, submitting changes, or pushing code, run these checks:

1. **Linting**: `npm run lint`
   - Ensures code follows TypeScript and ESLint standards
   - Fix any linting errors before committing

2. **Spell Check**: `npx cspell "src/**/*.ts" --config .vscode/cspell.yaml`
   - Checks for spelling errors in source code
   - Add technical terms to `.vscode/cspell-dictionary.txt` if needed

3. **Unit Tests**: `npm run unit-test`
   - Runs fast unit tests without full VS Code integration
   - All tests must pass before committing
   - **IMPORTANT**: If you modify existing code, ensure related tests still pass
   - **IMPORTANT**: If you add new functionality, add corresponding unit tests

Update [README.md](../README.md) if functionality changed. Verify that merge
conflicts are resolved and no `<<<<<<<`, `=======`, or `>>>>>>>` markers remain.

## Code style and conventions

### File organization
- Extension entry point: `src/extension.ts`
- Commands: `src/commands/`
- Language features: `src/language/` (IntelliSense, diagnostics, etc.)
- Views & tree providers: `src/views/`
- Utilities: `src/utils/`
- Tests: `src/test/`
- Constants: `src/constants/`
- Services: `src/services/`

### Naming conventions
- Use PascalCase for classes and interfaces
- Use camelCase for functions, methods, and variables
- Use descriptive names that clearly indicate purpose
- Prefix private members with underscore if needed for clarity

### Copyright headers
All TypeScript source files MUST include the Microsoft copyright header at the very top of the file:
```typescript
// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
```

### TypeScript guidelines
- Use explicit types where possible, avoid `any`
- Use VS Code API types from the `vscode` module
- Use `async/await` for asynchronous operations
- Handle errors gracefully with try/catch blocks

### Azure YAML language features
When working on `azure.yaml` language support in `src/language/`:
- Use YAML parser from `yaml` package
- Provide helpful diagnostics with clear error messages
- Use `vscode.l10n.t()` for all user-facing strings
- Test with various `azure.yaml` configurations

### Testing
- Write unit tests for new features in `src/test/suite/unit/`
- Use Mocha for test framework
- Use Chai for assertions
- Mock VS Code APIs when necessary using Sinon
- Keep tests focused and isolated
- Use `UserCancelledError` from `@microsoft/vscode-azext-utils` for testing user cancellation scenarios

## Common tasks

### Adding a new command
1. Create command handler in `src/commands/`
2. Register in `src/commands/registerCommands.ts`
3. Add to `package.json` contributions
4. Add localized strings to `package.nls.json`
5. Update README.md with new command documentation
6. Add tests for the command

### Adding language features
1. Create provider in `src/language/`
2. Register in `src/language/languageFeatures.ts`
3. Test with various `azure.yaml` files
4. Add diagnostics tests in `src/test/suite/unit/`

### Debugging the extension
- Press F5 to launch Extension Development Host
- Set breakpoints in TypeScript source
- Use Debug Console for logging
- Check Output > Azure Developer CLI for extension logs

## VS Code extension APIs
- Follow [VS Code Extension API](https://code.visualstudio.com/api) best practices
- Use `@microsoft/vscode-azext-utils` for Azure extension utilities
- Integrate with Azure Resources API via `@microsoft/vscode-azureresources-api`
- Use localization with `vscode.l10n.t()` for all user-facing text

## Performance best practices

### Activation and startup
- **Minimize activation time**: Keep `activate()` function lightweight
- Use **lazy activation events** - be specific with `activationEvents` in package.json
- Avoid synchronous file I/O during activation
- Defer expensive operations until they're actually needed
- Use `ExtensionContext.subscriptions` for proper cleanup

### Memory management
- **Dispose resources properly**: Always dispose of subscriptions, watchers, and providers
- Use `vscode.Disposable` pattern for all resources that need cleanup
- Avoid memory leaks by unsubscribing from events when no longer needed
- Clear caches and collections when they grow too large
- Use weak references where appropriate

### Asynchronous operations
- **Never block the main thread**: Use async/await for all I/O operations
- Use `Promise.all()` for parallel operations when possible
- Implement proper cancellation using `CancellationToken`
- Debounce frequent operations (e.g., text document changes)
- Use background workers for CPU-intensive tasks

### Tree views and data providers
- Implement efficient `getChildren()` - return only visible items
- Cache tree data when appropriate to avoid redundant queries
- Use `vscode.EventEmitter` efficiently - only fire events when data actually changes
- Implement `getTreeItem()` to be synchronous and fast
- Use `collapsibleState` wisely to control initial expansion

### Language features
- **Debounce document change events** (see `documentDebounce.ts`)
- Use incremental parsing when possible
- Cache parsed ASTs or syntax trees
- Limit diagnostic computation to visible range when feasible
- Return early from providers when results aren't needed

### File system operations
- Use `vscode.workspace.fs` API for better performance
- Batch file operations when possible
- Use the shared `FileSystemWatcherService` instead of polling
- Avoid recursive directory scans in large workspaces
- Cache file system queries with appropriate invalidation

### Commands and UI
- Keep command handlers fast and responsive
- Show progress indicators for long-running operations
- Use `withProgress()` for operations that take >1 second
- Provide cancellation support for long operations
- Avoid multiple sequential `showQuickPick` or `showInputBox` calls

### Extension size and bundle
- Minimize extension bundle size - exclude unnecessary dependencies
- Use webpack to bundle and tree-shake code
- Lazy load large dependencies only when needed
- Consider code splitting for rarely-used features
- Optimize images and assets

### Best practices from this codebase
- Use `documentDebounce()` utility for text change events (1000ms delay)
- Use `Lazy<T>` and `AsyncLazy<T>` for deferred initialization
- Implement proper `vscode.Disposable` cleanup in all providers
- Use telemetry to measure and track performance metrics
- Follow the patterns in `src/views/` for efficient tree providers

### User interface best practices
- All user-facing strings shown in the UI, error messages, etc. must use `vscode.l10n.t()`
- All user-facing strings in package.json must be extracted into package.nls.json
- Instead of `vscode.window.showQuickPick`, use `IActionContext.ui.showQuickPick`
- Instead of `vscode.window.showInputBox`, use `IActionContext.ui.showInputBox`
- The same applies for `showWarningMessage`, `showOpenDialog`, and `showWorkspaceFolderPick`
- FileSystemWatchers are a scarce resource on some systems - use the shared `FileSystemWatcherService`

## Build and package
- Development build: `npm run dev-build`
- Production build: `npm run build`
- Watch mode: `npm run watch`
- Package extension: `npm run package`
- CI build: `npm run ci-build`
- CI package: `npm run ci-package`

## Additional resources
- [Azure Developer CLI Documentation](https://learn.microsoft.com/azure/developer/azure-developer-cli/)
- [VS Code Extension API](https://code.visualstudio.com/api)
- [Contributing Guide](../CONTRIBUTING.md)
