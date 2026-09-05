import { test, expect } from "@playwright/test";
const path = "/alice/notes/edit/note.md";
async function load(page) {
  await page.goto(path);
  await expect(page.locator(".editor-ready")).toBeVisible();
}
async function snapshot(request) {
  return (
    await request.get(path, { headers: { Accept: "application/json" } })
  ).json();
}
async function commit(request, page, content, head) {
  const csrf = await page.locator('[name="csrf"]').inputValue();
  return request.post(path, {
    headers: { Accept: "application/json" },
    form: { head, content, csrf, message: "Browser test update" },
  });
}
let original;
test.beforeEach(async ({ page, request }) => {
  await load(page);
  const current = await snapshot(request);
  original ??= current.content;
  if (current.content !== original) {
    await commit(request, page, original, current.head);
    await load(page);
  }
});
test("visual writing, formatting, review, commit, source round trip and history", async ({
  page,
  request,
}) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await expect(
    page.getByRole("textbox", { name: "Note content" }),
  ).toContainText("A little room to think");
  await expect(
    page.getByRole("button", { name: "Save version", exact: true }),
  ).toBeDisabled();
  await page.getByRole("button", { name: "Markdown", exact: true }).click();
  await expect(
    page.getByRole("textbox", { name: "Markdown source" }),
  ).toContainText("description:");
  await page.getByRole("button", { name: "Write", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "Save version", exact: true }),
  ).toBeDisabled();
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.getByRole("textbox", { name: "Note content" }).blur();
  await page.screenshot({
    path: "test-results/editor-desktop.png",
    fullPage: true,
    animations: "disabled",
  });
  const note = page.getByRole("textbox", { name: "Note content" });
  await note.click();
  await page.keyboard.press("ControlOrMeta+End");
  await page.keyboard.press("ArrowRight");
  await page.keyboard.press("Enter");
  await page.keyboard.type("A thought worth keeping.");
  await expect(page.locator("[data-status]")).toContainText("Draft saved");
  await page.getByRole("button", { name: "Save version", exact: true }).click();
  await expect(
    page.getByRole("dialog", { name: "Save your changes" }),
  ).toBeVisible();
  await expect(page.locator("[data-diff]")).toContainText(
    "A thought worth keeping.",
  );
  await page.locator("#version-message").fill("Capture a useful thought");
  await page.screenshot({
    path: "test-results/editor-review.png",
    animations: "disabled",
  });
  await page.locator('[data-action="commit"]').click();
  await expect(page.locator("[data-status]")).toHaveText("Version saved");
  const saved = await snapshot(request);
  expect(saved.content).toContain("A thought worth keeping.");
  expect(saved.content.startsWith("---\ndescription:")).toBeTruthy();
  expect(saved.content).toContain("* Gather observations");
  expect(errors).toEqual([]);
});
test("slash commands and insertion controls create real markdown", async ({
  page,
  request,
}) => {
  const note = page.getByRole("textbox", { name: "Note content" });
  await note.click();
  await page.keyboard.press("ControlOrMeta+End");
  await page.keyboard.press("ArrowRight");
  await page.keyboard.press("Enter");
  await page.keyboard.type("/heading 2");
  await expect(
    page.getByRole("listbox", { name: "Insert a block" }),
  ).toBeVisible();
  await page.keyboard.press("Enter");
  await page.keyboard.type("A new section");
  await expect(note.locator("h2")).toContainText([
    "What we’re exploring",
    "Next steps",
    "A new section",
  ]);
  await page.keyboard.press("Enter");
  await page.getByRole("button", { name: "Checklist", exact: true }).click();
  await page.keyboard.type("Follow the thread");
  await page.getByRole("button", { name: "Markdown", exact: true }).click();
  await expect(
    page.getByRole("textbox", { name: "Markdown source" }),
  ).toContainText("## A new section");
  await expect(
    page.getByRole("textbox", { name: "Markdown source" }),
  ).toContainText("- [ ] Follow the thread");
});
test("draft recovery survives a reload without creating history", async ({
  page,
  request,
}) => {
  const start = await snapshot(request);
  await page.getByRole("button", { name: "Markdown", exact: true }).click();
  const source = page.getByRole("textbox", { name: "Markdown source" });
  await source.click();
  await page.keyboard.press("ControlOrMeta+End");
  await page.keyboard.type("\nRecovered writing.");
  await expect(page.locator("[data-status]")).toContainText("Draft saved");
  page.on("dialog", (d) => d.accept());
  await page.reload();
  await expect(
    page.getByRole("button", { name: "Restore draft" }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Restore draft" }).click();
  await expect(
    page.getByRole("textbox", { name: "Note content" }),
  ).toContainText("Recovered writing.");
  expect((await snapshot(request)).head).toEqual(start.head);
});
test("overlapping edit opens reconciliation and never overwrites latest", async ({
  page,
  request,
}) => {
  const start = await snapshot(request);
  await page.getByRole("button", { name: "Markdown", exact: true }).click();
  const source = page.getByRole("textbox", { name: "Markdown source" });
  await source.click();
  await page.keyboard.press("ControlOrMeta+End");
  await page.keyboard.type("\nMy draft.");
  const res = await commit(
    request,
    page,
    start.content + "\nA collaborator’s contribution.\n",
    start.head,
  );
  expect(res.ok()).toBeTruthy();
  await page.locator("[data-save]").click();
  await page.locator('[data-action="commit"]').click();
  await expect(
    page.getByRole("dialog", { name: "This note has a newer version" }),
  ).toBeVisible();
  await expect(page.locator("[data-latest]")).toContainText("collaborator");
  await expect(page.locator("[data-conflict-draft]")).toContainText(
    "My draft.",
  );
  expect((await snapshot(request)).content).not.toContain("My draft.");
  await expect(
    page.getByRole("textbox", { name: "Combined version", exact: true }),
  ).toBeVisible();
  await page.screenshot({
    path: "test-results/editor-conflict.png",
    animations: "disabled",
  });
  await page.locator("[data-action=conflict-mode]").click();
  await page
    .locator("#resolved-source")
    .fill(start.content + "\nA collaborator’s contribution.\n\nMy draft.\n");
  await page.locator("[data-conflict-confirm]").check();
  await page.locator('[data-action="resolve"]').click();
  await page.locator("[data-save]").click();
  await page.locator('[data-action="commit"]').click();
  await expect(page.locator("[data-status]")).toHaveText("Version saved");
  const saved = await snapshot(request);
  expect(saved.content).toContain("collaborator");
  expect(saved.content).toContain("My draft.");
});
test("unsupported syntax stays in source mode and mobile has no overflow", async ({
  page,
}) => {
  await page.goto("/alice/notes/edit/advanced.md");
  await expect(page.locator(".editor-ready")).toBeVisible();
  await expect(
    page.getByRole("textbox", { name: "Markdown source" }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Write", exact: true }).click();
  await expect(
    page.getByRole("textbox", { name: "Markdown source" }),
  ).toBeVisible();
  await expect(page.locator("[data-notice]")).toContainText("intact");
  await page.goto(path);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({
    path: "test-results/editor-mobile.png",
    fullPage: true,
    animations: "disabled",
  });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBeTruthy();
  await page.evaluate(() => (document.documentElement.dataset.theme = "dark"));
  await page.screenshot({
    path: "test-results/editor-dark-mobile.png",
    fullPage: true,
    animations: "disabled",
  });
});
test("failed save keeps the draft and allows retry", async ({ page }) => {
  await page.getByRole("button", { name: "Markdown", exact: true }).click();
  await page.getByRole("textbox", { name: "Markdown source" }).click();
  await page.keyboard.press("ControlOrMeta+End");
  await page.keyboard.type("\nA retryable draft.");
  await page.route("**/alice/notes/edit/note.md", (route) =>
    route.request().method() === "POST"
      ? route.fulfill({
          status: 503,
          contentType: "application/json",
          body: JSON.stringify({ error: "Temporarily unavailable" }),
        })
      : route.continue(),
  );
  await page.locator("[data-save]").click();
  await page.locator('[data-action="commit"]').click();
  await expect(page.locator("[data-save-error]")).toHaveText(
    "Temporarily unavailable",
  );
  await expect(page.locator('[data-action="commit"]')).toBeEnabled();
  await page.unroute("**/alice/notes/edit/note.md");
  await page.locator('[data-action="commit"]').click();
  await expect(page.locator("[data-status]")).toHaveText("Version saved");
});
test("toolbar links, formatting, tables and undo work without markdown knowledge", async ({
  page,
}) => {
  const note = page.getByRole("textbox", { name: "Note content" });
  await note.click();
  await page.keyboard.press("ControlOrMeta+End");
  await page.keyboard.press("ArrowRight");
  await page.keyboard.press("Enter");
  await page.getByRole("button", { name: "Bold", exact: true }).click();
  await page.keyboard.type("Important");
  await page.getByRole("button", { name: "Bold", exact: true }).click();
  await expect(note.locator("strong")).toContainText([
    "clear thinking",
    "Important",
  ]);
  await page.keyboard.press("Enter");
  await page.getByRole("button", { name: "Add link", exact: true }).click();
  await page.locator("#link-url").fill("https://example.com/research");
  await page.locator("#link-text").fill("Read the research");
  await page.getByRole("button", { name: "Apply", exact: true }).click();
  await expect(
    note.locator('a[href="https://example.com/research"]'),
  ).toHaveText("Read the research");
  await page.keyboard.press("Enter");
  await page.locator(".write-insert summary").click();
  await page.getByRole("button", { name: "Table", exact: true }).click();
  await expect(note.locator("table")).toBeVisible();
  await page.getByRole("button", { name: "Add row", exact: true }).click();
  await expect(note.locator("tr")).toHaveCount(4);
  await page.getByRole("button", { name: "Undo", exact: true }).click();
  await expect(note.locator("tr")).toHaveCount(3);
});
test("CRLF source survives opening and mode switches, and script content stays inert", async ({
  page,
  request,
}) => {
  const start = await snapshot(request),
    crlf =
      '---\r\ndescription: "Keep me"\r\n---\r\n# Windows note\r\n\r\nA paragraph.\r\n';
  await commit(request, page, crlf, start.head);
  await load(page);
  await page.getByRole("button", { name: "Markdown", exact: true }).click();
  await page.getByRole("button", { name: "Write", exact: true }).click();
  await expect(page.locator("[data-save]")).toBeDisabled();
  const downloadPromise = page.waitForEvent("download");
  await page.getByRole("button", { name: "Download draft" }).click();
  const download = await downloadPromise;
  const chunks = [];
  for await (const chunk of await download.createReadStream())
    chunks.push(chunk);
  expect(Buffer.concat(chunks).toString()).toBe(crlf);
  await page.getByRole("button", { name: "Markdown", exact: true }).click();
  await page.getByRole("textbox", { name: "Markdown source" }).click();
  await page.keyboard.press("ControlOrMeta+End");
  await page.keyboard.type("An addition.");
  await page.locator("[data-save]").click();
  await page.locator('[data-action="commit"]').click();
  await expect(page.locator("[data-status]")).toHaveText("Version saved");
  expect((await snapshot(request)).content).toBe(crlf + "An addition.");
  const current = await snapshot(request);
  await commit(
    request,
    page,
    "# Untrusted\n\n</script><script>window.editorInjected=true</script>\n",
    current.head,
  );
  await load(page);
  expect(await page.evaluate(() => window.editorInjected)).toBeUndefined();
  await expect(
    page.getByRole("textbox", { name: "Markdown source" }),
  ).toContainText("</script>");
});
test("plain form remains usable when JavaScript is disabled", async ({
  browser,
  request,
  page,
}) => {
  const context = await browser.newContext({ javaScriptEnabled: false });
  const plain = await context.newPage();
  await plain.goto("http://127.0.0.1:3347" + path);
  await plain
    .locator('[name="content"]')
    .fill(original + "\nSaved without JavaScript.\n");
  await plain.locator('[name="message"]').fill("Use the fallback editor");
  await plain
    .getByRole("button", { name: "Save version", exact: true })
    .click();
  await expect(plain).toHaveURL(/\/blob\/note.md$/);
  expect((await snapshot(request)).content).toContain(
    "Saved without JavaScript.",
  );
  await context.close();
});
