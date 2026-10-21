// Run with Node's test runner and Playwright on the module path; see README.md.
// Usage sources and quota pools, configured on the page (28d06318, 900be8d0):
// Add source opens a single card in the section's place, whose Save posts
// the source (a stored credential left blank is kept), whose Cancel drops
// the draft and puts the cards back; Edit reopens it with its id fixed and
// the credential's placeholder saying a blank keeps it. Discover accounts
// asks the source for its accounts and opens a new pool on them. A pool is
// named, drawn on a source and ticked full of that source's accounts, saving
// its id, name, source and account IDs; its Edit changes them. Delete asks
// first: dismissed, nothing is sent; accepted, the source or pool goes and
// the count follows. No click moves the page. English and Chinese, Chromium
// and WebKit; the API is faked here.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");
const { reach } = require("./reader.cjs");

const assets = path.resolve(__dirname, "../assets");
const usage = { calls: 0, errors: 0, input: 0, output: 0, cache_read: 0, cache_write: 0, reasoning: 0, cost: 0, unpriced: 0, series: [], bucket: "day" };
const discovered = [
  { id: "acct-1", name: "Acct one", platform: "sub2api", type: "account" },
  { id: "acct-2", name: "Acct two", platform: "sub2api", type: "account" },
];

// the configuration the fixtures keep, mutated the way usage_config.go's
// handlers mutate it, and every request made of them
function fixture(lang) {
  const state = {
    sources: [{ id: "team", name: "Team gateway", type: "sub2api", baseURL: "https://usage.example.com" }],
    pools: [],
    requests: [],
  };
  const poolState = (p) => ({ ...p, available: true, allowance: {}, keys: [] });
  const settings = { theme: "light", lang, tray: "panel", quotaLeft: false, currency: "usd" };
  const serve = async (route) => {
    const req = route.request(), url = new URL(req.url());
    const json = (data) => route.fulfill({ json: data });
    const body = () => req.postDataJSON() || {};
    if (url.pathname === "/boot.js") return route.fulfill({ contentType: "text/javascript", body: `window.bootPrefs = ${JSON.stringify({ lang, theme: "light", web: true })};` });
    if (url.pathname === "/wails/runtime.js") return route.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
    if (url.pathname === "/api/state") return json({ agents: [], profiles: [], settings });
    if (url.pathname === "/api/settings") return json(settings);
    if (url.pathname === "/api/providers") return json({ providers: [], presets: [], excluded: [], gateway: { running: true, window: true } });
    if (url.pathname === "/api/groups") return json({ groups: [], models: [] });
    if (url.pathname === "/api/plugins") return json({ plugins: [] });
    if (url.pathname === "/api/usage/quotas") return json([]);
    if (url.pathname === "/api/usage") return json(usage);
    if (url.pathname === "/api/usage/sources" && req.method() === "GET") return json(state.sources);
    if (url.pathname === "/api/usage/pools" && req.method() === "GET") return json(state.pools.map(poolState));
    const sourcePath = /^\/api\/usage\/sources\/([^/]+)(\/discover)?$/.exec(url.pathname);
    if (sourcePath) {
      const id = decodeURIComponent(sourcePath[1]);
      const found = state.sources.find((s) => s.id === id);
      state.requests.push({ method: req.method(), path: url.pathname, body: req.method() === "GET" ? null : body() });
      if (sourcePath[2]) return json(discovered);
      if (req.method() === "DELETE") {
        if (state.pools.some((p) => p.sourceRef === id)) return route.fulfill({ status: 400, body: "source is referenced by pool " + id });
        state.sources = state.sources.filter((s) => s.id !== id);
        return json(state.sources);
      }
      // PUT: the path's id wins, and a blank credential keeps the stored one
      const input = { ...body(), id };
      if (!input.credential) input.credential = found?.credential || "";
      state.sources = found ? state.sources.map((s) => s.id === id ? { ...input } : s) : [...state.sources, input];
      return json(state.sources);
    }
    if (url.pathname === "/api/usage/sources" && req.method() === "POST") {
      state.requests.push({ method: "POST", path: url.pathname, body: body() });
      state.sources = [...state.sources, { ...body() }];
      return json(state.sources);
    }
    const poolPath = /^\/api\/usage\/pools\/([^/]+)$/.exec(url.pathname);
    if (poolPath) {
      const id = decodeURIComponent(poolPath[1]);
      state.requests.push({ method: req.method(), path: url.pathname, body: req.method() === "GET" ? null : body() });
      if (req.method() === "DELETE") {
        state.pools = state.pools.filter((p) => p.id !== id);
        return json(state.pools);
      }
      state.pools = state.pools.map((p) => p.id === id ? { ...body(), id } : p);
      return json(state.pools);
    }
    if (url.pathname === "/api/usage/pools" && req.method() === "POST") {
      state.requests.push({ method: "POST", path: url.pathname, body: body() });
      state.pools = [...state.pools, { ...body() }];
      return json(state.pools);
    }
    if (url.pathname.startsWith("/api/")) return json({});
    const file = path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
    const contentType = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(file)];
    try { await route.fulfill({ body: await fs.readFile(file), contentType }); } catch { await route.fulfill({ status: 404, body: "" }); }
  };
  return { state, serve };
}

const words = {
  en: { save: "Save", cancel: "Cancel", edit: "Edit", del: "Delete", addSource: "Add source", addPool: "Add pool",
    discover: "Discover accounts", newSource: "Add usage source", editSource: "Edit usage source",
    newPool: "Add quota pool", editPool: "Edit quota pool", keep: "Stored credential (leave blank to keep)", credential: "Credential", saved: "Saved account" },
  zh: { save: "保存", cancel: "取消", edit: "编辑", del: "删除", addSource: "新增来源", addPool: "新增配额池",
    discover: "发现账户", newSource: "新增用量来源", editSource: "编辑用量来源",
    newPool: "新增配额池", editPool: "编辑配额池", keep: "已存储凭证（留空则保留）", credential: "凭证", saved: "已保存账户" },
};

const sideways = (page) => page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth + 1);
const at = (page) => page.evaluate(() => document.getElementById("view-usage").scrollTop);

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  for (const lang of ["en", "zh"]) {
    const w = words[lang];
    const open = async (t, name) => {
      const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
      const page = await (await browser.newContext({ viewport: { width: 900, height: 760 }, reducedMotion: "reduce" })).newPage();
      t.after(async () => {
        if (process.env.ARTIFACT_DIR) {
          await fs.mkdir(process.env.ARTIFACT_DIR, { recursive: true });
          await page.screenshot({ path: path.join(process.env.ARTIFACT_DIR, `${engine}-${lang}-${name}.png`), fullPage: true });
        }
        await browser.close();
      });
      page.setDefaultTimeout(5000);
      const errors = [], dialogs = [], answers = [];
      page.on("pageerror", (e) => errors.push(e.message));
      // the Delete confirmations: the next answer accepts, else dismissed
      page.on("dialog", (d) => { dialogs.push(d.message()); answers.shift() ? d.accept() : d.dismiss(); });
      const { state, serve } = fixture(lang);
      await page.route("**/*", serve);
      await page.goto("http://magpie.test/?view=usage");
      await page.locator("#usageConfig .usage-config-card").first().waitFor();
      return { page, errors, dialogs, answers, state };
    };
    const card = (page, name) => page.locator("#usageConfig .usage-config-card", { hasText: name });
    const count = (page, i) => page.locator("#usageConfig section.usage-config-group").nth(i).locator(".usage-config-head .hint").textContent();
    const editor = (page) => page.locator("#usageConfig .usage-config-editor");
    const field = (page, label) => editor(page).locator(`[aria-label="${label}"]`);

    test(`${engine} ${lang}: a usage source is added, edited, discovered from and deleted`, async (t) => {
      const { page, errors, dialogs, answers, state } = await open(t, "usage-source-crud");
      const before = await at(page);
      const sourceGroup = page.locator("#usageConfig section.usage-config-group").nth(0);

      // Add source: one card in the section's place, its own Save and Cancel
      await reach(page, sourceGroup.locator(".usage-config-head button"));
      await sourceGroup.locator(".usage-config-head button").click();
      await editor(page).waitFor();
      assert.equal(await page.locator("#usageConfig section.usage-config-group").count(), 0, "the cards stayed beside the editor");
      assert.equal(await editor(page).locator("h3").textContent(), w.newSource);
      assert.equal(await at(page), before, "Add source moved the page");
      assert.equal(await field(page, "id").isDisabled(), false, "a new source's id is fixed");

      await field(page, "id").fill("backup");
      await field(page, "name").fill("Backup gateway");
      await field(page, "type").fill("sub2api");
      await field(page, "baseURL").fill("https://backup.example.com");
      await field(page, "credential").fill("usage-secret");
      assert.equal(await field(page, "credential").getAttribute("type"), "password", "the credential is shown in the clear");
      assert.equal(await field(page, "credential").getAttribute("placeholder"), w.credential, "a new source asks for its credential");
      await editor(page).getByRole("button", { name: w.save, exact: true }).click();
      await card(page, "Backup gateway").waitFor();
      const post = state.requests.find((r) => r.method === "POST" && r.path === "/api/usage/sources");
      assert(post, "Save posted no source");
      assert.deepEqual(Object.fromEntries(Object.entries(post.body).filter(([, v]) => v !== "" && v !== false)),
        { id: "backup", name: "Backup gateway", type: "sub2api", baseURL: "https://backup.example.com", credential: "usage-secret" },
        "the source as typed");
      assert.equal(await count(page, 0), "2", "the sources' count didn't follow");
      assert.equal(await page.locator("#usageConfig section.usage-config-group").count(), 2, "the cards didn't come back");

      // Edit: the id is fixed, a blank credential keeps the stored one
      const backup = card(page, "Backup gateway");
      await reach(page, backup.getByRole("button", { name: w.edit, exact: true }));
      await backup.getByRole("button", { name: w.edit, exact: true }).click();
      await editor(page).waitFor();
      assert.equal(await editor(page).locator("h3").textContent(), w.editSource);
      assert.equal(await field(page, "id").inputValue(), "backup");
      assert.equal(await field(page, "id").isDisabled(), true, "an existing source's id may be changed");
      assert.equal(await field(page, "credential").inputValue(), "", "the stored credential was put back in the page");
      assert.equal(await field(page, "credential").getAttribute("placeholder"), w.keep, "a stored credential isn't said to be kept");
      await field(page, "name").fill("Backup usage");
      await editor(page).getByRole("button", { name: w.save, exact: true }).click();
      await card(page, "Backup usage").waitFor();
      const put = state.requests.find((r) => r.method === "PUT");
      assert.equal(put.path, "/api/usage/sources/backup", "the source's id didn't go in the path");
      assert.equal(put.body.name, "Backup usage");
      assert.equal(put.body.credential, "", "a blank credential wasn't left to be kept");
      assert.equal(await card(page, "Backup gateway").count(), 0, "the old name is still listed");

      // Cancel drops the draft and puts the cards back, posting nothing
      const sends = state.requests.filter((r) => r.method !== "GET").length;
      await reach(page, card(page, "Backup usage").getByRole("button", { name: w.edit, exact: true }));
      await card(page, "Backup usage").getByRole("button", { name: w.edit, exact: true }).click();
      await field(page, "name").fill("Dropped");
      await editor(page).getByRole("button", { name: w.cancel, exact: true }).click();
      await card(page, "Backup usage").waitFor();
      assert.equal(state.requests.filter((r) => r.method !== "GET").length, sends, "Cancel posted the draft");
      assert.equal(await card(page, "Dropped").count(), 0, "the draft's name stayed");

      // Discover accounts asks the source, and opens a pool on what it says
      await reach(page, card(page, "Backup usage").getByRole("button", { name: w.discover, exact: true }));
      await card(page, "Backup usage").getByRole("button", { name: w.discover, exact: true }).click();
      await editor(page).waitFor();
      const ask = state.requests.filter((r) => r.path === "/api/usage/sources/backup/discover");
      assert.deepEqual(ask.map((r) => r.method), ["POST"], "Discover accounts didn't ask the source");
      assert.equal(await editor(page).locator("h3").textContent(), w.newPool, "the discovered accounts didn't open a pool");
      assert.deepEqual(await editor(page).locator("label.row span").allTextContents(),
        ["Acct one · acct-1 sub2api account", "Acct two · acct-2 sub2api account"], "the source's accounts");
      assert.equal(await editor(page).locator("select").inputValue(), "backup", "the pool isn't drawn on the source that was asked");
      assert.equal(await editor(page).locator("label.row input:checked").count(), 0, "an account was picked already");

      // Delete asks first: dismissed, nothing goes
      await editor(page).getByRole("button", { name: w.cancel, exact: true }).click();
      await card(page, "Backup usage").waitFor();
      await reach(page, card(page, "Backup usage").getByRole("button", { name: w.del, exact: true }));
      await card(page, "Backup usage").getByRole("button", { name: w.del, exact: true }).click();
      await page.waitForTimeout(200);
      assert.equal(dialogs.at(-1), `${lang === "zh" ? "删除来源" : "Delete source"} Backup usage?`);
      assert.equal(state.requests.filter((r) => r.method === "DELETE").length, 0, "a dismissed Delete sent its request");
      assert.equal(await card(page, "Backup usage").count(), 1, "a dismissed Delete took the source");

      // accepted, it goes and the count follows
      answers.push(true);
      await card(page, "Backup usage").getByRole("button", { name: w.del, exact: true }).click();
      await page.waitForFunction(() => document.querySelectorAll("#usageConfig .usage-config-card").length === 1);
      const del = state.requests.filter((r) => r.method === "DELETE");
      assert.deepEqual(del.map((r) => r.path), ["/api/usage/sources/backup"]);
      assert.equal(await card(page, "Backup usage").count(), 0, "the deleted source is still listed");
      assert.equal(await count(page, 0), "1", "the sources' count didn't follow the delete");
      assert(!(await sideways(page)), "the page scrolls sideways");
      assert.deepEqual(errors, []);
    });

    test(`${engine} ${lang}: a quota pool is added from its source's accounts, edited and deleted`, async (t) => {
      const { page, errors, dialogs, answers, state } = await open(t, "usage-pool-crud");
      const before = await at(page);
      const poolGroup = () => page.locator("#usageConfig section.usage-config-group").nth(1);
      assert.equal(await count(page, 1), "0", "a pool is configured already");
      assert.equal(await poolGroup().locator(".sess-none").textContent(), lang === "zh" ? "无" : "None", "an empty group says so");

      // Add pool: named, drawn on a source, its accounts discovered in place
      await reach(page, poolGroup().locator(".usage-config-head button"));
      await poolGroup().locator(".usage-config-head button").click();
      await editor(page).waitFor();
      assert.equal(await editor(page).locator("h3").textContent(), w.newPool);
      assert.equal(await at(page), before, "Add pool moved the page");
      assert.equal(await editor(page).locator("select").inputValue(), "team", "the only source isn't the pool's");
      await field(page, "id").fill("shared");
      await field(page, "name").fill("Shared pool");
      await editor(page).getByRole("button", { name: w.discover, exact: true }).click();
      await editor(page).locator("label.row").first().waitFor();
      assert.equal(await editor(page).locator("label.row").count(), 2, "the source's accounts aren't listed");
      await editor(page).locator("label.row", { hasText: "Acct one" }).locator("input").check();
      await editor(page).locator("label.row", { hasText: "Acct two" }).locator("input").check();
      // another source clears what was picked: its accounts are not these
      await editor(page).locator("select").selectOption("team");
      assert.equal(await editor(page).locator("label.row").count(), 0, "picking another source kept its accounts");
      await editor(page).getByRole("button", { name: w.discover, exact: true }).click();
      await editor(page).locator("label.row").first().waitFor();
      assert.equal(await editor(page).locator("label.row input:checked").count(), 0, "the accounts came back picked");
      await editor(page).locator("label.row", { hasText: "Acct one" }).locator("input").check();
      await editor(page).locator("label.row", { hasText: "Acct two" }).locator("input").check();
      await editor(page).getByRole("button", { name: w.save, exact: true }).click();
      await card(page, "Shared pool").waitFor();
      const post = state.requests.find((r) => r.method === "POST" && r.path === "/api/usage/pools");
      assert(post, "Save posted no pool");
      assert.deepEqual(post.body, { id: "shared", name: "Shared pool", sourceRef: "team", accountIDs: ["acct-1", "acct-2"] }, "the pool as typed");
      assert.equal(await count(page, 1), "1", "the pools' count didn't follow");
      const shared = card(page, "Shared pool");
      assert.deepEqual(await shared.locator("dd").allTextContents(), ["shared", "team", "acct-1, acct-2", lang === "zh" ? "无" : "None"], "the saved pool's details");

      // Edit: its accounts change, and the id stays fixed
      await reach(page, shared.getByRole("button", { name: w.edit, exact: true }));
      await shared.getByRole("button", { name: w.edit, exact: true }).click();
      await editor(page).waitFor();
      assert.equal(await editor(page).locator("h3").textContent(), w.editPool);
      assert.equal(await field(page, "id").isDisabled(), true, "an existing pool's id may be changed");
      assert.deepEqual(await editor(page).locator("label.row span").allTextContents(), [`${w.saved} · acct-1  `, `${w.saved} · acct-2  `], "the pool's saved accounts aren't shown");
      assert.equal(await editor(page).locator("label.row input:checked").count(), 2, "the pool's accounts aren't ticked");
      await editor(page).locator("label.row", { hasText: "acct-2" }).locator("input").uncheck();
      await field(page, "name").fill("Shared pool 2");
      await editor(page).getByRole("button", { name: w.save, exact: true }).click();
      await card(page, "Shared pool 2").waitFor();
      const put = state.requests.find((r) => r.method === "PUT");
      assert.equal(put.path, "/api/usage/pools/shared", "the pool's id didn't go in the path");
      assert.deepEqual(put.body, { id: "shared", name: "Shared pool 2", sourceRef: "team", accountIDs: ["acct-1"] }, "the pool as edited");

      // Delete asks first, and the count follows once it is accepted
      const sends = state.requests.filter((r) => r.method !== "GET").length;
      await reach(page, card(page, "Shared pool 2").getByRole("button", { name: w.del, exact: true }));
      await card(page, "Shared pool 2").getByRole("button", { name: w.del, exact: true }).click();
      await page.waitForTimeout(200);
      assert.equal(dialogs.at(-1), `${lang === "zh" ? "删除配额池" : "Delete pool"} Shared pool 2?`);
      assert.equal(state.requests.filter((r) => r.method !== "GET").length, sends, "a dismissed Delete sent its request");
      answers.push(true);
      await card(page, "Shared pool 2").getByRole("button", { name: w.del, exact: true }).click();
      await poolGroup().locator(".sess-none").waitFor();
      assert.deepEqual(state.requests.filter((r) => r.method === "DELETE").map((r) => r.path), ["/api/usage/pools/shared"]);
      assert.equal(await count(page, 1), "0", "the pools' count didn't follow the delete");
      assert(!(await sideways(page)), "the page scrolls sideways");
      assert.deepEqual(errors, []);
    });
  }
}
