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
  box.replaceChildren(el("h3", "", "Usage sources and quota pools"));
  box.append(el("p", "hint", "Pools use the maximum applicable account usage, never a sum. Missing usage is unknown, not zero. Separate allowance windows are retained."));
  box.append(usageConfigButton("Add source", () => editUsageSource()), usageConfigButton("Add pool", () => editUsagePool()));
  for (const s of usageConfig.sources) {
    const row = el("div", "row"); row.append(el("span", "grow", `${s.name || s.id} · ${s.type} · ${s.off ? "Disabled" : "Enabled"}`));
    row.append(usageConfigButton("Edit", () => editUsageSource(s)), usageConfigButton("Discover accounts", async () => {
      const accounts = await usageConfigRequest(`sources/${encodeURIComponent(s.id)}/discover`, "POST", {});
      editUsagePool({ sourceRef: s.id, accountIDs: [] }, accounts || []);
    }), usageConfigButton("Delete", async () => { if (confirm(`Delete source ${s.name || s.id}?`)) { await usageConfigRequest(`sources/${encodeURIComponent(s.id)}`, "DELETE"); await loadUsageConfig(); } })); box.append(row);
  }
  for (const p of usageConfig.pools) {
    const row = el("div", "row"); const info = el("div", "grow");
    const source = usageConfig.sources.find(s => s.id === p.sourceRef);
    info.append(el("div", "", `${p.name || p.id} · ${p.available ? "Usage available" : "Usage unknown / unavailable"}`), el("div", "hint", `Pool: ${p.id} · Source: ${source?.name ? source.name + " (" + p.sourceRef + ")" : p.sourceRef} · Accounts: ${(p.accountIDs || []).join(", ") || "None"} · Keys: ${(p.keys || []).join(", ") || "None"}`));
    if (p.available) { const windows = el("pre", "hint"); windows.textContent = JSON.stringify(p.allowance, null, 2); info.append(windows); }
    row.append(info, usageConfigButton("Edit", () => editUsagePool(p)), usageConfigButton("Delete", async () => { if (confirm(`Delete pool ${p.name || p.id}?`)) { await usageConfigRequest(`pools/${encodeURIComponent(p.id)}`, "DELETE"); await loadUsageConfig(); } })); box.append(row);
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
  const { box, values, add } = usageConfigForm(source.id ? "Edit usage source" : "Add usage source");
  for (const key of ["id", "name", "type", "baseURL", "credentialEnv", "envFile", "authIndex", "project", "weeklyModel", "loadModel", "command"]) add(key, source[key] ?? "");
  values.id.disabled = !!source.id;
  add("credential", "", "password").placeholder = source.credential ? "Stored credential (leave blank to keep)" : "Credential";
  const clear = add("clearCredential", "", "checkbox");
  const off = add("off", "", "checkbox"); off.checked = !!source.off;
  box.append(usageConfigButton("Save", async () => {
    const data = {}; for (const [key, i] of Object.entries(values)) data[key] = i.type === "checkbox" ? i.checked : i.value;
    await usageConfigRequest(source.id ? `sources/${encodeURIComponent(source.id)}` : "sources", source.id ? "PUT" : "POST", data); usageConfigEditing = false; await loadUsageConfig();
  }), usageConfigButton("Cancel", () => { usageConfigEditing = false; return loadUsageConfig(); }));
}
async function editUsagePool(pool = {}, discovered) {
  const { box, values, add } = usageConfigForm(pool.id ? "Edit quota pool" : "Add quota pool");
  add("id", pool.id || "").disabled = !!pool.id; add("name", pool.name || "");
  const source = el("select"); for (const s of usageConfig.sources) { const option = el("option", "", s.name || s.id); option.value = s.id; source.append(option); } source.value = pool.sourceRef || usageConfig.sources[0]?.id || ""; box.append(...field("Source", source));
  const accounts = el("div"); box.append(...field("Accounts", accounts));
  let selected = new Set(pool.accountIDs || []);
  const paint = (items) => { accounts.replaceChildren(); const ids = new Set(items.map(a => a.id)); for (const id of selected) if (!ids.has(id)) items.push({ id, name: "Saved account" }); for (const a of items) { const label = el("label", "row"); const tick = input("", "", "checkbox"); tick.checked = selected.has(a.id); tick.onchange = () => tick.checked ? selected.add(a.id) : selected.delete(a.id); label.append(tick, el("span", "", `${a.name || a.id} · ${a.id} ${a.platform || ""} ${a.type || ""}`)); accounts.append(label); } };
  paint(discovered || [...selected].map(id => ({ id })));
  source.onchange = () => { selected = new Set(); paint([]); };
  box.append(usageConfigButton("Discover accounts", async () => { paint(await usageConfigRequest(`sources/${encodeURIComponent(source.value)}/discover`, "POST", {}) || []); }), usageConfigButton("Save", async () => {
    await usageConfigRequest(pool.id ? `pools/${encodeURIComponent(pool.id)}` : "pools", pool.id ? "PUT" : "POST", { id: values.id.value, name: values.name.value, sourceRef: source.value, accountIDs: [...selected] }); usageConfigEditing = false; await loadUsageConfig();
  }), usageConfigButton("Cancel", () => { usageConfigEditing = false; return loadUsageConfig(); }));
}
function renderKeyBinding(p, k) {
  const box = el("div", "pair"); const pools = el("div");
  const selected = new Set(k.poolRefs || []);
  const available = new Set(usageConfig.pools.map(pool => pool.id));
  const choices = [...usageConfig.pools, ...[...selected].filter(id => !available.has(id)).map(id => ({ id, name: id + " (unavailable)" }))];
  for (const pool of choices) {
    const label = el("label", "row"); const tick = input("", "", "checkbox");
    tick.checked = selected.has(pool.id);
    tick.onchange = () => tick.checked ? selected.add(pool.id) : selected.delete(pool.id);
    label.append(tick, el("span", "", pool.name || pool.id)); pools.append(label);
  }
  if (!choices.length) pools.append(el("span", "hint", "No quota pools"));
  const models = input((k.models?.length ? k.models : ["*"]).join(", "), "Model scope: * or comma-separated model IDs");
  box.append(pools, models, usageConfigButton("Save binding", () => accountAction("keys/binding", { id: p.id, ref: k.id, poolRefs: [...selected], models: models.value.split(",").map(s => s.trim()).filter(Boolean) })));
  return box;
}
loadUsageConfig();
