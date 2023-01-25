import { test, expect } from "@playwright/test";
import { v4 as uuidv4 } from "uuid";

test("Create and delete item test", async ({ page }) => {
  await page.goto("/", { waitUntil: 'networkidle' });

  await expect(page.locator("text=My List").first()).toBeVisible({
    timeout: 240 * 1000,
  });

  await expect(page.locator("text=This list is empty.").first()).toBeVisible()

  const guid = uuidv4();
  console.log(`Creating item with text: ${guid}`);

  await page.locator('[placeholder="Add an item"]').focus();
  await page.locator('[placeholder="Add an item"]').type(guid);
  await page.locator('[placeholder="Add an item"]').press("Enter");

  console.log(`Deleting item with text: ${guid}`);
  await expect(page.locator(`text=${guid}`).first()).toBeVisible()

  await page.locator(`text=${guid}`).click();

  /* when delete option is hide behind "..." button */
  const itemMoreDeleteButton = await page.$('button[role="menuitem"]:has-text("")');
  if(itemMoreDeleteButton){
    await itemMoreDeleteButton.click();
  };
  await page.locator('button[role="menuitem"]:has-text("Delete")').click();

  await expect(page.locator(`text=${guid}`).first()).toBeHidden()
});
