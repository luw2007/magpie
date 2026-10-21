// Usage sources and pools are independent of provider credentials.
let usageConfig = { sources: [], pools: [] };
let usageConfigEditing = false;
async function usageConfigRequest(path, method = "GET", body) {
  const response = await fetch("/api/usage/" + path, { method, headers: { "Content-Type": "application/json" }, body: body === undefined ? undefined : JSON.stringify(body) });
  if (!response.ok) { const text = await response.text(); throw new Error(text); }
  return response.status === 204 ? null : response.json();
}
async function loadUsageConfig() {
  if (usageConfigEditing) return;
  try {
    const [sources, pools] = await Promise.all([usageConfigRequest("sources"), usageConfigRequest("pools")]);
    usageConfig = { sources: sources || [], pools: pools || [] }; renderUsageConfig();
  } catch (error) { const box = document.getElementById("usageConfig"); if (box) box.textContent = error.message; }
}
function usageConfigButton(label, action) {
  const b = el("button", "text", label); b.type = "button";
  b.onclick = async () => { try { await action(); } catch (error) { alert(error.message); } }; return b;
}
function renderUsageConfig() {
  const box = document.getElementById("usageConfig"); if (!box) return;
  box.replaceChildren(el("h3", "usage-config-title", t("Usage sources and quota pools")));
  box.append(el("p", "hint", t("Pools use the maximum applicable account usage, never a sum. Missing usage is unknown, not zero. Separate allowance windows are retained.")));
  const group = (title, items, addLabel, addAction) => {
    const section = el("section", "usage-config-group");
    const head = el("div", "usage-config-head");
    head.append(el("h4", "", t(title)), el("span", "hint", String(items.length)), usageConfigButton(t(addLabel), addAction));
    const grid = el("div", "sess-grid usage-config-grid");
    if (!items.length) grid.append(el("div", "sess-card sess-none", t("None")));
    section.append(head, grid); box.append(section); return grid;
  };
  const sources = group("Usage sources", usageConfig.sources, "Add source", () => editUsageSource());
  for (const s of usageConfig.sources) {
    const card = el("article", "sess-card usage-config-card");
    card.append(el("strong", "usage-config-name", s.name || s.id), el("p", "hint", `${s.type} · ${s.off ? t("Disabled") : t("Enabled")}`));
    const actions = el("div", "usage-config-actions");
    actions.append(usageConfigButton(t("Edit"), () => editUsageSource(s)), usageConfigButton(t("Discover accounts"), async () => {
      const accounts = await usageConfigRequest(`sources/${encodeURIComponent(s.id)}/discover`, "POST", {});
      editUsagePool({ sourceRef: s.id, accountIDs: [] }, accounts || []);
    }), usageConfigButton(t("Delete"), async () => { if (confirm(`${t("Delete source")} ${s.name || s.id}?`)) { await usageConfigRequest(`sources/${encodeURIComponent(s.id)}`, "DELETE"); await loadUsageConfig(); } }));
    card.append(actions); sources.append(card);
  }
  const pools = group("Quota pools", usageConfig.pools, "Add pool", () => editUsagePool());
  for (const p of usageConfig.pools) {
    const card = el("article", "sess-card usage-config-card");
    const source = usageConfig.sources.find(s => s.id === p.sourceRef);
    card.append(el("strong", "usage-config-name", p.name || p.id), el("p", "hint", t(p.available ? "Usage available" : "Usage unknown / unavailable")), el("p", "hint", source?.name || p.sourceRef || t("None")));
    const details = el("details", "usage-config-details");
    details.append(el("summary", "", t("Details")));
    const list = el("dl");
    for (const [label, value] of [["Pool", p.id], ["Source", p.sourceRef], ["Accounts", (p.accountIDs || []).join(", ")], ["Keys", (p.keys || []).join(", ")]]) list.append(el("dt", "", t(label)), el("dd", "", value || t("None")));
    details.append(list); card.append(details);
    const actions = el("div", "usage-config-actions");
    actions.append(usageConfigButton(t("Edit"), () => editUsagePool(p)), usageConfigButton(t("Delete"), async () => { if (confirm(`${t("Delete pool")} ${p.name || p.id}?`)) { await usageConfigRequest(`pools/${encodeURIComponent(p.id)}`, "DELETE"); await loadUsageConfig(); } }));
    card.append(actions); pools.append(card);
  }
}
function usageConfigForm(title) {
  const root = document.getElementById("usageConfig");
  const box = el("div", "sess-card usage-config-editor");
  box.append(el("h3", "usage-config-title", title)); root.replaceChildren(box);
  usageConfigEditing = true;
  const values = {};
  const add = (name, value = "", type = "text") => { const i = input(value, name, type); values[name] = i; box.append(...field(name, i)); return i; };
  return { box, values, add };
}
function editUsageSource(source = {}) {
  const { box, values, add } = usageConfigForm(source.id ? t("Edit usage source") : t("Add usage source"));
  for (const key of ["id", "name", "type", "baseURL", "credentialEnv", "envFile", "authIndex", "project", "weeklyModel", "loadModel", "command"]) add(key, source[key] ?? "");
  values.id.disabled = !!source.id;
  add("credential", "", "password").placeholder = source.credential ? t("Stored credential (leave blank to keep)") : t("Credential");
  const clear = add("clearCredential", "", "checkbox");
  const off = add("off", "", "checkbox"); off.checked = !!source.off;
  box.append(usageConfigButton(t("Save"), async () => {
    const data = {}; for (const [key, i] of Object.entries(values)) data[key] = i.type === "checkbox" ? i.checked : i.value;
    await usageConfigRequest(source.id ? `sources/${encodeURIComponent(source.id)}` : "sources", source.id ? "PUT" : "POST", data); usageConfigEditing = false; await loadUsageConfig();
  }), usageConfigButton(t("Cancel"), () => { usageConfigEditing = false; return loadUsageConfig(); }));
}
async function editUsagePool(pool = {}, discovered) {
  const { box, values, add } = usageConfigForm(pool.id ? t("Edit quota pool") : t("Add quota pool"));
  add("id", pool.id || "").disabled = !!pool.id; add("name", pool.name || "");
  const source = el("select"); for (const s of usageConfig.sources) { const option = el("option", "", s.name || s.id); option.value = s.id; source.append(option); } source.value = pool.sourceRef || usageConfig.sources[0]?.id || ""; box.append(...field(t("Source"), source));
  const accounts = el("div"); box.append(...field(t("Accounts"), accounts));
  let selected = new Set(pool.accountIDs || []);
  const paint = (items) => { accounts.replaceChildren(); const ids = new Set(items.map(a => a.id)); for (const id of selected) if (!ids.has(id)) items.push({ id, name: t("Saved account") }); for (const a of items) { const label = el("label", "row"); const tick = input("", "", "checkbox"); tick.checked = selected.has(a.id); tick.onchange = () => tick.checked ? selected.add(a.id) : selected.delete(a.id); label.append(tick, el("span", "", `${a.name || a.id} · ${a.id} ${a.platform || ""} ${a.type || ""}`)); accounts.append(label); } };
  paint(discovered || [...selected].map(id => ({ id, name: t("Saved account") })));
  source.onchange = () => { selected = new Set(); paint([]); };
  box.append(usageConfigButton(t("Discover accounts"), async () => { paint(await usageConfigRequest(`sources/${encodeURIComponent(source.value)}/discover`, "POST", {}) || []); }), usageConfigButton(t("Save"), async () => {
    await usageConfigRequest(pool.id ? `pools/${encodeURIComponent(pool.id)}` : "pools", pool.id ? "PUT" : "POST", { id: values.id.value, name: values.name.value, sourceRef: source.value, accountIDs: [...selected] }); usageConfigEditing = false; await loadUsageConfig();
  }), usageConfigButton(t("Cancel"), () => { usageConfigEditing = false; return loadUsageConfig(); }));
}

// renderKeyBinding — self-contained compact block (no pair/row reuse that
// would stretch checkbox rows and inject heavy dividers between every pick).
function renderKeyBinding(p, k) {
  const box = el("div", "kbind");
  // Staged in the editor's draft (draft.keyBindings, by key id) and saved
  // with its Save, as the rest of the editor is; Cancel drops them.
  const staged = draft?.keyBindings?.[k.id];
  const selected = new Set(staged ? staged.poolRefs : k.poolRefs || []);
  const available = new Set(usageConfig.pools.map(pool => pool.id));
  const choices = [...usageConfig.pools, ...[...selected].filter(id => !available.has(id)).map(id => ({ id, name: id, unavailable: true }))];
  const modelsOf = () => modelsInput.value.split(",").map(s => s.trim()).filter(Boolean);
  const stage = () => {
    if (!draft) return;
    // "*" alone is every model, which the provider stores as no list at all
    const models = modelsOf().filter((m) => m !== "*"), now = [...selected];
    const same = (a, b) => a.length === b.length && a.every((x, i) => x === b[i]);
    if (same([...now].sort(), [...(k.poolRefs || [])].sort()) && same(models, k.models || [])) {
      if (draft.keyBindings) delete draft.keyBindings[k.id];
    } else {
      draft.keyBindings = draft.keyBindings || {};
      draft.keyBindings[k.id] = { poolRefs: now, models };
    }
  };

  // Pool section header with hint.
  const poolsSection = el("section", "kbind-section");
  poolsSection.append(el("h4", "", t("Quota pools")));
  poolsSection.append(el("p", "hint", t("Pick every pool this key may draw from. Usage is the maximum across picked pools for each window, never a sum. Saved with the editor's Save.")));

  const poolsWrap = el("div", "kbind-pools");
  for (const pool of choices) {
    // gone off the configuration, or on it with no usage of its own: named
    // with why, and kept, for a key still set to it
    const why = !available.has(pool.id) || pool.available === false;
    const label = el("label", "kbind-chip" + (why ? " unavailable" : ""));
    const tick = input("", "", "checkbox");
    const mark = () => label.classList.toggle("on", tick.checked); // a class, not :has(), which Safari before 15.4 lacks
    tick.checked = selected.has(pool.id);
    mark();
    tick.onchange = () => { mark(); tick.checked ? selected.add(pool.id) : selected.delete(pool.id); stage(); };
    const meta = el("span", "kbind-meta");
    meta.textContent = (pool.name || pool.id) + (why ? " · " + t("unavailable") : "");
    label.append(tick, meta);
    poolsWrap.append(label);
  }
  if (!choices.length) poolsWrap.append(el("span", "hint", t("No quota pools configured yet — add one on the Usage page.")));
  poolsSection.append(poolsWrap);
  box.append(poolsSection);

  // Model scope section.
  const modelsSection = el("section", "kbind-section");
  modelsSection.append(el("h4", "", t("Model scope")));
  const modelWrap = el("div", "kbind-models");
  const modelsInput = input((staged ? (staged.models.length ? staged.models : ["*"]) : (k.models?.length ? k.models : ["*"])).join(", "), t("* = any model; comma-separated IDs, e.g. gpt-5.1,gpt-5.1-mini"));
  modelsInput.oninput = stage;
  modelWrap.append(modelsInput);
  modelsSection.append(modelWrap);
  box.append(modelsSection);
  return box;
}
loadUsageConfig();
