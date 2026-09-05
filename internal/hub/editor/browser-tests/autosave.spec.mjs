import { test, expect } from "@playwright/test";
const path = "/alice/notes/edit/special%20%23%20%3F.md";
const seed = "# Autosave checks\n\nStart here.\n";
async function state(request) {
  return (
    await request.get(path, { headers: { Accept: "application/json" } })
  ).json();
}
async function load(page) {
  await page.goto(path);
  await expect(page.locator(".editor-ready")).toBeVisible();
}
async function append(page, text) {
  const note = page.getByRole("textbox", { name: "Note content" });
  await note.press("ControlOrMeta+End");
  await page.keyboard.type(text);
}
test.beforeEach(async ({ page, request }) => {
  await load(page);
  const current = await state(request);
  const csrf = await page.locator('[name="csrf"]').inputValue();
  const res = await request.post(path, {
    headers: { Accept: "application/json" },
    form: {
      action: "checkpoint",
      revision: String(current.draft?.revision || 0),
      reconcile: "true",
      head: current.head,
      content: seed,
      csrf,
    },
  });
  expect(res.ok()).toBeTruthy();
  await load(page);
});
test("server drafts restore in a fresh browser and checkpoint after all tabs close", async ({
  page,
  request,
  browser,
}) => {
  test.setTimeout(60000);
  const initial = await state(request);
  await append(page, " First thought.");
  await expect(page.locator("[data-status]")).toHaveText(
    "Saved · version pending",
  );
  await append(page, " Second thought.");
  await expect(page.locator("[data-status]")).toHaveText(
    "Saved · version pending",
  );
  const draft = await state(request);
  expect(draft.head).toBe(initial.head);
  expect(draft.draft.content).toContain("First thought. Second thought.");
  await page.close();
  const fresh = await browser.newContext();
  const reopened = await fresh.newPage();
  await reopened.goto("http://127.0.0.1:3348" + path);
  await expect(
    reopened.getByRole("textbox", { name: "Note content" }),
  ).toContainText("First thought. Second thought.");
  await expect(reopened.locator("[data-status]")).toHaveText(
    "Saved · version pending",
  );
  await fresh.close();
  await expect
    .poll(async () => (await state(request)).content, {
      timeout: 45000,
      intervals: [1000],
    })
    .toContain("First thought. Second thought.");
  expect((await state(request)).draft.pending).toBe(false);
});
test("two tabs preserve both drafts and require explicit reconciliation", async ({
  page,
  context,
  request,
}) => {
  const second = await context.newPage();
  await load(second);
  await append(page, " From tab one.");
  await expect(page.locator("[data-status]")).toHaveText(
    "Saved · version pending",
  );
  await append(second, " From tab two.");
  await expect(second.locator("[data-notice]")).toContainText("Another tab");
  expect((await state(request)).draft.content).toContain("From tab one.");
  expect((await state(request)).draft.content).not.toContain("From tab two.");
  await second
    .getByRole("button", { name: "Review changes", exact: true })
    .click();
  await expect(second.locator("[data-latest]")).toContainText("From tab one.");
  await second.locator('[data-action="conflict-mode"]').click();
  await second
    .locator("#resolved-source")
    .fill(seed + "From tab one. From tab two.\n");
  await second.locator("[data-conflict-confirm]").check();
  await second.locator('[data-action="resolve"]').click();
  await expect(second.locator("[data-status]")).toHaveText(
    "Saved · version pending",
  );
  expect((await state(request)).draft.content).toContain(
    "From tab one. From tab two.",
  );
});
test("offline edits recover locally and reconnect saves automatically", async ({
  page,
  context,
  request,
}) => {
  await context.setOffline(true);
  await append(page, " Written offline.");
  await expect(page.locator("[data-status]")).toContainText("Offline");
  await expect
    .poll(() =>
      page.evaluate(() =>
        Object.keys(localStorage).some(
          (k) =>
            k.startsWith("afs-note-draft:") &&
            localStorage[k].includes("Written offline."),
        ),
      ),
    )
    .toBe(true);
  await context.setOffline(false);
  await expect(page.locator("[data-status]")).toHaveText(
    "Saved · version pending",
  );
  expect((await state(request)).draft.content).toContain("Written offline.");
  await page.reload();
  await expect(
    page.getByRole("textbox", { name: "Note content" }),
  ).toContainText("Written offline.");
});
test("edits during a slow save are subsequently acknowledged without losing text", async ({
  page,
  request,
}) => {
  let release;
  let intercepted;
  const started = new Promise((resolve) => (intercepted = resolve));
  const gate = new Promise((resolve) => (release = resolve));
  let first = true;
  await page.route("**/alice/notes/edit/special*", async (route) => {
    if (route.request().method() === "POST" && first) {
      first = false;
      intercepted();
      await gate;
    }
    await route.continue();
  });
  await append(page, " First part.");
  await started;
  await append(page, " Still typing.");
  release();
  await expect(page.locator("[data-status]")).toHaveText(
    "Saved · version pending",
  );
  expect((await state(request)).draft.content).toContain(
    "First part. Still typing.",
  );
});
