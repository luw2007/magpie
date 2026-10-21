// Run with Node's test runner and Playwright on the module path; see README.md.
// Every key of a key provider picks the quota pools it may draw from, in its
// own fold of the provider's editor (the Usage page's configuration): a pool
// a chip, its tick its own, staged on the editor's draft — one key's picks
// are staged on that key alone, a click on a chip moves nothing — and saved
// by the editor's Save, which posts only the keys whose picks changed, and
// opened again showing what was saved. A pool that has gone off the Usage
// page's configuration is still listed, named with why, and stays picked.
// Save keeps a pool with no picks out of the post, so an unchanged key adds
// nothing, and a key with every pool unticked saves an empty list. Cancel
// asks when a pick is staged, and dropping it leaves the key as it was and
// nothing posted. The chips sit in a compact block of their own under their
// key's row, the section saying in the page's words which pools a key may
// draw from. English and Chinese, Chromium and WebKit; the API is faked.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");

const assets = path.resolve(__dirname, "../assets");

const relay = () => ({
  id: "relay", name: "Relay", icon: "generic", chat: "https://relay.example/v1", responses: "", anthropic: "",
  models: [{ id: "m", name: "Model", on: true }], agents: [], fallback: [], headers: {}, routing: "", catalog: "",
  key: { set: true, masked: "test-…onal" }, balanceToken: { takes: false, set: false },
  keyList: [
    { id: "first", name: "Personal", masked: "test-…onal", active: true, on: true, poolRefs: ["team-pool", "gone-pool"], models: [] },
    { id: "second", name: "Team", masked: "test-…team", active: false, on: true },
  ],
});
const pools = [
  { id: "team-pool", name: "Team pool", sourceRef: "team", accountIDs: ["acct-1"], available: true, allowance: {}, keys: [] },
  { id: "spare-pool", name: "Spare pool", sourceRef: "team", accountIDs: [], available: false, allowance: {}, keys: [] },
];

// the providers the fixtures hold, mutated the way provider/save does, and
// every request made of them
function fixture(lang) {
  const providers = { providers: [relay()], presets: [], excluded: [], gateway: { running: true, window: true } };
  const state = { requests: [] };
  const settings = { theme: "light", lang, tray: "panel", quotaLeft: false, currency: "usd" };
  const boot = { lang, theme: "light", web: true };
  const serve = async (route) => {
    const req = route.request(), url = new URL(req.url());
    const json = (data) => route.fulfill({ json: data });
    if (url.pathname === "/boot.js") return route.fulfill({ contentType: "text/javascript", body: `window.bootPrefs = ${JSON.stringify(boot)};` });
    if (url.pathname === "/wails/runtime.js") return route.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
    if (url.pathname === "/api/state") return json({ agents: [], profiles: [], settings });
    if (url.pathname === "/api/settings") return json(settings);
    if (url.pathname === "/api/providers") return json(providers);
    if (url.pathname === "/api/usage/quotas") return json([]);
    if (url.pathname === "/api/usage/sources") return json([{ id: "team", name: "Team gateway", type: "sub2api" }]);
    if (url.pathname === "/api/usage/pools") return json(pools);
    if (url.pathname === "/api/groups") return json({ groups: [], models: [] });
    if (url.pathname === "/api/plugins") return json({ plugins: [] });
    if (url.pathname === "/api/login/usage") return json({});
    if (url.pathname === "/api/provider/save" && req.method() === "POST") {
      const body = req.postDataJSON();
      state.requests.push({ path: url.pathname, body });
      // the keys the post carries bindings for take them, the way
      // provider.ApplyKeyBindings saves them; the rest keep theirs
      providers.providers = providers.providers.map((p) => {
        if (p.id !== body.id) return p;
        const next = { ...p, ...body, models: p.models };
        if (body.keyBindings) next.keyList = (p.keyList || []).map((k) => body.keyBindings[k.id] ? { ...k, ...body.keyBindings[k.id] } : k);
        return next;
      });
      return json(providers);
    }
    if (url.pathname.startsWith("/api/")) return json({});
    const file = path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
    const contentType = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(file)];
    try { await route.fulfill({ body: await fs.readFile(file), contentType }); } catch { await route.fulfill({ status: 404, body: "" }); }
  };
  return { state, serve };
}

// the words of the editor's Quota pools section, as the page has them
const words = (page, lang) => page.evaluate((lang) => {
  const keys = ["Quota pools",
    "Pick every pool this key may draw from. Usage is the maximum across picked pools for each window, never a sum. Saved with the editor's Save.",
    "Model scope", "* = any model; comma-separated IDs, e.g. gpt-5.1,gpt-5.1-mini", "unavailable", "Discard"];
  return keys.map((k) => I18N[lang]?.[k] ?? k);
}, lang);


for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  const open = async (t, lang) => {
  const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
  const page = await (await browser.newContext({ viewport: { width: 900, height: 760 }, reducedMotion: "reduce" })).newPage();
  t.after(async () => {
    if (process.env.ARTIFACT_DIR) {
      await fs.mkdir(process.env.ARTIFACT_DIR, { recursive: true });
      await page.screenshot({ path: path.join(process.env.ARTIFACT_DIR, `${engine}-${lang}-key-pool-binding.png`), fullPage: true });
    }
    await browser.close();
  });
  page.setDefaultTimeout(5000);
  const errors = [], dialogs = [], answers = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("dialog", (d) => { dialogs.push(d.message()); answers.shift() ? d.accept() : d.dismiss(); });
  const { state, serve } = fixture(lang);
  await page.route("**/*", serve);
  await page.goto("http://magpie.test/?view=providers");
  await page.locator(".row.provider", { hasText: "Relay" }).first().click();
  const keys = page.locator(".editor .accts.keys");
  await keys.locator(".kbind").first().waitFor();
  return { page, errors, dialogs, answers, state };
  };
  for (const lang of ["en", "zh"]) {
    test(`${engine} ${lang}: a key picks its pools on chips of its own, saved with the editor's Save`, async (t) => {
      const { page, errors, dialogs, answers, state } = await open(t, lang);
      const w = await words(page, lang);
      if (lang !== "en") assert(w[1] !== "Pick every pool this key may draw from. Usage is the maximum across picked pools for each window, never a sum. Saved with the editor's Save.", "the section's words fell back to English");
      const keys = page.locator(".editor .accts.keys");
      const block = (id) => keys.locator(`.acc[data-account-id="${id}"] + .kbind`);

      // each key's own fold: its picks ticked, the pool that has gone off
      // the configuration still listed, named with why, and stayed picked
      assert.deepEqual(await block("first").locator(".kbind-chip .kbind-meta").allTextContents(),
        ["Team pool", `Spare pool · ${w[4]}`, `gone-pool · ${w[4]}`], "the first key's pools");
      assert.deepEqual(await ticks(block("first")), [true, false, true], "the first key's picks");
      assert.equal(await block("first").locator(".kbind-chip:not(.unavailable)").count(), 1, "a drawable pool is marked unavailable");
      assert.deepEqual(await ticks(block("second")), [false, false], "the second key starts unpicked");
      const models = (id) => block(id).locator("input[type=\"text\"]");
      assert.equal(await models("first").inputValue(), "*", "a key with no models of its own takes every model");
      assert.equal(await models("first").getAttribute("placeholder"), w[3], "the model scope's field says what it takes");
      assert.equal(await block("first").locator("h4").nth(0).textContent(), w[0], "the section's title");
      assert.equal(await block("first").locator("p.hint").textContent(), w[1], "the section's words");
      assert.equal(await block("first").locator("h4").nth(1).textContent(), w[2], "the model scope's title");

      // compact: each block under its own key's row, no block stretched
      for (const id of ["first", "second"]) {
        const under = await block(id).evaluate((e) => {
          const r = e.previousElementSibling.getBoundingClientRect();
          const chips = [...e.querySelectorAll(".kbind-chip")].map((c) => c.getBoundingClientRect());
          const b = e.getBoundingClientRect();
          return {
            left: (chips[0]?.left ?? b.left) - r.left,
            width: chips.length ? Math.max(...chips.map((c) => c.right)) - Math.min(...chips.map((c) => c.left)) : b.width,
            top: b.top - r.bottom,
          };
        });
        assert(under.left > 10, `a block starts at its key's left edge (${under.left}px)`);
        assert(under.width < 500, `a block is stretched to the list's width (${under.width}px)`);
        assert(under.top >= -1, `a block rides over its key's row (${under.top}px)`);
      }

      // staging is a key's own: a tick on the second stages the second alone
      const body = await page.evaluate(() => JSON.stringify(draft?.keyBindings || {}));
      assert.equal(body, "{}", "a pick staged before a click");
      await page.locator(".kbind-chip .kbind-meta", { hasText: "Team pool" }).nth(1).click();
      assert.deepEqual(Object.keys(JSON.parse(await page.evaluate(() => JSON.stringify(draft?.keyBindings || {})))), ["second"], "a tick on the second staged the first");
      assert.equal(await page.evaluate(() => document.querySelector("#modal .ebody")?.scrollTop || 0), 0, "a tick moved the editor");

      // the editor's Save posts only the key whose picks changed
      await page.locator(".editor .bar").getByRole("button", { name: lang === "zh" ? "保存" : "Save", exact: true }).click();
      await page.locator(".row.provider", { hasText: "Relay" }).first().waitFor();
      assert.equal(state.requests.length, 1, "Save posted twice");
      const save = state.requests[0];
      assert.equal(save.path, "/api/provider/save");
      assert.deepEqual(save.body.keyBindings, { second: { poolRefs: ["team-pool"], models: [] } }, "the picks as staged");
      assert.equal(save.body.keyBindings.first, undefined, "an unchanged key went in the post");

      // opened again, what was saved is what shows; the picked pool ticked
      await page.locator(".row.provider", { hasText: "Relay" }).first().click();
      await keys.locator(".kbind").first().waitFor();
      assert.deepEqual(await ticks(block("second")), [true, false], "the second key's saved picks");

      // every pool unticked stages an empty list, and Save posts it
      await block("second").locator(".kbind-chip input").first().uncheck();
      await page.locator(".editor .bar").getByRole("button", { name: lang === "zh" ? "保存" : "Save", exact: true }).click();
      await page.locator(".row.provider", { hasText: "Relay" }).first().waitFor();
      assert.deepEqual(state.requests.at(-1).body.keyBindings, { second: { poolRefs: [], models: [] } }, "an empty list of pools saves as one");

      // Cancel asks when a pick is staged, and nothing is posted
      await page.locator(".row.provider", { hasText: "Relay" }).first().click();
      await keys.locator(".kbind").first().waitFor();
      await block("second").locator(".kbind-chip .kbind-meta", { hasText: "Spare pool" }).click();
      await page.locator(".editor .bar").getByRole("button", { name: lang === "zh" ? "取消" : "Cancel", exact: true }).click();
      await page.locator("dialog.action-confirm").waitFor();
      const ask = page.locator("dialog.action-confirm");
      assert.equal(await ask.locator("h2").textContent(), w[5], "the dialog's word");
      assert.equal(await ask.locator("button.primary").textContent(), w[5], "the dialog's button");
      await ask.locator("button.primary").click();
      await page.locator("#modal").waitFor({ state: "hidden" });
      assert.equal(state.requests.length, 2, "a dropped draft was posted");
      await page.locator(".row.provider", { hasText: "Relay" }).first().click();
      await keys.locator(".kbind").first().waitFor();
      assert.deepEqual(await ticks(block("second")), [false, false], "the dropped pick stayed");
      assert.deepEqual(errors, []);
    });

    test(`${engine} ${lang}: a pool that has gone off the configuration is named with why`, async (t) => {
      const { page, errors } = await open(t, lang);
      const w = await words(page, lang);
      const keys = page.locator(".editor .accts.keys");
      // the first key names poolRefs not in the configuration as unavailable
      const gone = keys.locator(".acc[data-account-id=\"first\"] + .kbind .kbind-chip");
      assert.equal(await gone.count(), 3, "a pool left the list");
      assert.equal(await gone.nth(2).evaluate((e) => e.classList.contains("unavailable")), true, "the gone pool isn't marked");
      assert.equal(await gone.nth(2).locator(".kbind-meta").textContent(), `gone-pool · ${w[4]}`, "the gone pool's name");
      assert.deepEqual(errors, []);
    });
  }
}

async function ticks(block) {
  return block.locator(".kbind-chip input").evaluateAll((es) => es.map((e) => e.checked));
}
