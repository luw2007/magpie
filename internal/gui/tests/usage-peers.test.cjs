// Run with Node's test runner and Playwright on the module path; see README.md.
// The Usage page's Requests says how the remote magpies its usage is pulled
// from are doing: one line each over the list. A remote just added is
// "reading", not empty; one that fails says what to do about it (can't be
// reached, doesn't take the key, isn't shared, too old) and when it was last
// read, since the calls shown for it are only as new as that; one that is
// fine says when it was read. Pull now posts for that remote alone and
// reloads the list; no remote, no line. No click moves the page. English and
// Chinese, light and dark; no backend, the API is faked.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");

const assets = path.resolve(__dirname, "../assets");
const now = Date.now();
const ago = (ms) => new Date(now - ms).toISOString();

const ROW = { t: ago(60e3), agent: "codex", agentName: "Codex", icon: "codex-color", provider: "relay", providerName: "Relay", req: "sol", model: "gpt-6-sol", in: 100, out: 10, ms: 900, status: 200, cost: 0.001, priced: true };

function ledger(peers) {
  return {
    period: "30d", rows: [ROW], offset: 0, total: 1, calls: 1, errors: 0, input: 100, output: 10, cache_read: 0, cache_write: 0, reasoning: 0, cost: 0.001, unpriced: 0,
    agents: [{ id: "codex", name: "Codex", icon: "codex-color" }], callerKeys: [],
    computers: peers.length ? [{ id: "this", name: "", calls: 1 }, { id: "245eab618771c093", name: "devbox", calls: 1 }] : undefined,
    peers: peers.length ? peers : undefined,
  };
}

function server(lang, theme, state) {
  return async (route) => {
    const req = route.request(), url = new URL(req.url());
    const json = (data) => route.fulfill({ json: data });
    if (url.pathname === "/boot.js") return route.fulfill({ contentType: "text/javascript", body: `window.bootPrefs = {lang:"${lang}",theme:"${theme}",web:false};` });
    if (url.pathname === "/wails/runtime.js") return route.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
    if (url.pathname === "/api/gateway/trace") return json({ mine: false, seq: 0, routes: [], totals: { requests: 0, rerouted: 0, errors: 0 }, now: new Date().toISOString() });
    if (url.pathname === "/api/gateway/history") return json({ days: [], routes: [], cut: false });
    if (url.pathname === "/api/providers") return json({ providers: [], presets: [], excluded: [], models: [], gateway: { running: false } });
    if (url.pathname === "/api/state") return json({ agents: [], profiles: [], settings: { lang, theme }, fx: { rate: 7.2, at: new Date().toISOString() } });
    if (url.pathname === "/api/usage/requests") return json(ledger(state.peers));
    if (url.pathname === "/api/usage/peers/pull") {
      state.pulls.push(req.postDataJSON());
      state.peers = state.afterPull;
      return json(state.afterPull[0]);
    }
    if (url.pathname === "/api/usage/quotas") return json([]);
    if (url.pathname === "/api/usage") return json({ calls: 1, errors: 0, input: 100, output: 10, cache_read: 0, cache_write: 0, reasoning: 0, unpriced: 0, cost: 0.001, bucket: "day", series: [], agents: [], models: [], path: "~/.config/magpie/usage.jsonl" });
    if (url.pathname === "/api/sessions") return json({ sessions: [], dirs: [] });
    if (url.pathname === "/api/sessions/stats") return json({ from: "", to: "", days: [], agents: {} });
    if (url.pathname === "/api/groups") return json({ groups: [], models: [] });
    if (url.pathname.startsWith("/api/")) return json({});
    const file = path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
    const contentType = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(file)];
    await route.fulfill({ body: await fs.readFile(file), contentType });
  };
}

const good = { id: "devbox", name: "Devbox", at: ago(120e3) };
const L = {
  en: {
    ok: /Devbox.*Usage read 2 minutes ago/s, wait: /Devbox.*Reading its usage…/s, pull: "Pull now",
    unreachable: /Can't reach it\. Check that it is running and the address is right.*last read 2 minutes ago/s,
    key: /It didn't take this key\. Use one of its enabled gateway keys in the provider\..*not read yet/s,
    notShared: /It isn't shared\. Turn on Share on local network on that computer\./,
    tooOld: /It has no usage API\. Update magpie on that computer\./,
    other: /It didn't answer: boom/,
  },
  zh: {
    ok: /Devbox.*用量读取于 2\s?分钟前/s, wait: /Devbox.*正在读取其用量…/s, pull: "立即拉取",
    unreachable: /无法连接。请确认它正在运行且地址正确.*上次读取于 2\s?分钟前/s,
    key: /它不接受这个密钥。请在供应商中改用它已启用的网关密钥之一。.*尚未读取/s,
    notShared: /它没有共享。请在那台电脑上开启“在局域网共享”。/,
    tooOld: /它没有用量 API。请在那台电脑上更新 magpie。/,
    other: /没有响应：boom/,
  },
};

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  test(engine + ": the Usage page tells how each remote magpie's usage is doing", async (t) => {
    assert(["chromium", "webkit"].includes(engine), "BROWSER must be chromium or webkit");
    const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
    t.after(() => browser.close());

    const open = async (lang, theme, state) => {
      const errors = [];
      const page = await (await browser.newContext({ viewport: { width: 1000, height: 640 }, reducedMotion: "reduce" })).newPage();
      page.setDefaultTimeout(5000);
      page.on("pageerror", (e) => errors.push(e.message));
      await page.route("**/*", server(lang, theme, state));
      await page.goto("http://magpie.test/");
      await page.locator('nav [data-view="usage"], [data-view="usage"]').first().click();
      await page.locator("#usageTab .opt").nth(1).click();
      await page.locator("#ledWrap .led tbody tr").first().waitFor();
      return { page, errors };
    };
    const text = (page) => page.locator("#ledPeers").innerText();

    for (const lang of ["en", "zh"]) {
      const w = L[lang];
      await t.test(lang + ": no remote, no line", async () => {
        const { page, errors } = await open(lang, "light", { peers: [], pulls: [] });
        assert(await page.locator("#ledPeers").isHidden());
        assert.deepEqual(errors, []);
      });

      await t.test(lang + ": a fine remote, a waiting one and each way one fails", async () => {
        const cases = [
          [{ ...good }, w.ok, false],
          [{ id: "devbox", name: "Devbox", pulling: true }, w.wait, false],
          [{ ...good, error: "couldn't reach the remote magpie Devbox: dial tcp: refused", kind: "unreachable" }, w.unreachable, true],
          [{ id: "devbox", name: "Devbox", error: "remote magpie didn't take this key", kind: "key" }, w.key, true],
          [{ ...good, error: "remote magpie isn't shared", kind: "notShared" }, w.notShared, true],
          [{ ...good, error: "remote magpie doesn't share its usage", kind: "tooOld" }, w.tooOld, true],
          [{ ...good, error: "boom", kind: "other" }, w.other, true],
        ];
        for (const [peer, want, warn] of cases) {
          const { page, errors } = await open(lang, "light", { peers: [peer], pulls: [] });
          const got = await text(page);
          assert.match(got, want, `${peer.kind || (peer.pulling ? "waiting" : "ok")}: ${got}`);
          assert.equal(await page.locator("#ledPeers .led-peer.warn").count(), warn ? 1 : 0, peer.kind || "ok");
          // the notice is read at a glance: it fits its row and leans on no side stripe
          const box = await page.locator("#ledPeers .led-peer").first().boundingBox();
          assert(box.width <= 1000 && box.height < 60, JSON.stringify(box));
          assert.equal(await page.locator("#ledPeers .led-peer").first().evaluate((e) => getComputedStyle(e).borderLeftWidth), "0px");
          assert.deepEqual(errors, []);
        }
      });

      await t.test(lang + ": Pull now asks for that remote alone and the line follows", async () => {
        const state = { peers: [{ id: "devbox", name: "Devbox", error: "remote magpie didn't take this key", kind: "key" }], pulls: [], afterPull: [{ ...good, at: ago(1e3) }] };
        const { page, errors } = await open(lang, "dark", state);
        const before = await page.locator("#view-usage").evaluate((v) => v.scrollTop);
        await page.locator("#ledPeers").getByRole("button", { name: w.pull }).click();
        await page.locator("#ledPeers .led-peer:not(.warn)").waitFor();
        assert.deepEqual(state.pulls, [{ id: "devbox" }]);
        assert.match(await text(page), /Devbox/);
        assert.equal(await page.locator("#ledPeers .led-peer.warn").count(), 0);
        assert.equal(await page.locator("#view-usage").evaluate((v) => v.scrollTop), before, "the click moved the page");
        assert.deepEqual(errors, []);
      });
    }
  });
}
