// Exercise the real assets with isolated gateway fixtures, not a local config.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");
const { openPane, shownPane } = require("./routing-pane.cjs");

const assets = path.resolve(__dirname, "../assets");
const today = new Date().toISOString().slice(0, 10);
const route = { id: 12, seq: 12, time: new Date().toISOString(), agent: "fixture", model: "a/m", provider: "a",
  order: [{ id: "a", provider: "a", name: "A", kind: "provider", model: "m" }],
  tries: [{ id: "a", model: "m", start: new Date().toISOString(), done: true, status: 200, ms: 900 }],
  done: true, status: 200, ms: 900, tokens: 1200 };
const models = [{ id: "a/m", name: "m", providerName: "A", icon: "generic" }];

function serve(lang, arrived) {
  return async (request) => {
    const url = new URL(request.request().url());
    const json = (data) => request.fulfill({ json: data });
    if (url.pathname === "/boot.js") return request.fulfill({ contentType: "text/javascript", body: `window.bootPrefs = {lang:"${lang}",theme:"light",web:true};` });
    if (url.pathname === "/wails/runtime.js") return request.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
    if (url.pathname === "/api/state") return json({ agents: [{ id: "fixture", name: "Fixture", path: "/test/config.toml", fields: [] }], profiles: [], settings: { lang, theme: "light" } });
    if (url.pathname === "/api/plugins") return json({ plugins: [] });
    if (url.pathname === "/api/gateway/trace") {
      if (url.searchParams.has("wait")) await arrived;
      return json({ mine: true, now: new Date().toISOString(), seq: url.searchParams.has("wait") ? 12 : 1,
        totals: { requests: url.searchParams.has("wait") ? 1 : 0, rerouted: 0, errors: 0 }, routes: url.searchParams.has("wait") ? [route] : [] });
    }
    if (url.pathname === "/api/gateway/history") return json({ cut: false, days: [{ day: today, requests: 1 }], routes: url.searchParams.has("day") ? [route] : [] });
    if (url.pathname === "/api/groups") return json({ models, pools: [], deciders: [], found: true, groups: [] });
    if (url.pathname === "/api/providers") return json({ providers: [], presets: [], excluded: [], gateway: { running: true, window: true, url: "http://127.0.0.1:3999" } });
    if (url.pathname.startsWith("/api/")) return json({});
    const file = path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
    const contentType = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(file)];
    await request.fulfill({ body: await fs.readFile(file), contentType });
  };
}

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  for (const [lang, width] of [["en", 1100], ["de", 390]]) {
    test(`${engine} ${lang} ${width}px: responsive routing panes, live arrival, and reachable metrics`, async (t) => {
      let release;
      const arrived = new Promise((resolve) => { release = resolve; });
      const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
      t.after(() => browser.close());
      const page = await (await browser.newContext({ viewport: { width, height: 640 }, reducedMotion: "reduce" })).newPage();
      page.setDefaultTimeout(5000);
      const errors = [];
      page.on("pageerror", (e) => errors.push(e.message));
      await page.route("**/*", serve(lang, arrived));
      await page.goto("http://magpie.test/?view=routing");
      await page.locator("#rtPages .opt").nth(3).waitFor();
      const shot = async (pane) => {
        if (!process.env.ARTIFACT_DIR) return;
        await fs.mkdir(process.env.ARTIFACT_DIR, { recursive: true });
        await page.screenshot({ path: path.join(process.env.ARTIFACT_DIR, `routing-${engine}-${lang}-${width}-${pane}.png`) });
      };
      if (lang === "de") assert.deepEqual(await page.locator("#rtPages .opt").allTextContents(),
        ["Live", "Anfragen", "Weiterleitungsgruppen", "Mehrere Konten oder Schlüssel"]);
      assert.deepEqual(await shownPane(page), { on: 0, live: true, requests: false, groups: false, pools: false });
      await shot("live");
      await openPane(page, "requests");
      release();
      await page.locator(".rt-req").first().waitFor();
      assert.deepEqual(await shownPane(page), { on: 1, live: false, requests: true, groups: false, pools: false });
      await shot("requests-arrived");
      await page.locator(".rt-days .rt-day").nth(1).click();
      await page.locator(".rt-req").first().click();
      assert.deepEqual(await shownPane(page), { on: 0, live: true, requests: false, groups: false, pools: false });
      await openPane(page, "groups");
      await page.locator("#rtNewGroup").click();
      const draft = page.locator(".rt-groups .rt-gedit input").first();
      await draft.fill("Unsaved fixture");
      for (const other of ["pools", "requests", "live"]) {
        await openPane(page, other);
        await openPane(page, "groups");
        assert.equal(await draft.inputValue(), "Unsaved fixture");
      }
      await shot("groups-draft");
      await page.reload();
      await page.locator("#rtPages .opt").nth(3).waitFor();
      assert.deepEqual(await shownPane(page), { on: 2, live: false, requests: false, groups: true, pools: false });
      await openPane(page, "pools");
      await shot("pools");
      await page.evaluate(() => window.newGroupWith("a/m", "Model A"));
      await page.locator(".rt-groups .rt-gedit").waitFor();
      assert.deepEqual(await shownPane(page), { on: 2, live: false, requests: false, groups: true, pools: false });
      assert.equal(await draft.inputValue(), "Model A");
      await openPane(page, "requests");
      await page.locator(".rt-days .rt-day").nth(1).click();
      const view = page.locator("#view-routing");
      await view.evaluate((v) => {
        const spacer = document.createElement("div");
        spacer.style.cssText = "height:1400px;flex:none";
        v.append(spacer);
        v.scrollTop = 1100;
      });
      const tabs = await page.locator("#rtPages").boundingBox();
      const viewport = await view.boundingBox();
      assert(tabs && viewport && tabs.y >= viewport.y && tabs.y + tabs.height <= viewport.y + viewport.height,
        "pane tabs stay visible when the view is scrolled");
      await openPane(page, "groups");
      await openPane(page, "requests");
      const metrics = page.locator("#rtMetrics");
      await metrics.click();
      assert.equal(await metrics.getAttribute("aria-expanded"), "true", "Metrics opens from the reachable request heading");
      assert(await page.locator(".rt-metric-menu:not([hidden])").count(), "Metrics choices appear");
      const horizontal = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
      assert(horizontal <= 1, `the ${width}px page overflows horizontally by ${horizontal}px`);
      assert.deepEqual(errors, []);
      await shot("metrics-scrolled");
    });
  }
}
