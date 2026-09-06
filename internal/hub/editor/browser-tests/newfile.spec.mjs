import { test, expect } from "@playwright/test";

test("create a nested note, write, autosave, and reopen", async ({ page, request }) => {
  const errors = [];
  page.on("pageerror", e => errors.push(e.message));
  await page.goto("/alice/notes");
  await page.getByRole("link", { name: "New file" }).click();
  await expect(page.getByRole("textbox", { name: "File name" })).toBeFocused();
  await page.getByRole("textbox", { name: "File name" }).fill("New ideas/A thought # " + Date.now());
  await page.screenshot({ path: "test-results/new-file-desktop.png" });
  await page.getByRole("button", { name: "Create and write" }).click();
  await expect(page.locator(".editor-ready")).toBeVisible();
  const editURL = page.url();
  const note = page.getByRole("textbox", { name: "Note content" });
  await note.fill("An idea worth keeping.");
  await expect(page.locator("[data-status]")).toHaveText("Saved · version pending");
  const saved = await (await request.get(editURL, { headers: { Accept: "application/json" } })).json();
  expect(saved.draft.content).toContain("An idea worth keeping.");
  await page.reload();
  await expect(note).toContainText("An idea worth keeping.");
  expect(errors).toEqual([]);
});

test("mobile new file action and duplicate feedback", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/alice/notes");
  await page.getByRole("link", { name: "New file" }).click();
  await page.getByRole("textbox", { name: "File name" }).fill("note.md");
  await page.getByRole("button", { name: "Create and write" }).click();
  await expect(page.getByRole("alert")).toContainText("already uses this path");
  await expect(page.getByRole("textbox", { name: "File name" })).toHaveValue("note.md");
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.screenshot({ path: "test-results/new-file-mobile.png" });
});
