// Run with Node's test runner and Playwright on the module path; see README.md.
// The Usage page's configuration, at the foot of Overview (900be8d0): usage
// sources and quota pools as cards, each group headed by its title, how many
// it holds and its Add, and each card carrying what it is and what may be
// done to it. A source says its type and whether it is on; a pool says
// whether its usage is available and which source it draws on, and keeps its
// pool, source, accounts and the keys bound to it folded under Details. The
// section follows the page's own note and is drawn apart from it, with no
// left-border accent, and the cards fit a narrow window one to a row.
// English and Chinese, Chromium and WebKit; the API is faked here.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");
const { reach } = require("./reader.cjs");

const assets = path.resolve(__dirname, "../assets");

// what usage_config.go's GET routes answer: sources as PublicUsageSources
// masks them, pools as the handler adds allowance, available and keys
const sources = [
  { id: "team", name: "Team gateway", type: "sub2api", baseURL: "https://usage.example.com" },
  { id: "legacy", name: "Legacy proxy", type: "google-proxy", baseURL: "https://proxy.example.com", off: true },
];
const pools = [
  { id: "team-pool", name: "Team pool", sourceRef: "team", accountIDs: ["acct-1", "acct-2"], available: true,
    allowance: { "5 hours": 40 }, keys: ["Relay / Personal (relay / first)"] },
  { id: "spare-pool", name: "Spare pool", sourceRef: "legacy", accountIDs: [], available: false, allowance: null, keys: [] },
];
const usage = { calls: 0, errors: 0, input: 0, output: 0, cache_read: 0, cache_write: 0, reasoning: 0, cost: 0, unpriced: 0, series: [], bucket: "day" };

function serve(lang) {
  const settings = { theme: "light", lang, tray: "panel", quotaLeft: false, currency: "usd" };
  return async (route) => {
    const url = new URL(route.request().url());
    const json = (data) => route.fulfill({ json: data });
    if (url.pathname === "/boot.js") return route.fulfill({ contentType: "text/javascript", body: `window.bootPrefs = ${JSON.stringify({ lang, theme: "light", web: true })};` });
    if (url.pathname === "/wails/runtime.js") return route.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
    if (url.pathname === "/api/state") return json({ agents: [], profiles: [], settings });
    if (url.pathname === "/api/settings") return json(settings);
    if (url.pathname === "/api/providers") return json({ providers: [], presets: [], excluded: [], gateway: { running: true, window: true } });
    if (url.pathname === "/api/groups") return json({ groups: [], models: [] });
    if (url.pathname === "/api/plugins") return json({ plugins: [] });
    if (url.pathname === "/api/usage/quotas") return json([]);
    if (url.pathname === "/api/usage") return json(usage);
    if (url.pathname === "/api/usage/sources") return json(sources);
    if (url.pathname === "/api/usage/pools") return json(pools);
    if (url.pathname.startsWith("/api/")) return json({});
    const file = path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
    const contentType = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(file)];
    try { await route.fulfill({ body: await fs.readFile(file), contentType }); } catch { await route.fulfill({ status: 404, body: "" }); }
  };
}

const sideways = (page) => page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth + 1);
const scrollTop = (page) => page.evaluate(() => document.getElementById("view-usage").scrollTop);

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  for (const lang of ["en", "zh"]) {
    test(`${engine} ${lang}: the Usage page's sources and pools are cards, counted and acted on`, async (t) => {
      const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
      t.after(() => browser.close());
      const page = await (await browser.newContext({ viewport: { width: 900, height: 700 }, reducedMotion: "reduce" })).newPage();
      page.setDefaultTimeout(5000);
      const errors = [];
      page.on("pageerror", (e) => errors.push(e.message));
      await page.route("**/*", serve(lang));
      await page.goto("http://magpie.test/?view=usage");

      const box = page.locator("#usageConfig");
      await box.locator(".usage-config-card").first().waitFor();
      // the section is the page's last part, after its own note, drawn apart
      assert(await page.evaluate(() => {
        const note = document.getElementById("usageNote"), config = document.getElementById("usageConfig");
        return !!(note.compareDocumentPosition(config) & Node.DOCUMENT_POSITION_FOLLOWING);
      }), "the configuration is not after the page's note");
      assert.equal(await box.evaluate((e) => getComputedStyle(e).borderTopWidth), "1px", "the section isn't set apart from the page");
      assert.equal(await box.evaluate((e) => getComputedStyle(e).borderLeftWidth), "0px", "the section has a left-border accent");

      // every string the section says, as the page says it
      const chrome = ["Usage sources and quota pools",
        "Pools use the maximum applicable account usage, never a sum. Missing usage is unknown, not zero. Separate allowance windows are retained.",
        "Usage sources", "Quota pools", "Add source", "Add pool", "Edit", "Discover accounts", "Delete",
        "Details", "Pool", "Source", "Accounts", "Keys", "Enabled", "Disabled", "Usage available", "Usage unknown / unavailable", "None"];
      const want = await page.evaluate(([lang, keys]) => keys.map((k) => I18N[lang]?.[k] ?? k), [lang, chrome]);
      if (lang !== "en") assert(want.every((w, i) => w !== chrome[i]), "a string fell back to English: " + JSON.stringify(chrome.filter((k, i) => want[i] === k)));

      assert.equal(await box.locator("h3").textContent(), want[0], "the section's title");
      assert.equal(await box.locator(":scope > p.hint").textContent(), want[1], "the section's hint");

      // each group: its title, how many it holds, and its Add
      const groups = box.locator("section.usage-config-group");
      assert.equal(await groups.count(), 2, "a group for the sources and one for the pools");
      const head = async (i) => ({
        title: await groups.nth(i).locator(".usage-config-head h4").textContent(),
        count: await groups.nth(i).locator(".usage-config-head .hint").textContent(),
        add: await groups.nth(i).locator(".usage-config-head button").textContent(),
      });
      assert.deepEqual(await head(0), { title: want[2], count: "2", add: want[4] }, "the sources' head");
      assert.deepEqual(await head(1), { title: want[3], count: "2", add: want[5] }, "the pools' head");

      // a source: what it is called, its type and whether it is on, and its actions
      const source = (name) => groups.nth(0).locator(".usage-config-card", { hasText: name });
      const team = source("Team gateway");
      assert.equal(await team.locator(".usage-config-name").textContent(), "Team gateway");
      assert.equal(await team.locator("p.hint").textContent(), "sub2api · " + want[14], "a source that is on");
      assert.equal(await source("Legacy proxy").locator("p.hint").textContent(), "google-proxy · " + want[15], "a source switched off");
      assert.deepEqual(await team.locator(".usage-config-actions button").allTextContents(), [want[6], want[7], want[8]], "Edit, Discover accounts and Delete, in that order");

      // a pool: its usage, its source, and what it holds under Details
      const pool = (name) => groups.nth(1).locator(".usage-config-card", { hasText: name });
      const teamPool = pool("Team pool");
      assert.equal(await teamPool.locator(".usage-config-name").textContent(), "Team pool");
      assert.deepEqual(await teamPool.locator("p.hint").allTextContents(), [want[16], "Team gateway"], "usage available, drawn on Team gateway");
      assert.deepEqual(await pool("Spare pool").locator("p.hint").allTextContents(), [want[17], "Legacy proxy"], "usage unknown, drawn on Legacy proxy");
      assert.deepEqual(await teamPool.locator(".usage-config-actions button").allTextContents(), [want[6], want[8]], "a pool has Edit and Delete, no Discover");

      const details = teamPool.locator("details.usage-config-details");
      assert.equal(await details.evaluate((e) => e.open), false, "Details starts folded");
      assert.equal(await details.locator("summary").textContent(), want[9]);
      const at = await scrollTop(page);
      await reach(page, details.locator("summary"));
      await details.locator("summary").click();
      await page.waitForTimeout(200);
      assert.equal(await details.evaluate((e) => e.open), true, "Details didn't open");
      assert.deepEqual(await details.locator("dt").allTextContents(), [want[10], want[11], want[12], want[13]], "Pool, Source, Accounts and Keys");
      assert.deepEqual(await details.locator("dd").allTextContents(),
        ["team-pool", "team", "acct-1, acct-2", "Relay / Personal (relay / first)"], "each detail's value");
      assert.deepEqual(await pool("Spare pool").locator("dd").allTextContents(),
        ["spare-pool", "legacy", want[18], want[18]], "a pool with nothing bound says None");
      assert.equal(await scrollTop(page), at, "opening Details moved the page");

      // no card is a stripe
      const stripes = await page.evaluate(() => [...document.querySelectorAll("#usageConfig, #usageConfig *")]
        .map((e) => getComputedStyle(e))
        .filter((s) => parseFloat(s.borderLeftWidth) > parseFloat(s.borderRightWidth) || (parseFloat(s.borderLeftWidth) > 0 && s.borderLeftColor !== s.borderRightColor)).length);
      assert.equal(stripes, 0, "a card has a left-border accent");

      // a narrow window: one card a row, nothing cut off sideways
      await page.setViewportSize({ width: 440, height: 760 });
      await page.waitForTimeout(200);
      assert(!(await sideways(page)), "the page scrolls sideways at 440px");
      assert(await box.evaluate((e) => e.scrollWidth <= e.clientWidth + 1), "the section overflows its own width at 440px");
      const stack = await groups.nth(1).locator(".usage-config-card").evaluateAll((es) => es.map((e) => e.getBoundingClientRect()));
      assert.equal(stack.length, 2);
      assert(stack[0].bottom <= stack[1].top + 1, "the pool cards sit side by side in a narrow window");
      assert.deepEqual(errors, []);
    });
  }
}
