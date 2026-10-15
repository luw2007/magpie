// Run with Node's test runner and Playwright on the module path; see README.md.
// Settings → Page width (1200, 1600, 2000, Full width): the content of every
// page is as wide as the pick on a large screen, centred, and a window
// narrower than the pick is not widened (the 16px gutter stays). A pick posts
// to /api/settings/page-width alone, takes effect at once without a reload,
// is there before the first paint after one (boot.js carries it), and a save
// of the other settings never changes it. English and Chinese, light and
// dark, with the API faked.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");

const assets = path.resolve(__dirname, "../assets");
const VIEWS = ["agents", "providers", "routing", "usage", "settings"];

function server(lang, theme, first, posts) {
  let cur = { lang, theme, textSize: 100, pageWidth: first };
  return async (route) => {
    const req = route.request(), url = new URL(req.url());
    const json = (data) => route.fulfill({ json: data });
    if (url.pathname === "/boot.js") return route.fulfill({ contentType: "text/javascript", body: `window.bootPrefs = {lang:"${lang}",theme:"${theme}",web:false,textSize:100${cur.pageWidth ? `,pageWidth:${cur.pageWidth}` : ""}};` });
    if (url.pathname === "/wails/runtime.js") return route.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
    if (url.pathname === "/api/state") return json({ agents: [], profiles: [], settings: cur, fx: { rate: 7.2, at: new Date().toISOString() } });
    if (url.pathname === "/api/settings/page-width") {
      const { width } = req.postDataJSON();
      posts.push(width);
      cur = { ...cur, pageWidth: width };
      return json(cur);
    }
    if (url.pathname === "/api/settings") {
      // the Settings page's own save never carries the page width
      if (req.method() === "POST") cur = { ...cur, ...req.postDataJSON(), pageWidth: cur.pageWidth };
      return json(cur);
    }
    if (url.pathname === "/api/providers") return json({ providers: [], presets: [], excluded: [], gateway: { url: "http://127.0.0.1:3425", running: true, window: true } });
    if (url.pathname === "/api/groups") return json({ groups: [], models: [] });
    if (url.pathname.startsWith("/api/usage/quotas")) return json([]);
    if (url.pathname === "/api/gateway/trace") return json({ mine: false, now: new Date().toISOString(), seq: 0, totals: { requests: 0, rerouted: 0, errors: 0 }, routes: [] });
    if (url.pathname === "/api/gateway/history") return json({ cut: false, days: [], routes: [] });
    if (url.pathname.startsWith("/api/")) return json({});
    const file = path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
    const contentType = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(file)];
    await route.fulfill({ body: await fs.readFile(file).catch(() => ""), contentType });
  };
}

// where a view's content sits: its box less its own padding
const box = (page, v) => page.evaluate((v) => {
  const e = document.querySelector(`#view-${v}`);
  const cs = getComputedStyle(e);
  const pl = parseFloat(cs.paddingLeft), pr = parseFloat(cs.paddingRight);
  return { width: Math.round(e.clientWidth - pl - pr), pl, pr, view: e.clientWidth };
}, v);

async function visit(page, v) {
  const b = page.locator(v === "settings" ? "#prefs" : `#nav button[data-view="${v}"]`);
  await b.click();
  await page.waitForFunction((v) => !document.querySelector(`#view-${v}`).hidden, v);
}

const WORDS = {
  en: { row: "Page width", full: "Full width" },
  zh: { row: "页面宽度", full: "全宽" },
};

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  test(engine + ": the page width setting", async (t) => {
    assert(["chromium", "webkit"].includes(engine), "BROWSER must be chromium or webkit");
    const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
    t.after(() => browser.close());

    const open = async (lang, theme, first, width) => {
      const posts = [], errors = [];
      const page = await (await browser.newContext({ viewport: { width, height: 900 }, reducedMotion: "reduce" })).newPage();
      page.setDefaultTimeout(5000);
      page.on("pageerror", (e) => errors.push(e.message));
      await page.route("**/*", server(lang, theme, first, posts));
      await page.goto("http://magpie.test/");
      await page.waitForSelector("#nav button.on");
      return { page, posts, errors };
    };

    for (const [lang, theme] of [["en", "light"], ["zh", "dark"]]) {
      const w = WORDS[lang];

      await t.test(lang + ": a page that never chose is 1200 wide, as before", async () => {
        const { page, posts, errors } = await open(lang, theme, 0, 2560);
        for (const v of VIEWS) {
          await visit(page, v);
          const b = await box(page, v);
          assert.ok(b.width <= 1200 && b.width >= 1150, `${v}: ${b.width}`);
        }
        assert.deepEqual(posts, []);
        assert.deepEqual(errors, []);
      });

      await t.test(lang + ": each pick takes effect at once, centred, and posts alone", async () => {
        const { page, posts, errors } = await open(lang, theme, 0, 2560);
        await visit(page, "settings");
        const row = page.locator("#pageWidthRow");
        assert.match(await row.innerText(), new RegExp(w.row));
        // the content's width is settled when the view's padding is: wait on that, not on the variable;
        // a page long enough for a scroll bar has that bar's width less inside, hence the 20px
        const settled = (n) => page.waitForFunction((n) => {
          const e = document.querySelector("#view-settings"), cs = getComputedStyle(e);
          return Math.abs(e.clientWidth - parseFloat(cs.paddingLeft) - parseFloat(cs.paddingRight) - n) <= 20;
        }, n);
        for (const [label, want] of [["1600", 1600], ["2000", 2000]]) {
          await row.getByRole("button", { name: label, exact: true }).click();
          await settled(want);
          const b = await box(page, "settings");
          assert.ok(Math.abs(b.width - want) <= 20, `${label}: ${b.width}`);
          assert.ok(Math.abs(b.pl - b.pr) <= 1, `${label}: centred (${b.pl} / ${b.pr})`);
          await visit(page, "usage");
          assert.ok(Math.abs((await box(page, "usage")).width - want) <= 20, `usage at ${label}`);
          await visit(page, "settings");
        }
        // the whole window: the content is the window less the gutters
        await row.getByRole("button", { name: w.full, exact: true }).click();
        await page.waitForFunction(() => parseFloat(getComputedStyle(document.documentElement).getPropertyValue("--page-half")) > 5000);
        for (const v of VIEWS) {
          await visit(page, v);
          const b = await box(page, v);
          assert.equal(b.pl, 16, `${v}: the gutter`);
          assert.ok(b.view - 32 - b.width <= 2, `${v}: ${b.width} of ${b.view}`);
        }
        assert.deepEqual(posts, [1600, 2000, -1]);
        assert.deepEqual(errors, []);
      });

      await t.test(lang + ": it is there before the first paint after a reload, and a wider pick than the window changes nothing", async () => {
        const { page, errors } = await open(lang, theme, 1600, 2560);
        assert.equal(await page.evaluate(() => getComputedStyle(document.documentElement).getPropertyValue("--page-half").trim()), "800px");
        await visit(page, "agents");
        assert.ok(Math.abs((await box(page, "agents")).width - 1600) <= 20);
        await page.setViewportSize({ width: 900, height: 900 });
        const narrow = await box(page, "agents");
        assert.equal(narrow.pl, 16, "a narrow window keeps its gutter");
        assert.equal(narrow.width, narrow.view - 32);
        assert.deepEqual(errors, []);
      });

      await t.test(lang + ": a save of the other settings leaves the width as it is", async () => {
        const { page, errors } = await open(lang, theme, 2000, 2560);
        await visit(page, "settings");
        await page.locator("#themeSegs .opt").nth(1).click();
        await page.waitForTimeout(300);
        assert.equal(await page.evaluate(() => getComputedStyle(document.documentElement).getPropertyValue("--page-half").trim()), "1000px");
        assert.ok(await page.locator("#pageWidthSegs .opt.on").innerText().then((x) => x.includes("2000")));
        assert.deepEqual(errors, []);
      });
    }
  });
}
