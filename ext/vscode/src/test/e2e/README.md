# E2E Tests for Azure Developer CLI VS Code Extension

This directory contains end-to-end (E2E) tests using Playwright that verify browser-based features of the extension.

## What We Test

### ✅ Portal URL Construction (`portal-urls.spec.ts`)
Tests that verify correct Azure Portal URLs are generated for various resource types:
- Web Apps (`Microsoft.Web/sites`)
- Storage Accounts (`Microsoft.Storage/storageAccounts`)
- Cosmos DB (`Microsoft.DocumentDB/databaseAccounts`)
- Container Apps (`Microsoft.App/containerApps`)
- Resource Groups

### ✅ Template Gallery (`template-gallery.spec.ts`)
Tests that verify template discovery and navigation:
- awesome-azd gallery (`aka.ms/awesome-azd`)
- AI templates gallery (`aka.ms/aiapps`)
- GitHub template repository links
- Template metadata structure

### ✅ Documentation Links (`documentation-links.spec.ts`)
Tests that verify all documentation and help links are accessible:
- Azure Developer CLI documentation
- VS Code extension documentation
- GitHub issues and discussions
- Installation guides
- Reference documentation
- Community links

## Running the Tests

### Run All E2E Tests
```bash
npm run test:e2e
```

### Run Tests with UI Mode (Interactive)
```bash
npm run test:e2e:ui
```

### Run Tests in Headed Mode (See Browser)
```bash
npm run test:e2e:headed
```

### Run Tests in Debug Mode
```bash
npm run test:e2e:debug
```

### Run Specific Test File
```bash
npx playwright test portal-urls.spec.ts
```

### Run Tests Matching Pattern
```bash
npx playwright test --grep "Azure Portal"
```

## Test Reports

After running tests, view the HTML report:
```bash
npx playwright show-report
```

Reports are generated in the `playwright-report/` directory.

## Prerequisites

1. **Install Dependencies**
   ```bash
   npm install
   ```

2. **Install Playwright Browsers**
   ```bash
   npx playwright install chromium
   ```

## Test Structure

Each test file follows this pattern:

```typescript
import { test, expect } from '@playwright/test';

test.describe('Feature Name', () => {
  test('specific behavior', async ({ page }) => {
    // Test implementation
    await page.goto('https://example.com');
    await expect(page).toHaveTitle(/Expected Title/);
  });
});
```

## Skipped Tests

Some tests are skipped by default (marked with `test.skip`) because they:
- Require Azure authentication
- Need actual deployed resources
- Are dependent on external services

To run skipped tests, remove the `.skip` modifier.

## CI/CD Integration

These tests are designed to run in CI/CD pipelines:
- Retries failures automatically in CI (configured in `playwright.config.ts`)
- Generates artifacts (screenshots, videos) on failure
- Produces HTML reports for debugging

## Adding New Tests

When adding new tests:

1. Create a new `.spec.ts` file in `src/test/e2e/`
2. Use descriptive test names that explain what is being verified
3. Group related tests with `test.describe()`
4. Add comments explaining the purpose of the test
5. Handle async operations properly with `await`
6. Use appropriate assertions from `@playwright/test`

### Example Test Structure
```typescript
test.describe('New Feature', () => {
  test('does something specific', async ({ page }) => {
    // Arrange: Set up test conditions
    const url = 'https://example.com';

    // Act: Perform actions
    await page.goto(url);

    // Assert: Verify results
    await expect(page).toHaveTitle(/Expected/);
  });
});
```

## Debugging Tips

### Take Screenshots
```typescript
await page.screenshot({ path: 'debug.png' });
```

### Pause Execution
```typescript
await page.pause(); // Opens Playwright Inspector
```

### Console Logs
```typescript
page.on('console', msg => console.log(msg.text()));
```

### Network Inspection
```typescript
page.on('request', request => console.log('>>', request.method(), request.url()));
page.on('response', response => console.log('<<', response.status(), response.url()));
```

## Best Practices

1. **Keep tests independent** - Each test should be able to run in isolation
2. **Use explicit waits** - Wait for elements/conditions rather than arbitrary timeouts
3. **Avoid flakiness** - Use proper locators and wait for stable states
4. **Clean up resources** - Tests should not leave side effects
5. **Test user-facing behavior** - Focus on what users experience, not implementation details

## Troubleshooting

### Browser Not Installed
```bash
npx playwright install
```

### Tests Timing Out
- Increase timeout in test or config
- Check network connectivity
- Verify URLs are accessible

### Screenshots Not Generated
- Check `playwright.config.ts` screenshot settings
- Ensure test is actually failing
- Check `test-results/` directory

## Further Reading

- [Playwright Documentation](https://playwright.dev)
- [VS Code Extension Testing](https://code.visualstudio.com/api/working-with-extensions/testing-extension)
- [Playwright Best Practices](https://playwright.dev/docs/best-practices)
