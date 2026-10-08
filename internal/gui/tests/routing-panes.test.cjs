// Run with Node's test runner and Playwright on the module path; see README.md.
// The Routing page is four panes, one at a time, chosen in #rtPages: Live,
// Requests, Routing groups, Several accounts or keys. Exactly the chosen
// one shows; the choice is kept (and a stored one restored) across a reload;
// a group half-made is still there when the reader looks at another pane and
// comes back; picking or replaying a request opens Live, where its story
// plays; and making a group from a model (newGroupWith) opens Groups with it
// in the editor. In English and Chinese, Chromium and WebKit.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");
const { openPane, shownPane } = require("./routing-pane.cjs");

const assets = path.resolve(__dirname, "../assets");
const now = new Date();
const day = [now.getFullYear(), now.getMonth() + 1, now.getDate()].map((n) => String(n).padStart(2, "0")).join("-");
const at = (i) => new Date(now.getTime() - (i + 1) * 60e3).toISOString();
const key = { id: "relay", provider: "relay", name: "Relay", kind: "provider", model: "m" };
const routes = Array.from({ length: 4 }, (_, i) => ({
  id: 100 - i, seq: 100 - i, time: at(i), agent: "codex", model: "relay/m", provider: "relay",
  order: [key], tries: [{ id: key.id, model: "m", start: at(i), done: true, status: 200, ms: 900 }],
  done: true, status: 200, ms: 900, tokens: 1200,
}));
const models = [
  { id: "a/m", name: "m", providerName: "A", icon: "generic" },
  { id: "b/m", name: "m", providerName: "B", icon: "generic" },
];
const found = { id: "auto-m", name: "Model M", members: ["a/m", "b/m"], auto: true, ready: true, memberInfo: models.map(({ id }) => ({ id, ready: true })) };

function serve(lang, hold) {
  const state = { agents: [{ id: "codex", name: "Codex", path: "/test/config.toml", fields: [] }], profiles: [], settings: { lang, theme: "light" } };
  return async (route) => {
    const url = new URL(route.request().url());
    const json = (data) => route.fulfill({ json: data });
    if (url.pathname === "/boot.js") return route.fulfill({ contentType: "text/javascript", body: `window.bootPrefs = {lang:"${lang}",theme:"light",web:true};` });
    if (url.pathname === "/wails/runtime.js") return route.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
    if (url.pathname === "/api/state") return json(state);
    if (url.pathname === "/api/plugins") return json({ plugins: [] });
    if (url.pathname === "/api/gateway/trace") {
      if (url.searchParams.get("wait")) {
        if (!hold) return new Promise(() => {});
        await hold;
        const live = { ...routes[0], id: 200, seq: 200, time: new Date().toISOString() };
        return json({ mine: true, now: new Date().toISOString(), seq: 2, totals: { requests: 1, rerouted: 0, errors: 0 }, routes: [live] });
      }
      return json({ mine: true, now: now.toISOString(), seq: 1, totals: { requests: 0, rerouted: 0, errors: 0 }, routes: [] });
    }
    if (url.pathname === "/api/gateway/history") {
      const d = url.searchParams.get("day");
      return json({ cut: false, days: [{ day, requests: routes.length }], routes: d ? routes : [] });
    }
    if (url.pathname === "/api/groups") return json({ models, pools: [], deciders: [], found: true, groups: [found] });
    if (url.pathname === "/api/providers") return json({ providers: [], presets: [], excluded: [], gateway: { running: true, window: true, url: "http://127.0.0.1:3999" } });
    if (url.pathname.startsWith("/api/")) return json({});
    const file = path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
    const contentType = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(file)];
    await route.fulfill({ body: await fs.readFile(file), contentType });
  };
}

const words = { en: { replay: "Replay them all" }, zh: { replay: "全部重放" } };
const only = (name) => ({ on: ["live", "requests", "groups", "pools"].indexOf(name), live: name === "live", requests: name === "requests", groups: name === "groups", pools: name === "pools" });

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  for (const lang of ["en", "zh"]) {
    const open = async (t, hold) => {
      const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
      t.after(() => browser.close());
      const page = await (await browser.newContext({ viewport: { width: 1100, height: 760 }, reducedMotion: "reduce" })).newPage();
      page.setDefaultTimeout(5000);
      const errors = [];
      page.on("pageerror", (e) => errors.push(e.message));
      await page.route("**/*", serve(lang, hold));
      await page.goto("http://magpie.test/?view=routing");
      await page.locator("#rtPages .opt").nth(3).waitFor();
      return { page, errors };
    };

    test(`${engine} ${lang}: one Routing pane shows at a time, and the choice is kept`, async (t) => {
      const { page, errors } = await open(t);
      // arrival: Live
      assert.deepEqual(await shownPane(page), only("live"));
      for (const name of ["requests", "groups", "pools", "live", "pools", "requests"]) {
        await openPane(page, name);
        assert.deepEqual(await shownPane(page), only(name));
        assert.equal(await page.evaluate(() => localStorage.getItem("magpie.routingPage")), name);
      }
      // the stored choice is where a reload lands
      await openPane(page, "groups");
      await page.reload();
      await page.locator("#rtPages .opt").nth(3).waitFor();
      await page.waitForFunction(() => document.querySelectorAll("#rtPages .opt")[2]?.getAttribute("aria-pressed") === "true");
      assert.deepEqual(await shownPane(page), only("groups"));
      // one that names no pane is none: Live
      await page.evaluate(() => localStorage.setItem("magpie.routingPage", "nowhere"));
      await page.reload();
      await page.locator("#rtPages .opt").nth(3).waitFor();
      assert.deepEqual(await shownPane(page), only("live"));
      assert.deepEqual(errors, []);
    });

    test(`${engine} ${lang}: the Requests pane lists a request that arrives while it is open`, async (t) => {
      let release;
      const { page, errors } = await open(t, new Promise((r) => { release = r; }));
      await openPane(page, "requests");
      assert.equal(await page.locator(".rt-req").count(), 0);
      release();
      await page.locator(".rt-req").first().waitFor();
      assert.deepEqual(await shownPane(page), only("requests"));
      assert.deepEqual(errors, []);
    });

    test(`${engine} ${lang}: a group half-made is still there after looking at another pane`, async (t) => {
      const { page, errors } = await open(t);
      await openPane(page, "groups");
      await page.locator(".rt-group", { hasText: "Model M" }).waitFor({ state: "attached" });
      await page.locator("#rtNewGroup").click();
      const name = page.locator(".rt-groups .rt-gedit input").first();
      await name.waitFor();
      await name.fill("Half made");
      for (const other of ["requests", "live", "pools"]) {
        await openPane(page, other);
        await openPane(page, "groups");
        assert.equal(await name.inputValue(), "Half made", `the draft survived ${other}`);
        assert.deepEqual(await shownPane(page), only("groups"));
      }
      assert.deepEqual(errors, []);
    });

    test(`${engine} ${lang}: picking or replaying a request opens Live`, async (t) => {
      const { page, errors } = await open(t);
      await openPane(page, "requests");
      await page.locator(".rt-days .rt-day").nth(1).click();
      await page.locator(".rt-req").nth(routes.length - 1).waitFor();
      await page.locator(".rt-req").nth(2).click();
      await page.waitForFunction(() => document.querySelectorAll("#rtPages .opt")[0]?.getAttribute("aria-pressed") === "true");
      assert.deepEqual(await shownPane(page), only("live"));
      await page.locator(".rt-steps li").first().waitFor();

      // back in the list, the replay of all of it plays on Live too
      await openPane(page, "requests");
      assert.deepEqual(await shownPane(page), only("requests"));
      await page.getByRole("button", { name: words[lang].replay, exact: true }).click();
      await page.waitForFunction(() => document.querySelectorAll("#rtPages .opt")[0]?.getAttribute("aria-pressed") === "true");
      assert.deepEqual(await shownPane(page), only("live"));
      await page.waitForFunction(() => !document.querySelector(".rt-replay").hidden);
      assert.deepEqual(errors, []);
    });

    test(`${engine} ${lang}: a group made from a model opens Groups with it`, async (t) => {
      const { page, errors } = await open(t);
      for (const from of ["live", "requests", "pools"]) {
        await openPane(page, from);
        await page.waitForFunction(() => window.newGroupWith);
        await page.evaluate(() => { window.newGroupWith("a/m", "Model A"); });
        await page.locator(".rt-groups .rt-gedit").waitFor();
        assert.deepEqual(await shownPane(page), only("groups"), `from ${from}`);
        assert.equal(await page.locator(".rt-groups .rt-gedit input").first().inputValue(), "Model A");
        assert.deepEqual(await page.locator(".rt-groups .rt-gedit .fbrow .n").allTextContents(), ["mA"]);
        // leave the draft: the next round starts clean
        await page.reload();
        await page.locator("#rtPages .opt").nth(3).waitFor();
      }
      assert.deepEqual(errors, []);
    });
  }
}
