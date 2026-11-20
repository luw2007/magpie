// Routing keeps only its live and request panes. Groups and provider pools
// have their own page; the legacy names below open that page so older focused
// tests still exercise the same editor without putting it back in Routing.
const PANES = ["live", "requests"];

async function openPane(page, name) {
  if (name === "groups" || name === "pools") {
    const opened = await page.evaluate(async () => {
      const ok = await window.show("groups");
      return { ok, groups: !document.querySelector("#view-groups")?.hidden };
    });
    if (opened.ok && opened.groups) {
      await page.locator("#view-groups").waitFor({ state: "visible" });
      await page.locator(name === "groups" ? ".rt-groups" : ".rt-pools").waitFor({ state: "visible" });
      return;
    }
    await openPane(page, "requests");
    return;
  }
  const opened = await page.evaluate(async () => {
    const view = document.querySelector("#view-routing");
    return view && !view.hidden ? true : await window.show("routing");
  });
  if (!opened) throw new Error("could not open Routing");
  await page.locator("#view-routing").waitFor({ state: "visible" });
  const i = PANES.indexOf(name);
  if (i < 0) throw new Error("no routing pane " + name);
  const button = page.locator("#rtPages .opt").nth(i);
  await button.waitFor();
  if ((await button.getAttribute("aria-pressed")) !== "true") await button.click();
  await page.waitForFunction((n) => document.querySelectorAll("#rtPages .opt")[n]?.getAttribute("aria-pressed") === "true", i);
}

// Which pane is on, and which panes' content the reader can see. With the
// Groups page showing, no Routing pane is on, though Routing keeps the
// subpage it was left on.
const shownPane = (page) => page.evaluate(() => {
  const routing = document.querySelector("#view-routing");
  const on = routing && !routing.hidden ? [...document.querySelectorAll("#rtPages .opt")].findIndex((b) => b.getAttribute("aria-pressed") === "true") : -1;
  const seen = (sel) => { const e = document.querySelector(sel); return !!e && !e.closest("[hidden]") && e.getClientRects().length > 0; };
  return { on, live: seen("#rt"), requests: seen(".rt-cols"), groups: seen(".rt-groups"), pools: seen(".rt-pools") };
});

module.exports = { openPane, shownPane, PANES };
