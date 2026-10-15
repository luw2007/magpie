// Run with Node's test runner and Playwright on the module path; see README.md.
// The Routing page can show another computer's routing in place of this one's:
// a picker by the page's tabs lists "This computer" and each remote magpie in
// use, and is not there with none. Choosing one asks the live trace, the kept
// days and a request with ?source=<id>, empties what the other computer showed
// (its requests are never mixed in) and goes back to this computer's by the
// same menu. A remote that fails says what to do about it, by the kind the
// server sorted the failure into, and leaves the page where it was. English
// and Chinese; no backend, the API is faked.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");
const { openPane } = require("./routing-pane.cjs");

const assets = path.resolve(__dirname, "../assets");
const now = new Date();
const day = [now.getFullYear(), now.getMonth() + 1, now.getDate()].map((n) => String(n).padStart(2, "0")).join("-");
const at = (i) => new Date(now.getTime() - (i + 1) * 60e3).toISOString();
const who = { id: "fixture-key", provider: "fixture", name: "Fixture", who: "key-1", kind: "key", model: "model-a", routing: "", used: 0 };
const route = (id, model) => ({
  id, seq: id, time: at(0), agent: "fixture", model, provider: "fixture",
  order: [who], tries: [{ id: who.id, model, start: at(0), done: true, status: 200, ms: 1200 }],
  done: true, status: 200, ms: 1200, tokens: 3000,
});
const HERE = route(11, "model-here"), THERE = route(22, "model-there");
const state = { agents: [{ id: "fixture", name: "Fixture", path: "/test/config.toml", fields: [] }], profiles: [], settings: { lang: "en", theme: "light" } };

const TEXT = {
  en: {
    unreachable: /Can't reach Devbox\. Check that it is running and the address is right\./,
    key: /Devbox didn't take this key\./,
    notShared: /Devbox isn't shared\. Turn on Share on local network on that computer\./,
    tooOld: /Devbox has no routing API\. Update magpie on that computer\./,
    other: /Devbox didn't answer: boom/,
    here: "This computer",
  },
  zh: {
    unreachable: /无法连接 Devbox。请确认它正在运行且地址正确。/,
    key: /Devbox 不接受这个密钥。/,
    notShared: /Devbox 没有共享。请在那台电脑上开启“在局域网共享”。/,
    tooOld: /Devbox 没有路由 API。请在那台电脑上更新 magpie。/,
    other: /Devbox 没有响应：boom/,
    here: "本机",
  },
};

function server(lang, remotes, seen, fail) {
  return async (r) => {
    const url = new URL(r.request().url());
    const json = (data, status = 200) => r.fulfill({ json: data, status });
    const src = url.searchParams.get("source") || "";
    if (url.pathname === "/boot.js") return r.fulfill({ contentType: "text/javascript", body: `window.bootPrefs = {lang:"${lang}",theme:"light",web:true};` });
    if (url.pathname === "/wails/runtime.js") return r.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
    if (url.pathname === "/api/state") return json({ ...state, settings: { lang, theme: "light" } });
    if (url.pathname === "/api/gateway/trace" || url.pathname === "/api/gateway/history") {
      seen.push(url.pathname.split("/").pop() + ":" + src);
      if (src && fail.kind) return json({ error: "boom", kind: fail.kind === "other" ? "other" : fail.kind }, 502);
      if (url.pathname.endsWith("trace")) {
        if (url.searchParams.get("wait")) await new Promise((x) => setTimeout(x, 20e3));
        return json({ mine: true, now: now.toISOString(), seq: 30, totals: { requests: 1, rerouted: 0, errors: 0 }, routes: [src ? THERE : HERE] });
      }
      const d = url.searchParams.get("day");
      return json({ cut: false, days: [{ day, requests: 1 }], routes: d ? [src ? THERE : HERE] : [] });
    }
    if (url.pathname === "/api/groups") return json({ groups: [] });
    if (url.pathname === "/api/providers") {
      const providers = remotes.map((id) => ({ id, name: "Devbox", preset: "remote-magpie", models: [] }));
      return json({ providers, gateway: { running: true, window: true } });
    }
    if (url.pathname.startsWith("/api/")) return json({});
    const file = path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
    const contentType = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(file)];
    await r.fulfill({ body: await fs.readFile(file), contentType });
  };
}

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  test(engine + ": the Routing page switches to another computer's routing", async (t) => {
    assert(["chromium", "webkit"].includes(engine), "BROWSER must be chromium or webkit");
    const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
    t.after(() => browser.close());

    const open = async (lang, remotes, fail = {}) => {
      const errors = [], seen = [];
      const page = await (await browser.newContext({ viewport: { width: 1100, height: 700 }, reducedMotion: "reduce" })).newPage();
      page.setDefaultTimeout(5000);
      page.on("pageerror", (e) => errors.push(e.message));
      await page.route("**/*", server(lang, remotes, seen, fail));
      await page.goto("http://magpie.test/?view=routing");
      await openPane(page, "requests");
      await page.locator(".rt-req").first().waitFor();
      return { page, errors, seen };
    };
    const pick = async (page, name) => {
      await page.locator("#rtSource").click();
      await page.locator(".sess-menu .pm-item", { hasText: name }).click();
    };

    for (const lang of ["en", "zh"]) {
      const w = TEXT[lang];

      await t.test(lang + ": no remote magpie, no picker", async () => {
        const { page, errors, seen } = await open(lang, []);
        assert(await page.locator("#rtSource").isHidden());
        assert(seen.every((s) => s.endsWith(":")), "asked with a source though none is in use: " + seen);
        assert.deepEqual(errors, []);
      });

      await t.test(lang + ": choosing a remote shows its requests in place of this computer's, and back", async () => {
        const { page, errors, seen } = await open(lang, ["devbox"]);
        await page.locator("#rtSource").waitFor({ state: "visible" });
        assert.match(await page.locator("#rtSource").innerText(), new RegExp(w.here));
        assert.equal(await page.locator(".rt-req").count(), 1);
        assert.match(await page.locator("#rt, #view-routing").first().innerText() + await page.locator(".rt-req").first().innerText(), /model-here/);

        await pick(page, "Devbox");
        await page.locator(".rt-req", { hasText: "model-there" }).waitFor();
        assert.equal(await page.locator(".rt-req", { hasText: "model-here" }).count(), 0, "this computer's request stayed in the list");
        assert(seen.includes("trace:devbox"), "the live trace was not asked of the remote: " + seen);
        assert(seen.includes("history:devbox"), "the days were not asked of the remote: " + seen);
        assert.match(await page.locator("#rtSource").innerText(), /Devbox/);

        await pick(page, w.here);
        await page.locator(".rt-req", { hasText: "model-here" }).waitFor();
        assert.equal(await page.locator(".rt-req", { hasText: "model-there" }).count(), 0, "the remote's request stayed in the list");
        assert.deepEqual(errors, []);
      });

      await t.test(lang + ": a remote that fails says what to do about it", async () => {
        const fail = {};
        const { page, errors } = await open(lang, ["devbox"], fail);
        for (const kind of ["unreachable", "key", "notShared", "tooOld", "other"]) {
          fail.kind = kind;
          await pick(page, w.here);
          await pick(page, "Devbox");
          await page.locator("#rtSrcNote").filter({ hasText: w[kind] }).waitFor();
          assert.equal(await page.locator(".rt-req", { hasText: "model-here" }).count(), 0, kind + ": this computer's request shown under the remote's name");
        }
        // the remote answers again: the note goes and its requests are there
        fail.kind = "";
        await pick(page, w.here);
        await pick(page, "Devbox");
        await page.locator(".rt-req", { hasText: "model-there" }).waitFor();
        assert(await page.locator("#rtSrcNote").isHidden());
        assert.deepEqual(errors, []);
      });
    }
  });
}
