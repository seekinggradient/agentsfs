import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.goto("/alice/notes/edit/note.md");
  await expect(page.locator(".editor-ready")).toBeVisible();
});

for (const [undo, redo] of [
  ["Meta+z", "Meta+Shift+z"],
  ["Control+z", "Control+Shift+z"],
  ["Control+z", "Control+y"],
]) {
  test(`Write keyboard history: ${undo} and ${redo}`, async ({ page }) => {
    const note = page.getByRole("textbox", { name: "Note content" });
    await expect(
      page.getByRole("button", { name: "Undo", exact: true }),
    ).toBeDisabled();
    const before = await note.innerText();
    await note.press("ControlOrMeta+End");
    await page.keyboard.type(" Keyboard history probe.");
    await expect(note).toContainText("Keyboard history probe.");
    await note.press(undo);
    await expect(note).not.toContainText("Keyboard history probe.");
    expect(await note.innerText()).toBe(before);
    await note.press(redo);
    await expect(note).toContainText("Keyboard history probe.");
  });
}
test("native Edit-menu undo and redo use editor history", async ({ page }) => {
  const note = page.getByRole("textbox", { name: "Note content" });
  await note.press("ControlOrMeta+End");
  await page.keyboard.type(" Native history probe.");
  for (const type of ["historyUndo", "historyRedo"]) {
    const handled = await note.evaluate(
      (element, type) =>
        !element.dispatchEvent(
          new InputEvent("beforeinput", {
            inputType: type,
            bubbles: true,
            cancelable: true,
          }),
        ),
      type,
    );
    expect(handled).toBe(true);
    if (type === "historyUndo")
      await expect(note).not.toContainText("Native history probe.");
    else await expect(note).toContainText("Native history probe.");
  }
});
test("switching modes preserves Write undo history and the original document", async ({
  page,
}) => {
  const note = page.getByRole("textbox", { name: "Note content" });
  const before = await note.innerText();
  await note.press("ControlOrMeta+End");
  await page.keyboard.type(" Keep my history.");
  await page.getByRole("button", { name: "Markdown", exact: true }).click();
  await page.getByRole("button", { name: "Write", exact: true }).click();
  await note.press("Meta+z");
  expect(await note.innerText()).toBe(before);
  await expect(
    page.getByRole("button", { name: "Undo", exact: true }),
  ).toBeDisabled();
  await expect(page.locator("[data-save]")).toBeDisabled();
});
test("Markdown keyboard history still works", async ({ page }) => {
  await page.getByRole("button", { name: "Markdown", exact: true }).click();
  const source = page.getByRole("textbox", { name: "Markdown source" });
  await source.press("ControlOrMeta+End");
  await page.keyboard.type(" Source history probe.");
  await source.press("Meta+z");
  await expect(source).not.toContainText("Source history probe.");
  await source.press("Meta+Shift+z");
  await expect(source).toContainText("Source history probe.");
});
