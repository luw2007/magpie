// Run with Node's test runner and Playwright on the module path; see README.md.
// Saving the current setup as a profile in the tray panel's Profiles tab:
// "＋ Save current", with the list scrolled to its end, opens a name field
// beside it without moving the list (the field at the list's head drew it
// up under the tabs, the field's top cut off) and with one ring, not the
// page's focus ring over its own; field and button stay in sight, and the
// button then reads Save; a click on it saves as Enter does (it did nothing:
// the field lost focus to it and went). Escape closes the field and gives the
// button back. No backend: the API is faked here.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");

const assets = path.resolve(__dirname, "../assets");
const models = ["model-a", "model-b"].map((m) => ({ value: m, label: m }));
const agent = (id) => ({
  id, name: id, path: "/test/" + id, fields: [
    { key: "model", label: "model", value: "model-a", options: models },
  ],
});
const profile = (name) => ({ name, summary: "" });

function server(lang, saved) {
  let profiles = Array.from({ length: 30 }, (_, i) => profile("profile-" + i));
  return async (route) => {
    const req = route.request(), url = new URL(req.url());
    const state = () => ({ agents: [agent("codex"), agent("claude")], profiles, settings: { lang, theme: "light" } });
    if (url.pathname === "/boot.js") return route.fulfill({ contentType: "text/javascript", body: `window.bootPrefs = {lang:"${lang}",theme:"light",web:true};` });
    if (url.pathname === "/wails/runtime.js") return route.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
    if (url.pathname === "/api/state") return route.fulfill({ json: state() });
    if (url.pathname === "/api/profile/save") {
      const { name } = req.postDataJSON();
      saved.push(name);
      profiles = [...profiles, profile(name)];
      return route.fulfill({ json: state() });
    }
    if (url.pathname === "/api/usage/quotas") return route.fulfill({ json: [] });
    if (url.pathname === "/api/groups") return route.fulfill({ json: { groups: [] } });
    if (url.pathname === "/api/providers") return route.fulfill({ json: { providers: [], gateway: { running: true } } });
    if (url.pathname === "/api/window/fit") return route.fulfill({ status: 204 });
    if (url.pathname.startsWith("/api/")) return route.fulfill({ json: {} });
    const file = path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
    const contentType = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(file)];
    await route.fulfill({ body: await fs.readFile(file), contentType });
  };
}

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  test(engine + ": saving a profile in the tray panel", async (t) => {
    assert(["chromium", "webkit"].includes(engine), "BROWSER must be chromium or webkit");
    const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
    const errors = [];
    const pages = [];
    t.after(async () => {
      if (process.env.ARTIFACT_DIR) {
        await fs.mkdir(process.env.ARTIFACT_DIR, { recursive: true });
        for (const [i, p] of pages.entries()) await p.screenshot({ path: path.join(process.env.ARTIFACT_DIR, `${engine}-panel-profiles-${i}.png`) });
      }
      await browser.close();
    });
    const open = async (lang, saved) => {
      const page = await (await browser.newContext({ viewport: { width: 440, height: 420 } })).newPage();
      pages.push(page);
      page.setDefaultTimeout(5000);
      page.on("pageerror", (e) => errors.push(e.message));
      await page.route("**/*", server(lang, saved));
      await page.goto("http://magpie.test/?mode=panel");
      await page.locator("#profiles .chip").first().waitFor({ state: "attached" }); // shown with the tab
      await page.locator('[data-ptab="profiles"]').click();
      await page.waitForTimeout(300);
      return page;
    };
    const view = (page) => page.locator("#view-agents").evaluate((v) => v.scrollTop);

    for (const [lang, add, save] of [["en", "＋ Save current", "Save"], ["zh", "＋ 保存当前", "保存"]]) {
      await t.test(lang, async () => {
        const saved = [];
        const page = await open(lang, saved);
        const button = page.locator("#save");
        assert.equal((await button.textContent()).trim(), add);

        // the reader scrolls to the button; the field opens beside it, whole,
        // with one ring, and nothing moves
        const box = await page.locator("#view-agents").boundingBox();
        await page.mouse.move(box.x + box.width / 2, box.y + 60);
        for (let i = 0; i < 20; i++) { await page.mouse.wheel(0, 40); await page.waitForTimeout(20); }
        await page.waitForTimeout(400);
        const before = await view(page);
        assert(before > 0, "the list must be long enough to scroll");
        await button.click();
        const field = page.locator(".profiles > .chip-input");
        await field.waitFor();
        await page.waitForTimeout(300);
        assert.equal(await view(page), before, "opening the field must not scroll the list");
        const cut = await page.evaluate(() => {
          const v = document.querySelector("#view-agents").getBoundingClientRect(), f = document.querySelector(".profiles > .chip-input");
          const inView = (e) => { const r = e.getBoundingClientRect(); return r.top >= v.top + 1 && r.bottom <= v.bottom; };
          return { field: inView(f), button: inView(document.querySelector("#save")), outline: getComputedStyle(f).outlineStyle, focused: document.activeElement === f };
        });
        assert(cut.focused, "the field must have focus");
        assert(cut.field, "the field must be whole in the view");
        assert(cut.button, "the button must stay in the view");
        assert.equal(cut.outline, "none", "the field's border is its focus; no ring over it");
        assert.equal((await button.textContent()).trim(), save, "with the field open the button saves");

        // a click on it saves, and the button is ＋ Save current again
        await field.pressSequentially("from click");
        await button.click();
        await page.locator('#profiles .chip:has-text("from click")').waitFor();
        assert.deepEqual(saved, ["from click"]);
        assert.equal(await page.locator(".profiles > .chip-input").count(), 0);
        assert.equal((await button.textContent()).trim(), add);

        // an empty field isn't saved; the click keeps it open, focused
        await button.click();
        await field.waitFor();
        await button.click();
        await page.waitForTimeout(250);
        assert.equal(await field.count(), 1, "an empty name keeps the field open");
        assert(await field.evaluate((f) => document.activeElement === f));
        assert.deepEqual(saved, ["from click"]);

        // Enter still saves; Escape closes and gives the button back
        await field.pressSequentially("from enter");
        await field.press("Enter");
        await page.locator('#profiles .chip:has-text("from enter")').waitFor();
        assert.deepEqual(saved, ["from click", "from enter"]);
        await button.click();
        await field.waitFor();
        await field.press("Escape");
        assert.equal(await field.count(), 0);
        assert.equal((await button.textContent()).trim(), add);
      });
    }
    assert.deepEqual(errors, []);
  });
}
