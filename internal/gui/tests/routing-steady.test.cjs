// Run with Node's test runner and Playwright on the module path; see README.md.
// The groups have a page of their own, and Routing does not read the live
// trace while that page is showing: nothing arrives to redraw the editor the
// reader has open, and what they type stays. Coming back to Live asks again.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");
const { openPane } = require("./routing-pane.cjs");

const assets = path.resolve(__dirname, "../assets");
const now = new Date();
const iso = (ms) => new Date(ms).toISOString();
const acct = (n) => ({ id: `acct-${n}`, provider: `prov${n}`, name: `Provider ${n}`, who: `user${n}@example.com`, kind: "account", model: `model-${n}`, known: true, used: 10 * n, plan: "Pro" });

// a request: from an agent, weighing some accounts, answered by the last
// of them after the others failed (more tries, a longer story)
function req(id, agent, ids, fails) {
  const t0 = now.getTime() - (200 - id) * 1000;
  const order = ids.map(acct);
  const tries = order.slice(0, fails + 1).map((w, i) => ({
    id: w.id, model: w.model, start: iso(t0 + i * 100), done: true, ms: 800,
    ...(i < fails ? { status: 429, fail: "rate", again: true, error: "rate limited, try again later" } : { status: 200 }),
  }));
  return { id, seq: id, time: iso(t0), agent, model: order[0].model, provider: order[0].provider, order, tries, done: true, status: 200, ms: 900 + fails * 800, tokens: 1500 };
}
const first = [req(100, "codex", [1], 0), req(101, "codex", [1, 2], 0)];
const later = [req(102, "claude", [3, 4, 5], 2), req(103, "opencode", [6, 1, 2, 3], 3)];

const models = [1, 2, 3].map((n) => ({ id: `prov${n}/model-${n}`, name: `model-${n}`, providerName: `Provider ${n}`, icon: "generic" }));
const groups = {
  models,
  groups: [
    { id: "opus-anywhere", name: "Opus anywhere", members: [models[0].id, models[1].id], routing: "", ready: true },
    { id: "cheap", name: "Cheap", members: [models[2].id], routing: "order", ready: true },
  ],
  pools: [1, 2, 3].map((n) => ({ provider: `prov${n}`, name: `Provider ${n}`, kind: "account", who: ["a", "b"], routing: "", affinity: "" })),
};

function serve(feed) {
  const state = { agents: [], profiles: [], settings: { lang: "en", theme: "light" } };
  return async (route) => {
    const url = new URL(route.request().url());
    const json = (data) => route.fulfill({ json: data });
    if (url.pathname === "/boot.js") return route.fulfill({ contentType: "text/javascript", body: 'window.bootPrefs = {lang:"en",theme:"light",web:true};' });
    if (url.pathname === "/wails/runtime.js") return route.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
    if (url.pathname === "/api/state") return json(state);
    if (url.pathname === "/api/gateway/trace") {
      const trace = (routes) => json({ mine: true, now: new Date().toISOString(), seq: routes.length ? routes[routes.length - 1].seq : Number(url.searchParams.get("after")), totals: { requests: 0, rerouted: 0, errors: 0 }, routes });
      if (!url.searchParams.get("wait")) return trace(first);
      // the long poll: answered when the test feeds a request
      feed.polls++;
      const r = await new Promise((res) => { feed.next = res; });
      return trace([r]);
    }
    if (url.pathname === "/api/gateway/history") return json({ cut: false, days: [], routes: [] });
    if (url.pathname === "/api/groups") return json(groups);
    if (url.pathname === "/api/providers") return json({ providers: [], excluded: [], gateway: { running: true, window: true, url: "http://127.0.0.1:3999" } });
    if (url.pathname.startsWith("/api/")) return json({});
    const file = path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
    const contentType = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(file)];
    await route.fulfill({ body: await fs.readFile(file), contentType });
  };
}

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  test(`${engine}: the Groups page asks for no trace, and keeps the editor's input`, async (t) => {
    const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
    const context = await browser.newContext({ viewport: { width: 1100, height: 760 } });
    const page = await context.newPage();
    page.setDefaultTimeout(5000);
    page.setDefaultNavigationTimeout(60000);
    const errors = [];
    page.on("pageerror", (e) => errors.push(e.message));
    const feed = { next: null, polls: 0 };
    await page.route("**/*", serve(feed));
    t.after(async () => {
      feed.next?.(req(999, "codex", [1], 0));
      await browser.close();
    });
    await page.goto("http://magpie.test/?view=routing");
    await openPane(page, "requests");
    await page.locator(".rt-req").nth(first.length - 1).waitFor();
    // Routing reads the live trace while its own panes are the page
    for (let i = 0; i < 60 && !feed.polls; i++) await page.waitForTimeout(50);
    assert(feed.polls > 0, "Routing asks for the live trace while Requests is shown");

    // the reader opens a group on the Groups page and types its name
    await openPane(page, "groups");
    await page.locator(".rt-group").first().waitFor();
    await page.locator(".rt-group").first().locator("button", { hasText: "Edit" }).click();
    const name = page.locator(".rt-gedit input").first();
    await name.click();
    await name.press("End");
    await page.keyboard.type(" and more");
    const typed = await name.inputValue();
    assert.equal(typed, "Opus anywhere and more");
    await page.waitForTimeout(300);

    // nothing is read while the Groups page is the page: any request fed
    // now has no reader, and the field keeps its focus and what was typed
    const asked = feed.polls;
    for (const r of later) if (feed.next) { const next = feed.next; feed.next = null; next(r); }
    await page.waitForTimeout(2500);
    assert.equal(feed.polls, asked, "the Groups page asks for the live trace");
    assert.equal(await name.evaluate((e) => e === document.activeElement), true, "the field lost its focus");
    assert.equal(await name.inputValue(), typed);
    await page.keyboard.type("!");
    assert.equal(await name.inputValue(), typed + "!");

    // back on Live, the trace is read again
    await openPane(page, "live");
    for (let i = 0; i < 60 && feed.polls === asked; i++) await page.waitForTimeout(50);
    assert(feed.polls > asked, "Live asks for the live trace again");
    await openPane(page, "requests");
    assert.equal(await page.locator(".rt-req").count(), first.length, "only the requests fed were listed");
    for (let i = 0; i < 50 && !feed.next; i++) await page.waitForTimeout(50);
    const next = feed.next;
    assert(next, "the page asks for the next trace");
    feed.next = null;
    next(later[0]);
    await page.locator(".rt-req").nth(first.length).waitFor();
    assert.deepEqual(errors, []);
  });
}
