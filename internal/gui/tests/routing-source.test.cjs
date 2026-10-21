// Run with Node's test runner and Playwright on the module path; see README.md.
// The Routing page can show another computer's routing in place of this one's:
// a picker by the page's tabs lists "This computer" and each remote magpie in
// use, and is not there with none. Choosing one asks the live trace, the kept
// days and a request with ?source=<id>, empties what the other computer showed
// (its requests are never mixed in) and goes back to this computer's by the
// same menu. A remote that fails says what to do about it, by the kind the
// server sorted the failure into, and leaves the page where it was. An answer
// that comes back only after the reader has left that computer changes
// nothing about the computer shown now, nor what it says failed. English and
// Chinese; no backend, the API is faked.
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
// Route IDs belong to each computer: deliberately collide to catch local body leaks.
const HERE = route(11, "model-here"), THERE = route(11, "model-there");
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
  let localDays = 0;
  return async (r) => {
    const url = new URL(r.request().url());
    const json = (data, status = 200) => r.fulfill({ json: data, status });
    const src = url.searchParams.get("source") || "";
    if (url.pathname === "/boot.js") return r.fulfill({ contentType: "text/javascript", body: `window.bootPrefs = {lang:"${lang}",theme:"light",web:true};` });
    if (url.pathname === "/wails/runtime.js") return r.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
    if (url.pathname === "/api/state") return json({ ...state, settings: { lang, theme: "light" } });
    if (url.pathname === "/api/gateway/trace" || url.pathname === "/api/gateway/history") {
      seen.push(url.pathname.split("/").pop() + ":" + src);
      const d = url.searchParams.get("day");
      // A held answer comes back only when the test lets it: by then the
      // reader has left that computer. fail.hold gives the kind it fails with
      // ("" for an answer), and "history:<id>:answered" says it was delivered.
      const hold = fail.hold;
      if (url.pathname.endsWith("history") && src && hold) {
        const kind = await hold();
        await json(kind ? { error: "boom", kind: kind === "other" ? "other" : kind }
          : { cut: false, days: [{ day, requests: 9 }], routes: d ? [THERE] : [] }, kind ? 502 : 200);
        seen.push("history:" + src + ":answered");
        return;
      }
      if (src && fail.kind) return json({ error: "boom", kind: fail.kind === "other" ? "other" : fail.kind }, 502);
      if (url.pathname.endsWith("trace")) {
        if (url.searchParams.get("wait")) await new Promise((x) => setTimeout(x, 20e3));
        return json({ mine: true, now: now.toISOString(), seq: 30, totals: { requests: 1, rerouted: 0, errors: 0 }, routes: [src ? THERE : HERE] });
      }
      // this computer's days gain one more once it is asked again: a mark the
      // reader can see of which answer the bar was drawn from
      if (!src && ++localDays > 1) return json({ cut: false, days: [{ day, requests: 1 }, { day: "2020-01-01", requests: 2 }], routes: d ? [HERE] : [] });
      // each computer counts its own days: the held answer's 9 is never one's
      return json({ cut: false, days: [{ day, requests: src ? 3 : 1 }], routes: d ? [src ? THERE : HERE] : [] });
    }
    if (url.pathname === "/api/gateway/call") {
      seen.push("call:" + url.searchParams.get("route"));
      return json({ requestBody: '{"secret":"local-only-secret"}', responseBody: '{"ok":true}' });
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

    // A gate the test opens to let a held answer back, a poll of the faked
    // API's record until it says what the test waits for, and a watch that
    // what the page shows stays so for a while (a late answer would change it
    // at once, and there is nothing else to wait on for it not doing so).
    const held = () => { let release; const p = new Promise((r) => { release = r; }); return { p, release }; };
    const until = async (ok, what) => {
      for (let i = 0; i < 100; i++) { if (ok()) return; await new Promise((r) => setTimeout(r, 50)); }
      assert.fail(what);
    };
    const stays = async (check, what) => {
      for (let i = 0; i < 20; i++) { await check(what); await new Promise((r) => setTimeout(r, 50)); }
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

      await t.test(lang + ": request bodies are local only even when route IDs collide", async () => {
        const { page, errors, seen } = await open(lang, ["devbox"]);
        await openPane(page, "live");
        await page.locator(".rt-bodies-b").click();
        await page.locator(".rt-bodies").filter({ hasText: "local-only-secret" }).waitFor();
        assert.equal(seen.filter((s) => s.startsWith("call:")).length, 1);

        await pick(page, "Devbox");
        await openPane(page, "requests");
        await page.locator(".rt-req", { hasText: "model-there" }).waitFor();
        await openPane(page, "live");
        assert.equal(await page.locator(".rt-bodies-b").count(), 0, "remote request exposed the local body action");
        assert(await page.locator(".rt-bodies").isHidden(), "remote request exposed local bodies");
        assert(!await page.locator(".rt-bodies").textContent(), "local secret survived the source switch");
        assert.equal(seen.filter((s) => s.startsWith("call:")).length, 1, "remote request fetched local bodies");

        await pick(page, w.here);
        await page.locator(".rt-bodies-b").click();
        await page.locator(".rt-bodies").filter({ hasText: "local-only-secret" }).waitFor();
        assert.equal(seen.filter((s) => s.startsWith("call:")).length, 2, "source switch retained the old body cache");
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

      await t.test(lang + ": the remote's days, held until this computer is shown, leave its list alone", async () => {
        const fail = {}, gate = held();
        fail.hold = async () => { await gate.p; return ""; };
        const { page, errors, seen } = await open(lang, ["devbox"], fail);
        await pick(page, "Devbox");
        await until(() => seen.includes("history:devbox"), "the remote's days were not asked: " + seen);
        fail.hold = null; // only that answer is held
        const here = seen.filter((s) => s === "history:").length;
        await pick(page, w.here);
        await until(() => seen.filter((s) => s === "history:").length > here, "this computer's days were not asked again: " + seen);
        await page.locator(".rt-days .rt-day small").filter({ hasText: /^2$/ }).waitFor(); // the bar was drawn from this computer's answer
        await page.locator(".rt-req", { hasText: "model-here" }).waitFor();
        gate.release();
        await until(() => seen.includes("history:devbox:answered"), "the held answer was never delivered: " + seen);
        // the held answer arrives and is read; the list stays this computer's
        await stays(async (what) => {
          assert.deepEqual(await page.locator(".rt-days .rt-day small").allInnerTexts(), ["1", "2"], what + ": the remote's days were counted here");
          assert.equal(await page.locator(".rt-req", { hasText: "model-here" }).count(), 1, what + ": this computer's request left the list");
          assert.equal(await page.locator(".rt-req", { hasText: "model-there" }).count(), 0, what + ": the remote's request came in with its days");
          assert(await page.locator("#rtSrcNote").isHidden(), what + ": the held answer said something over this computer");
        }, "a held days answer");
        assert.deepEqual(errors, []);
      });

      await t.test(lang + ": the remote's failure, held until it is shown again, doesn't speak for it now", async () => {
        const fail = {}, gate = held();
        fail.hold = async () => { await gate.p; return "key"; };
        const { page, errors, seen } = await open(lang, ["devbox"], fail);
        await pick(page, "Devbox");
        await page.locator(".rt-req", { hasText: "model-there" }).waitFor();
        await until(() => seen.includes("history:devbox"), "the remote's days were not asked: " + seen);
        await pick(page, w.here);
        await page.locator(".rt-req", { hasText: "model-here" }).waitFor();
        fail.hold = null; // the answer asked now is not held
        await pick(page, "Devbox");
        await page.locator(".rt-days .rt-day small").filter({ hasText: /^3$/ }).waitFor();
        await page.locator(".rt-req", { hasText: "model-there" }).waitFor();
        assert(await page.locator("#rtSrcNote").isHidden());
        gate.release();
        await until(() => seen.includes("history:devbox:answered"), "the held answer was never delivered: " + seen);
        // the held failure arrives and is read; the note stays about nothing
        await stays(async (what) => {
          assert(await page.locator("#rtSrcNote").isHidden(), what + ": the held failure spoke for the computer shown now");
          assert.deepEqual(await page.locator(".rt-days .rt-day small").allInnerTexts(), ["3"], what + ": the held failure emptied the days shown");
          assert.equal(await page.locator(".rt-req", { hasText: "model-there" }).count(), 1, what + ": the remote's request went with the held failure");
          assert.equal(await page.locator(".rt-req", { hasText: "model-here" }).count(), 0, what + ": this computer's request came in with the held failure");
        }, "a held failure");
      });

    }
  });
}
