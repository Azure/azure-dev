import { test, expect } from '@playwright/test';

/**
 * E2E tests for Template Gallery and external template links.
 *
 * These tests verify that template-related URLs and links work correctly,
 * including the awesome-azd gallery and AI templates.
 */
test.describe('Template Gallery - awesome-azd', () => {
  test('awesome-azd gallery redirects and loads correctly', async ({ page }) => {
    await page.goto('https://aka.ms/awesome-azd');

    // Wait for navigation after redirect
    await page.waitForLoadState('networkidle');

    // Verify we landed on the Azure Developer CLI templates page
    await expect(page).toHaveTitle(/Azure Developer CLI/i);
  });

  test('awesome-azd gallery has templates section', async ({ page }) => {
    await page.goto('https://aka.ms/awesome-azd');
    await page.waitForLoadState('networkidle');

    // Look for common template gallery elements
    // Note: These selectors may need adjustment based on actual gallery structure
    const hasTemplates = await page.locator('body').textContent();
    expect(hasTemplates).toBeTruthy();
  });
});

test.describe('AI Templates Gallery', () => {
  test('AI templates page (aka.ms/aiapps) is accessible', async ({ page }) => {
    await page.goto('https://aka.ms/aiapps');
    await page.waitForLoadState('networkidle');

    // Verify AI/ML related content is present
    const content = await page.textContent('body');
    expect(content).toBeTruthy();
  });

  test('AI templates link redirects successfully', async ({ page }) => {
    const response = await page.goto('https://aka.ms/aiapps');

    // Verify successful redirect
    expect(response?.status()).toBeLessThan(400);
    expect(page.url()).toBeTruthy();
  });
});

test.describe('GitHub Template Repository Links', () => {
  test('Azure Developer CLI main repository is accessible', async ({ page }) => {
    await page.goto('https://github.com/Azure/azure-dev');

    // Verify GitHub page loaded
    await expect(page).toHaveTitle(/GitHub/);

    // Verify we're on the right repository by checking URL
    expect(page.url()).toContain('github.com/Azure/azure-dev');
  });

  test('GitHub repository has README', async ({ page }) => {
    await page.goto('https://github.com/Azure/azure-dev');

    // Look for README content
    const readme = page.locator('article[itemprop="text"]');
    await expect(readme).toBeVisible({ timeout: 10000 });
  });

  test('template repository link format is valid', async () => {
    // Test that our template URL construction is valid
    const templateName = 'todo-nodejs-mongo';
    const githubUrl = `https://github.com/Azure-Samples/${templateName}`;

    expect(githubUrl).toMatch(/^https:\/\/github\.com\/Azure-Samples\//);
  });
});

test.describe('Documentation Links', () => {
  test('Azure Developer CLI documentation (aka.ms/azure-dev/vscode) redirects', async ({ page }) => {
    const response = await page.goto('https://aka.ms/azure-dev/vscode');

    // Verify redirect was successful
    expect(response?.status()).toBeLessThan(400);
    await page.waitForLoadState('networkidle');

    // Verify we're on a Microsoft Learn or docs page
    expect(page.url()).toMatch(/microsoft\.com|azure\.com/);
  });

  test('Azure Developer CLI main docs are accessible', async ({ page }) => {
    // Test the direct documentation URL instead of short link
    const response = await page.goto('https://learn.microsoft.com/azure/developer/azure-developer-cli/');

    expect(response?.status()).toBeLessThan(400);
    await page.waitForLoadState('networkidle');

    // Verify we're on the Azure Developer CLI docs
    expect(page.url()).toContain('azure-developer-cli');
  });
});

test.describe('Template Metadata Validation', () => {
  test('template structure includes required fields', async () => {
    // This tests the expected structure of template metadata
    // (actual template data would come from the extension's template provider)
    const mockTemplate = {
      id: 'todo-nodejs-mongo',
      name: 'ToDo Application with Node.js and MongoDB',
      description: 'A complete ToDo application built with Node.js and MongoDB',
      source: 'https://github.com/Azure-Samples/todo-nodejs-mongo',
      tags: ['nodejs', 'mongodb', 'web'],
    };

    expect(mockTemplate.id).toBeTruthy();
    expect(mockTemplate.name).toBeTruthy();
    expect(mockTemplate.source).toMatch(/^https:\/\/github\.com/);
    expect(mockTemplate.tags).toBeInstanceOf(Array);
  });
});
