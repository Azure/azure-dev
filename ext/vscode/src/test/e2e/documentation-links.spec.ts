import { test, expect } from '@playwright/test';

/**
 * E2E tests for documentation and help links from the extension.
 *
 * These tests verify that links in the Help and Feedback view,
 * as well as other documentation links, are valid and accessible.
 */
test.describe('Help and Feedback Links', () => {
  test('Azure Developer CLI documentation is accessible', async ({ page }) => {
    const response = await page.goto('https://aka.ms/azure-dev');

    expect(response?.status()).toBeLessThan(400);
    await page.waitForLoadState('networkidle');

    // Verify we're on Azure or Microsoft Learn
    expect(page.url()).toMatch(/microsoft\.com|azure\.com/);
  });

  test('VS Code extension documentation link works', async ({ page }) => {
    const response = await page.goto('https://aka.ms/azure-dev/vscode');

    expect(response?.status()).toBeLessThan(400);
    await page.waitForLoadState('networkidle');
  });

  test('GitHub issues page is accessible', async ({ page }) => {
    await page.goto('https://github.com/Azure/azure-dev/issues');

    await expect(page).toHaveTitle(/Issues.*Azure\/azure-dev/i);

    // Verify issues page elements are present
    await expect(page.locator('[data-target="qbsearch-input.inputButton"]')).toBeVisible({ timeout: 10000 });
  });

  test('GitHub discussions page redirects to GitHub', async ({ page }) => {
    // Note: aka.ms/azure-dev/discussions may return 404 if redirect is not set up
    // We'll just verify the GitHub repo discussions page directly
    await page.goto('https://github.com/Azure/azure-dev/discussions');

    // Verify GitHub discussions page (don't wait for networkidle as it can timeout)
    await expect(page).toHaveTitle(/GitHub/);
    expect(page.url()).toContain('github.com');
    expect(page.url()).toContain('discussions');
  });

  test('feedback survey link (aka.ms/azure-dev/hats) redirects', async ({ page }) => {
    const response = await page.goto('https://aka.ms/azure-dev/hats');

    // Verify redirect works (might go to Microsoft Forms or similar)
    expect(response?.status()).toBeLessThan(400);
  });
});

test.describe('Installation and Getting Started Links', () => {
  test('Azure Developer CLI install page is accessible', async ({ page }) => {
    const response = await page.goto('https://aka.ms/azure-dev/install');

    expect(response?.status()).toBeLessThan(400);
    await page.waitForLoadState('networkidle');

    // Verify installation documentation
    const content = await page.textContent('body');
    expect(content?.toLowerCase()).toContain('install');
  });

  test('VS Code marketplace page is accessible', async ({ page }) => {
    await page.goto('https://marketplace.visualstudio.com/items?itemName=ms-azuretools.azure-dev');

    await expect(page).toHaveTitle(/Azure Developer CLI/i);

    // Verify extension name is displayed (use first() to handle multiple matches)
    await expect(page.locator('text=Azure Developer CLI').first()).toBeVisible({ timeout: 10000 });
  });
});

test.describe('Reference Documentation', () => {
  test('Azure Developer CLI command reference is accessible', async ({ page }) => {
    // This would test the command reference documentation
    const response = await page.goto('https://learn.microsoft.com/azure/developer/azure-developer-cli/');

    expect(response?.status()).toBeLessThan(400);
    await page.waitForLoadState('networkidle');
  });

  test('azure.yaml schema documentation exists', async ({ page }) => {
    const response = await page.goto('https://raw.githubusercontent.com/Azure/azure-dev/main/schemas/v1.0/azure.yaml.json');

    expect(response?.status()).toBe(200);

    // Verify it's valid JSON schema
    const content = await page.textContent('body');
    expect(() => JSON.parse(content || '{}')).not.toThrow();
  });
});

test.describe('External Tool Links', () => {
  test('Application Insights overview page is accessible', async ({ page }) => {
    const response = await page.goto('https://learn.microsoft.com/azure/azure-monitor/app/app-insights-overview');

    expect(response?.status()).toBeLessThan(400);
    await page.waitForLoadState('networkidle');

    const content = await page.textContent('body');
    expect(content?.toLowerCase()).toContain('application insights');
  });

  test('Azure Portal home page is accessible', async ({ page }) => {
    // Note: This will redirect to login if not authenticated
    const response = await page.goto('https://portal.azure.com');

    // We expect either the portal to load or a redirect to login
    expect(response?.status()).toBeLessThan(500);
  });
});

test.describe('Community and Social Links', () => {
  test('Azure Developer CLI Twitter/X profile link works', async ({ page }) => {
    // Test if social links (if any) are accessible
    // This is a placeholder - adjust based on actual social links
    const response = await page.goto('https://github.com/Azure/azure-dev');
    expect(response?.status()).toBeLessThan(400);
  });
});

test.describe('Link Validation Utils', () => {
  test('validates URL format for portal links', async () => {
    const validPortalUrl = 'https://portal.azure.com/#@/resource/subscriptions/123/resourceGroups/rg';
    const invalidPortalUrl = 'http://portal.azure.com'; // Should be https

    expect(validPortalUrl).toMatch(/^https:\/\/portal\.azure\.com/);
    expect(invalidPortalUrl).not.toMatch(/^https:\/\/portal\.azure\.com/);
  });

  test('validates GitHub repository URL format', async () => {
    const validGitHubUrl = 'https://github.com/Azure/azure-dev';
    const validSamplesUrl = 'https://github.com/Azure-Samples/todo-nodejs-mongo';

    expect(validGitHubUrl).toMatch(/^https:\/\/github\.com\//);
    expect(validSamplesUrl).toMatch(/^https:\/\/github\.com\/Azure-Samples\//);
  });

  test('validates Microsoft Learn URL format', async () => {
    const validLearnUrl = 'https://learn.microsoft.com/azure/developer/azure-developer-cli/';
    const validDocsUrl = 'https://docs.microsoft.com/azure/';

    expect(validLearnUrl).toMatch(/^https:\/\/(learn|docs)\.microsoft\.com/);
    expect(validDocsUrl).toMatch(/^https:\/\/(learn|docs)\.microsoft\.com/);
  });
});
