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
  box.replaceChildren(el("h3", "", t("Usage sources and quota pools")));
  box.append(el("p", "hint", t("Pools use the maximum applicable account usage, never a sum. Missing usage is unknown, not zero. Separate allowance windows are retained.")));
  box.append(usageConfigButton(t("Add source"), () => editUsageSource()), usageConfigButton(t("Add pool"), () => editUsagePool()));
  for (const s of usageConfig.sources) {
    const row = el("div", "row"); row.append(el("span", "grow", `${s.name || s.id} · ${s.type} · ${s.off ? t("Disabled") : t("Enabled")}`));
    row.append(usageConfigButton(t("Edit"), () => editUsageSource(s)), usageConfigButton(t("Discover accounts"), async () => {
      const accounts = await usageConfigRequest(`sources/${encodeURIComponent(s.id)}/discover`, "POST", {});
      editUsagePool({ sourceRef: s.id, accountIDs: [] }, accounts || []);
    }), usageConfigButton(t("Delete"), async () => { if (confirm(`${t("Delete source")} ${s.name || s.id}?`)) { await usageConfigRequest(`sources/${encodeURIComponent(s.id)}`, "DELETE"); await loadUsageConfig(); } })); box.append(row);
  }
  for (const p of usageConfig.pools) {
    const row = el("div", "row"); const info = el("div", "grow");
    const source = usageConfig.sources.find(s => s.id === p.sourceRef);
    const sourceLabel = source?.name ? `${source.name} (${p.sourceRef})` : p.sourceRef;
    info.append(el("div", "", `${p.name || p.id} · ${p.available ? t("Usage available") : t("Usage unknown / unavailable")}`),
      el("div", "hint", `${t("Pool")}: ${p.id} · ${t("Source")}: ${sourceLabel} · ${t("Accounts")}: ${(p.accountIDs || []).join(", ") || t("None")} · ${t("Keys")}: ${(p.keys || []).join(", ") || t("None")}`));
    if (p.available) { const windows = el("pre", "hint"); windows.textContent = JSON.stringify(p.allowance, null, 2); info.append(windows); }
    row.append(info, usageConfigButton(t("Edit"), () => editUsagePool(p)), usageConfigButton(t("Delete"), async () => { if (confirm(`${t("Delete pool")} ${p.name || p.id}?`)) { await usageConfigRequest(`pools/${encodeURIComponent(p.id)}`, "DELETE"); await loadUsageConfig(); } })); box.append(row);
  }
}
function usageConfigForm(title) {
  const box = document.getElementById("usageConfig"); box.replaceChildren(el("h3", "", title));
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
  paint(discovered || [...selected].map(id => ({ id })));
  source.onchange = () => { selected = new Set(); paint([]); };
  box.append(usageConfigButton(t("Discover accounts"), async () => { paint(await usageConfigRequest(`sources/${encodeURIComponent(source.value)}/discover`, "POST", {}) || []); }), usageConfigButton(t("Save"), async () => {
    await usageConfigRequest(pool.id ? `pools/${encodeURIComponent(pool.id)}` : "pools", pool.id ? "PUT" : "POST", { id: values.id.value, name: values.name.value, sourceRef: source.value, accountIDs: [...selected] }); usageConfigEditing = false; await loadUsageConfig();
  }), usageConfigButton(t("Cancel"), () => { usageConfigEditing = false; return loadUsageConfig(); }));
}

// renderKeyBinding — self-contained compact block (no pair/row reuse that
// would stretch checkbox rows and inject heavy dividers between every pick).
function renderKeyBinding(p, k) {
  const box = el("div", "kbind");
  const selected = new Set(k.poolRefs || []);
  const available = new Set(usageConfig.pools.map(pool => pool.id));
  const choices = [...usageConfig.pools, ...[...selected].filter(id => !available.has(id)).map(id => ({ id, name: id, unavailable: true }))];

  // Pool section header with hint.
  const poolsSection = el("section", "kbind-section");
  poolsSection.append(el("h4", "", t("Quota pools")));
  poolsSection.append(el("p", "hint", t("Pick every pool this key may draw from. Usage is the maximum across picked pools for each window, never a sum.")));

  const poolsWrap = el("div", "kbind-pools");
  for (const pool of choices) {
    const source = usageConfig.sources.find(s => s.id === pool.sourceRef);
    const srcLabel = pool.sourceRef ? (source?.name ? `${source.name} · ${pool.sourceRef}` : pool.sourceRef) : t("no source");
    const label = el("label", "kbind-chip" + (pool.unavailable ? " unavailable" : ""));
    const tick = input("", "", "checkbox");
    tick.checked = selected.has(pool.id);
    tick.onchange = () => tick.checked ? selected.add(pool.id) : selected.delete(pool.id);
    const meta = el("span", "kbind-meta");
    meta.textContent = (pool.name || pool.id) + " · " + (available.has(pool.id) ? srcLabel : t("unavailable"));
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
  const modelsInput = input((k.models?.length ? k.models : ["*"]).join(", "), t("* = any model; comma-separated IDs, e.g. gpt-5.1,gpt-5.1-mini"));
  modelWrap.append(modelsInput);
  modelsSection.append(modelWrap);
  box.append(modelsSection);

  // Save footer: a dedicated bar, not another flex row.
  const footer = el("div", "kbind-foot");
  footer.append(usageConfigButton(t("Save binding"), () => accountAction("keys/binding", { id: p.id, ref: k.id, poolRefs: [...selected], models: modelsInput.value.split(",").map(s => s.trim()).filter(Boolean) }, t("Binding saved"))));
  box.append(footer);
  return box;
}
loadUsageConfig();
