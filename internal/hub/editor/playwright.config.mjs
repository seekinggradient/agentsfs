import { defineConfig } from "@playwright/test";
export default defineConfig({
  testDir: "./browser-tests",
  fullyParallel: false,
  workers: 1,
  use: {
    baseURL: "http://127.0.0.1:3347",
    channel: "chrome",
    headless: true,
    viewport: { width: 1440, height: 1050 },
    trace: "retain-on-failure",
  },
  webServer: {
    command:
      "AFS_EDITOR_BROWSER_FIXTURE=1 GOCACHE=/private/tmp/afs-editor-go-cache go test .. -run ^TestEditorBrowserFixture$ -count=1 -timeout=30m -v",
    url: "http://127.0.0.1:3347/alice/notes/edit/note.md",
    reuseExistingServer: !process.env.CI,
    timeout: 120000,
  },
});
