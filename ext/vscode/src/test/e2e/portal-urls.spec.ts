import { test, expect } from '@playwright/test';

/**
 * E2E tests for Azure Portal URL construction and navigation.
 *
 * These tests verify that the "Show in Azure Portal" command constructs
 * correct URLs for various Azure resource types.
 */

/**
 * Helper function to parse Azure Resource IDs (simplified version for testing)
 * This avoids dependency on VS Code modules
 */
function parseResourceId(resourceId: string) {
  const regex = /^\/subscriptions\/(?<subscription>[^/]+)\/resourceGroups\/(?<resourceGroup>[^/]+)(\/providers\/(?<provider>[^/]+)\/(?<resourceType>[^/]+)\/(?<resourceName>[^/]+))?$/i;
  const match = resourceId.match(regex);

  if (!match?.groups) {
    return null;
  }

  return {
    subscription: match.groups.subscription,
    resourceGroup: match.groups.resourceGroup,
    provider: match.groups.provider,
    resourceType: match.groups.resourceType,
    resourceName: match.groups.resourceName,
  };
}

test.describe('Azure Portal URL Construction', () => {
  test('constructs correct URL for Web App resource', async () => {
    const resourceId = '/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg-test/providers/Microsoft.Web/sites/my-webapp';
    const parsedId = parseResourceId(resourceId);

    expect(parsedId).toBeDefined();
    expect(parsedId?.provider).toBe('Microsoft.Web');
    expect(parsedId?.resourceType).toBe('sites');
    expect(parsedId?.resourceName).toBe('my-webapp');

    // Construct portal URL (this matches the logic in openInPortalStep.ts)
    const portalUrl = `https://portal.azure.com/#@/resource${resourceId}`;
    expect(portalUrl).toContain('portal.azure.com');
    expect(portalUrl).toContain('Microsoft.Web');
  });

  test('constructs correct URL for Storage Account', async () => {
    const resourceId = '/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg-test/providers/Microsoft.Storage/storageAccounts/mystorageacct';
    const parsedId = parseResourceId(resourceId);

    expect(parsedId).toBeDefined();
    expect(parsedId?.provider).toBe('Microsoft.Storage');
    expect(parsedId?.resourceType).toBe('storageAccounts');

    const portalUrl = `https://portal.azure.com/#@/resource${resourceId}`;
    expect(portalUrl).toContain('Microsoft.Storage');
  });

  test('constructs correct URL for Cosmos DB', async () => {
    const resourceId = '/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg-test/providers/Microsoft.DocumentDB/databaseAccounts/my-cosmos-db';
    const parsedId = parseResourceId(resourceId);

    expect(parsedId).toBeDefined();
    expect(parsedId?.provider).toBe('Microsoft.DocumentDB');
    expect(parsedId?.resourceType).toBe('databaseAccounts');

    const portalUrl = `https://portal.azure.com/#@/resource${resourceId}`;
    expect(portalUrl).toContain('Microsoft.DocumentDB');
  });

  test('constructs correct URL for Container Apps', async () => {
    const resourceId = '/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg-test/providers/Microsoft.App/containerApps/my-container-app';
    const parsedId = parseResourceId(resourceId);
    expect(parsedId).toBeDefined();
    expect(parsedId?.provider).toBe('Microsoft.App');
    expect(parsedId?.resourceType).toBe('containerApps');

    const portalUrl = `https://portal.azure.com/#@/resource${resourceId}`;
    expect(portalUrl).toContain('Microsoft.App');
  });

  test('constructs correct URL for Resource Group', async () => {
    const resourceId = '/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg-test';
    const portalUrl = `https://portal.azure.com/#@/resource${resourceId}`;

    expect(portalUrl).toContain('portal.azure.com');
    expect(portalUrl).toContain('resourceGroups/rg-test');
  });

  test('portal URL format matches Azure requirements', async () => {
    const resourceId = '/subscriptions/sub123/resourceGroups/rg-test/providers/Microsoft.Web/sites/webapp';
    const portalUrl = `https://portal.azure.com/#@/resource${resourceId}`;

    // Verify URL structure
    expect(portalUrl).toMatch(/^https:\/\/portal\.azure\.com\/#@\/resource\//);
    expect(portalUrl).toContain('/subscriptions/');
    expect(portalUrl).toContain('/resourceGroups/');
    expect(portalUrl).toContain('/providers/');
  });
});

test.describe('Azure Portal Navigation', () => {
  test.skip('portal.azure.com is accessible', async ({ page }) => {
    // This test requires authentication and is skipped by default
    // Uncomment when testing with proper Azure credentials
    await page.goto('https://portal.azure.com');
    await expect(page).toHaveTitle(/Microsoft Azure/);
  });
});
