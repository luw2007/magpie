// The Routing page shows one pane at a time (#rtPages): the live stage, the
// request history, the routing groups, or the several-accounts pools. Pick
// the pane by its button's place, so the helper reads the same in English and
// Chinese, and wait for it to be the one showing.
const PANES = ["live", "requests", "groups", "pools"];

async function openPane(page, name) {
  const i = PANES.indexOf(name);
  if (i < 0) throw new Error("no routing pane " + name);
  const button = page.locator("#rtPages .opt").nth(i);
  await button.waitFor();
  if ((await button.getAttribute("aria-pressed")) !== "true") await button.click();
  await page.waitForFunction((n) => document.querySelectorAll("#rtPages .opt")[n]?.getAttribute("aria-pressed") === "true", i);
}

// Which pane is on, and which panes' content the reader can see.
const shownPane = (page) => page.evaluate(() => {
  const on = [...document.querySelectorAll("#rtPages .opt")].findIndex((b) => b.getAttribute("aria-pressed") === "true");
  const seen = (sel) => { const e = document.querySelector(sel); return !!e && !e.closest("[hidden]") && e.getClientRects().length > 0; };
  return { on, live: seen("#rt"), requests: seen(".rt-cols"), groups: seen(".rt-groups"), pools: seen(".rt-pools") };
});

module.exports = { openPane, shownPane, PANES };
