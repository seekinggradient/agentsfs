import { test, expect } from "@playwright/test";

test("create a nested note, write, autosave, and reopen", async ({ page, request }) => {
  const errors = [];
  page.on("pageerror", e => errors.push(e.message));
  await page.goto("/alice/notes");
  await page.getByRole("link", { name: "New file", exact: true }).click();
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
  await page.getByRole("link", { name: "New file", exact: true }).click();
  await page.getByRole("textbox", { name: "File name" }).fill("note.md");
  await page.getByRole("button", { name: "Create and write" }).click();
  await expect(page.getByRole("alert")).toContainText("already uses this path");
  await expect(page.getByRole("textbox", { name: "File name" })).toHaveValue("note.md");
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.screenshot({ path: "test-results/new-file-mobile.png" });
  await page.keyboard.press("Escape");
  await page.goto("/alice/notes/blob/note.md");
  await page.getByRole("button", { name: "Show file list", exact: true }).click();
  await page.getByRole("complementary", { name: "Workspace files" }).getByRole("link", { name: "New file", exact: true }).click();
  await expect(page.getByRole("dialog", { name: "New file" })).toBeVisible();
  await expect(page.getByRole("textbox", { name: "File name" })).toBeFocused();
});

test("folder context menu creates in place and keyboard dismissal restores focus", async ({ page }) => {
  await page.goto("/alice/notes");
  const row = page.locator('[data-folder-path="Projects #"]');
  await row.click({ button: "right" });
  await expect(page.getByRole("menu", { name: "Folder actions" })).toBeVisible();
  await page.screenshot({ path: "test-results/folder-context-menu.png" });
  await page.getByRole("menuitem", { name: "New file here" }).click();
  const dialog = page.getByRole("dialog", { name: "New file" });
  await expect(dialog).toContainText("Create in notes / Projects #");
  await expect(page).toHaveURL(/\/alice\/notes$/);
  await dialog.getByRole("textbox", { name: "File name" }).fill("Right click idea");
  await page.screenshot({ path: "test-results/folder-create-dialog.png" });
  await dialog.getByRole("button", { name: "Create and write" }).click();
  await expect(page).toHaveURL(/\/edit\/Projects%20%23\/Right%20click%20idea.md$/);
  await expect(page.locator(".editor-ready")).toBeVisible();
  await page.goto("/alice/notes");
  const caret = row.getByRole("button");
  await caret.focus();
  await caret.press("Shift+F10");
  await expect(page.getByRole("menu")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(caret).toBeFocused();
  await expect(page.getByRole("menu")).toHaveCount(0);
});

test("sidebar plus and nested folder plus work after in-place navigation", async ({ page }) => {
  await page.goto("/alice/notes/blob/note.md");
  const sidebar = page.getByRole("complementary", { name: "Workspace files" });
  await sidebar.getByRole("link", { name: "New file", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "New file" });
  await expect(dialog).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(sidebar.getByRole("link", { name: "New file", exact: true })).toBeFocused();
  await sidebar.getByRole("link", { name: "special # ?.md", exact: true }).click();
  await expect(page).toHaveURL(/\/blob\/special/);
  await sidebar.getByRole("button", { name: "Expand Projects # folder", exact: true }).click();
  const plus = sidebar.getByRole("link", { name: "New file in Projects #/Weekly", exact: true });
  await plus.focus();
  await plus.click();
  await expect(dialog).toContainText("notes / Projects #/Weekly");
  await dialog.getByRole("textbox", { name: "File name" }).fill("Sidebar idea");
  await dialog.getByRole("button", { name: "Create and write" }).click();
  await expect(page).toHaveURL(/\/edit\/Projects%20%23\/Weekly\/Sidebar%20idea.md$/);
  await expect(page.locator(".editor-ready")).toBeVisible();
});
