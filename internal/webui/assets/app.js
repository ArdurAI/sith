"use strict";

const csrfToken = document.querySelector('meta[name="sith-csrf-token"]').content;
const desktopHydrationFailure = "live cache refresh stopped; re-import the folder or restart Sith";
const state = {
  meta: null,
  snapshot: null,
  lens: "Pod",
  scope: "",
  query: "",
  correlate: false,
  selected: null,
  logAbort: null,
  viewKey: "",
};

const dom = Object.fromEntries([
  "coverage-count", "coverage-detail", "lens-switcher", "fleet-search", "search-label",
  "query-mode", "context-list", "board-heading", "board-kicker", "result-count", "fleet-rows",
  "empty-state", "coverage-line", "inspector-empty", "inspector-content", "inspector-kind",
  "inspector-name", "inspector-address", "inspector-facts", "operation-grid", "refresh-button",
  "forwards-button", "forward-count", "import-folder-button", "toast-region", "action-dialog", "dialog-title",
  "dialog-kicker", "dialog-body", "dialog-actions", "dialog-close", "loading-template",
].map((id) => [id, document.getElementById(id)]));

async function api(path, options = {}) {
  const headers = new Headers(options.headers || {});
  headers.set("X-Sith-CSRF", csrfToken);
  if (options.body && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
  const response = await fetch(path, {...options, headers});
  if (!response.ok) {
    let message = `${response.status} ${response.statusText}`;
    try { message = (await response.json()).error || message; } catch (_) { /* response was not JSON */ }
    throw new Error(message);
  }
  return response;
}

function node(tag, className = "", text = "") {
  const element = document.createElement(tag);
  if (className) element.className = className;
  if (text !== "") element.textContent = text;
  return element;
}

function replaceChildren(parent, children = []) { parent.replaceChildren(...children); }

function ageLabel(value) {
  if (!value || value.startsWith("0001-")) return "never";
  const seconds = Math.max(0, Math.floor((Date.now() - new Date(value).getTime()) / 1000));
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
  return `${Math.floor(seconds / 3600)}h`;
}

function coverageText(coverage = {}) {
  const requested = coverage.requested || 0;
  const reachable = coverage.reachable || 0;
  const parts = [`covered ${reachable}/${requested} clusters`];
  if ((coverage.stale || []).length) parts.push(`${coverage.stale.length} stale (${coverage.stale.join(", ")})`);
  if ((coverage.unreachable || []).length) parts.push(`${coverage.unreachable.length} unreachable (${coverage.unreachable.join(", ")})`);
  return parts.join(" · ");
}

function statusTone(status = "") {
  const normalized = status.toLowerCase();
  if (["running", "ready", "healthy", "normal", "succeeded"].some((word) => normalized.includes(word))) return "good";
  if (["progress", "pending", "unknown"].some((word) => normalized.includes(word))) return "warn";
  if (["fail", "error", "crash", "degraded", "notready", "backoff"].some((word) => normalized.includes(word))) return "bad";
  return "muted";
}

function renderMeta() {
  replaceChildren(dom["lens-switcher"]);
  for (const lens of state.meta.lenses) {
    const button = node("button", "", `${lens}s`);
    button.type = "button";
    button.setAttribute("role", "listitem");
    button.setAttribute("aria-current", String(state.lens === lens));
    button.addEventListener("click", () => { state.lens = lens; state.selected = null; loadSnapshot(); renderMeta(); });
    dom["lens-switcher"].append(button);
  }
}

function renderSnapshot() {
  const snapshot = state.snapshot;
  const coverage = snapshot.coverage || {};
  dom["coverage-count"].textContent = `${coverage.reachable || 0} of ${coverage.requested || 0} contexts answering`;
  const coverageDetail = snapshot.state === "offline" ? "Offline — last-known fleet remains visible." : coverageText(coverage);
  const safeDesktopFailure = snapshot.last_error === desktopHydrationFailure ? snapshot.last_error : "";
  dom["coverage-detail"].textContent = safeDesktopFailure ? `${coverageDetail} · ${safeDesktopFailure}` : coverageDetail;
  dom["coverage-line"].textContent = coverageText(coverage);
  dom["board-heading"].textContent = state.correlate || state.query ? "Fleet results" : `${state.lens}s`;
  dom["board-kicker"].textContent = state.correlate ? "Correlation answer" : state.query ? "Filtered cache" : "Aggregated lens";
  dom["result-count"].textContent = `${snapshot.records.length} cached row${snapshot.records.length === 1 ? "" : "s"}`;
  renderContexts(snapshot.scopes || [], coverage, snapshot.diagnostics || []);
  renderRows(snapshot.records || []);
  if (state.selected) {
    const identity = recordIdentity(state.selected);
    state.selected = snapshot.records.find((record) => recordIdentity(record) === identity) || null;
  }
  renderInspector();
}

function renderContexts(scopes, coverage, diagnostics) {
  const entries = [];
  const all = node("button", "context-node");
  all.type = "button";
  all.setAttribute("aria-current", String(!state.scope));
  all.dataset.state = (coverage.unreachable || []).length ? "stale" : "fresh";
  all.append(contextLabel("All contexts", compactCoverage(coverage)));
  all.addEventListener("click", () => { state.scope = ""; loadSnapshot(); });
  entries.push(all);
  const grouped = new Map();
  for (const scope of scopes) {
    const origin = scope.origin || "";
    if (!grouped.has(origin)) grouped.set(origin, []);
    grouped.get(origin).push(scope);
  }
  for (const [origin, members] of grouped) {
    if (origin) {
      const sourceScopes = members.map((scope) => scope.name).join(",");
      const source = node("button", "context-node context-source");
      source.type = "button";
      source.setAttribute("aria-current", String(state.scope === sourceScopes));
      source.append(contextLabel(origin, `${members.length} imported context${members.length === 1 ? "" : "s"}`));
      source.addEventListener("click", () => { state.scope = state.scope === sourceScopes ? "" : sourceScopes; state.selected = null; loadSnapshot(); });
      entries.push(source);
    }
    for (const scope of members) {
    const button = node("button", "context-node");
    button.type = "button";
    const isDown = !scope.reachable;
    const isStale = (coverage.stale || []).includes(scope.name);
    button.dataset.state = isDown ? "down" : isStale ? "stale" : "fresh";
    button.setAttribute("aria-current", String(state.scope === scope.name));
    button.append(contextLabel(scope.display_name || scope.name, `${isDown ? "unreachable" : isStale ? "stale" : "reachable"} · ${ageLabel(scope.observed_at)}`));
    button.addEventListener("click", () => { state.scope = state.scope === scope.name ? "" : scope.name; loadSnapshot(); });
    entries.push(button);
  }
  }
  for (const diagnostic of diagnostics) {
    const source = diagnostic.source ? `${diagnostic.source}: ` : "";
    entries.push(node("div", "context-diagnostic", `Import warning — ${source}${diagnostic.message}`));
  }
  replaceChildren(dom["context-list"], entries);
}

function compactCoverage(coverage) {
  return `${coverage.reachable || 0}/${coverage.requested || 0} covered · ${(coverage.stale || []).length} stale · ${(coverage.unreachable || []).length} down`;
}

function contextLabel(name, detail) {
  const wrapper = node("span");
  wrapper.append(node("strong", "", name), node("small", "", detail));
  return wrapper;
}

function renderRows(records) {
  const rows = records.map((record) => {
    const row = node("tr");
    row.tabIndex = 0;
    row.setAttribute("aria-selected", String(state.selected && recordIdentity(state.selected) === recordIdentity(record)));
    row.append(
      cell(record.cluster, "cell-context"), cell(record.namespace || "—"), cell(record.name),
      cell(record.ready || "—"), statusCell(record.status || record.reason || "Unknown"),
      cell(String(record.restarts || 0), "cell-restarts"), cell(ageLabel(record.observed_at), "cell-age"),
    );
    const select = () => { state.selected = record; renderRows(records); renderInspector(); };
    row.addEventListener("click", select);
    row.addEventListener("keydown", (event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); select(); } });
    return row;
  });
  replaceChildren(dom["fleet-rows"], rows);
  dom["empty-state"].hidden = records.length !== 0;
}

function cell(text, className = "") { return node("td", className, text); }
function statusCell(text) {
  const td = node("td");
  const status = node("span", "status-pill", text);
  status.dataset.tone = statusTone(text);
  td.append(status);
  return td;
}
function recordIdentity(record) { return [record.cluster, record.kind, record.namespace, record.name].join("/"); }

function renderInspector() {
  const record = state.selected;
  dom["inspector-empty"].hidden = Boolean(record);
  dom["inspector-content"].hidden = !record;
  if (!record) return;
  dom["inspector-kind"].textContent = record.kind;
  dom["inspector-name"].textContent = record.name;
  dom["inspector-address"].textContent = `${record.cluster} / ${record.namespace || "cluster-scoped"}`;
  const facts = [
    ["Status", record.status || record.reason || "Unknown"], ["Ready", record.ready || "—"],
    ["Restarts", String(record.restarts || 0)], ["Node", record.node || "—"],
    ["Observed", ageLabel(record.observed_at)], ["Image", (record.images || []).join(", ") || "—"],
  ];
  replaceChildren(dom["inspector-facts"], facts.map(([label, value]) => {
    const wrapper = node("div"); wrapper.append(node("dt", "", label), node("dd", "", value)); return wrapper;
  }));
  const operations = [
    ["Describe", () => showObject(true, false)], ["View YAML", () => showObject(false, false)],
  ];
  if (record.kind === "Pod") operations.push(["Follow logs", showLogs], ["Run command", showExec], ["Forward port", showForward]);
  if (record.kind === "Service") operations.push(["Forward port", showForward]);
  operations.push(["Edit YAML", () => showEdit(false)]);
  replaceChildren(dom["operation-grid"], operations.map(([label, action]) => {
    const button = node("button", "", label); button.type = "button"; button.addEventListener("click", action); return button;
  }));
}

function selectedTarget() {
  const record = state.selected;
  return {context: record.cluster, namespace: record.namespace, kind: record.kind, name: record.name};
}

function targetQuery(target) {
  return new URLSearchParams({context: target.context, namespace: target.namespace || "", kind: target.kind, name: target.name});
}

function openDialog(title, kicker = "Local operation") {
  dom["dialog-title"].textContent = title;
  dom["dialog-kicker"].textContent = kicker;
  replaceChildren(dom["dialog-body"], [dom["loading-template"].content.cloneNode(true)]);
  replaceChildren(dom["dialog-actions"]);
  if (!dom["action-dialog"].open) dom["action-dialog"].showModal();
}

function dialogButton(label, action, primary = false) {
  const button = node("button", "", label); button.type = "button"; button.dataset.primary = String(primary); button.addEventListener("click", action); return button;
}

function showPre(content) { replaceChildren(dom["dialog-body"], [node("pre", "", content)]); }

async function showObject(describe, reveal) {
  const target = selectedTarget();
  openDialog(`${describe ? "Describe" : "YAML"} · ${target.name}`, `${target.context} / ${target.kind}`);
  try {
    const query = targetQuery(target); query.set("describe", String(describe)); query.set("reveal_secrets", String(reveal));
    const payload = await (await api(`/api/v1/object?${query}`)).json();
    if (!describe) {
      showPre(payload.yaml);
      const actions = [];
      if (target.kind === "Secret" && !reveal) actions.push(dialogButton("Reveal Secret data", () => showObject(false, true)));
      actions.push(dialogButton("Edit YAML", () => showEdit(false), true));
      replaceChildren(dom["dialog-actions"], actions);
      return;
    }
    const container = node("div");
    container.append(node("pre", "", payload.yaml));
    const events = node("div", "event-list");
    if (!payload.events.length) events.append(node("div", "event-entry", "No related events."));
    for (const event of payload.events) {
      const entry = node("div", "event-entry");
      entry.append(node("strong", "", `${event.type || "Event"} · ${event.reason || "Unknown"}`), node("span", "", event.message || "No message"));
      events.append(entry);
    }
    container.append(events); replaceChildren(dom["dialog-body"], [container]);
  } catch (error) { showDialogError(error); }
}

async function showLogs() {
  const target = selectedTarget();
  openDialog(`Logs · ${target.name}`, `${target.context} / follow`);
  const output = node("pre", "", "Opening stream…\n");
  replaceChildren(dom["dialog-body"], [output]);
  state.logAbort?.abort();
  state.logAbort = new AbortController();
  replaceChildren(dom["dialog-actions"], [dialogButton("Stop following", () => state.logAbort?.abort(), true)]);
  try {
    const query = targetQuery(target); query.set("follow", "true"); query.set("tail", "200");
    const response = await api(`/api/v1/logs?${query}`, {signal: state.logAbort.signal});
    output.textContent = "";
    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    while (true) {
      const {done, value} = await reader.read();
      if (done) break;
      output.textContent += decoder.decode(value, {stream: true});
      output.scrollTop = output.scrollHeight;
    }
  } catch (error) {
    if (error.name !== "AbortError") showDialogError(error);
  }
}

function showExec() {
  const target = selectedTarget();
  openDialog(`Run command · ${target.name}`, `${target.context} / exact argv`);
  const wrapper = node("div");
  const label = node("label", "", "Command arguments (JSON array)");
  const input = node("textarea"); input.value = '["uname", "-a"]'; input.style.minHeight = "110px"; label.append(input); wrapper.append(label);
  replaceChildren(dom["dialog-body"], [wrapper]);
  replaceChildren(dom["dialog-actions"], [dialogButton("Run command", async () => {
    try {
      const command = JSON.parse(input.value);
      if (!Array.isArray(command) || command.some((part) => typeof part !== "string")) throw new Error("Command must be a JSON array of strings.");
      replaceChildren(dom["dialog-actions"]);
      const payload = await (await api("/api/v1/exec", {method: "POST", body: JSON.stringify({target, command})})).json();
      showPre(`${payload.stdout}${payload.stderr ? `\n[stderr]\n${payload.stderr}` : ""}${payload.truncated ? "\n[output truncated]" : ""}`);
    } catch (error) { showDialogError(error); }
  }, true)]);
}

async function showEdit(revealSecret) {
  const target = selectedTarget();
  if (target.kind === "Secret" && !revealSecret) {
    openDialog(`Edit Secret · ${target.name}`, `${target.context} / explicit disclosure`);
    replaceChildren(dom["dialog-body"], [node("div", "event-entry", "Editing this Secret reveals its data in the browser until the dialog closes.")]);
    replaceChildren(dom["dialog-actions"], [dialogButton("Reveal and edit Secret", () => showEdit(true), true)]);
    return;
  }
  openDialog(`Edit YAML · ${target.name}`, `${target.context} / server-validated`);
  try {
    const query = targetQuery(target); query.set("reveal_secrets", String(target.kind === "Secret" && revealSecret));
    const payload = await (await api(`/api/v1/object?${query}`)).json();
    const textarea = node("textarea"); textarea.value = payload.yaml; textarea.setAttribute("aria-label", "Resource YAML");
    replaceChildren(dom["dialog-body"], [textarea]);
    let previewedManifest = "";
    let previewToken = "";
    const preview = dialogButton("Preview changes", async () => {
      try {
        const manifest = textarea.value;
        const result = await (await api("/api/v1/edit/preview", {method: "POST", body: JSON.stringify({target, manifest})})).json();
        previewedManifest = manifest;
        previewToken = result.preview_token;
        const diff = node("pre", "", result.diff || "No changes.");
        replaceChildren(dom["dialog-body"], [textarea, diff]);
        replaceChildren(dom["dialog-actions"], [preview, dialogButton("Apply previewed YAML", async () => {
          if (textarea.value !== previewedManifest) { toast("YAML changed after preview. Preview it again.", "error"); return; }
          try {
            await api("/api/v1/edit/apply", {method: "POST", body: JSON.stringify({target, manifest: previewedManifest, preview_token: previewToken})});
            toast(`${target.kind}/${target.name} updated in ${target.context}.`);
            dom["action-dialog"].close(); loadSnapshot();
          } catch (error) { showDialogError(error); }
        }, true)]);
      } catch (error) { showDialogError(error); }
    });
    replaceChildren(dom["dialog-actions"], [preview]);
  } catch (error) { showDialogError(error); }
}

function showForward() {
  const target = selectedTarget();
  openDialog(`Forward port · ${target.name}`, `${target.context} / loopback only`);
  const label = node("label", "", "Port mapping");
  const input = node("input"); input.value = ":8080"; input.placeholder = "LOCAL:REMOTE or :REMOTE"; label.append(input);
  replaceChildren(dom["dialog-body"], [label]);
  replaceChildren(dom["dialog-actions"], [dialogButton("Start loopback forward", async () => {
    try {
      const payload = await (await api("/api/v1/port-forwards", {method: "POST", body: JSON.stringify({target, ports: input.value.trim().split(/\s+/)})})).json();
      toast(`Forward ready: ${payload.ports.map((port) => `${port.local}→${port.remote}`).join(", ")}`);
      dom["action-dialog"].close(); refreshForwardCount();
    } catch (error) { showDialogError(error); }
  }, true)]);
}

async function showForwards() {
  openDialog("Active port-forwards", "Loopback session manager");
  try {
    const forwards = await (await api("/api/v1/port-forwards")).json();
    const list = node("div", "forward-list");
    if (!forwards.length) list.append(node("div", "forward-entry", "No active port-forwards."));
    for (const forward of forwards) {
      const entry = node("div", "forward-entry");
      entry.append(node("strong", "", `${forward.target.context} / ${forward.target.name}`), node("span", "", forward.ports.map((port) => `${port.local}→${port.remote}`).join(", ")));
      const close = dialogButton("Close forward", async () => { await api(`/api/v1/port-forwards/${encodeURIComponent(forward.id)}`, {method: "DELETE"}); showForwards(); refreshForwardCount(); });
      entry.append(close); list.append(entry);
    }
    replaceChildren(dom["dialog-body"], [list]);
  } catch (error) { showDialogError(error); }
}

function showDialogError(error) {
  const message = error instanceof Error ? error.message : String(error);
  replaceChildren(dom["dialog-body"], [node("div", "event-entry", message)]);
  replaceChildren(dom["dialog-actions"]);
  toast(message, "error");
}

function toast(message, tone = "ok") {
  const item = node("div", "toast", message); item.dataset.tone = tone; dom["toast-region"].append(item);
  setTimeout(() => item.remove(), 4800);
}

async function refreshForwardCount() {
  try { const forwards = await (await api("/api/v1/port-forwards")).json(); dom["forward-count"].textContent = String(forwards.filter((entry) => !entry.done).length); } catch (_) { /* main view reports API failures */ }
}

async function loadSnapshot() {
  const query = new URLSearchParams({kind: state.lens});
  if (state.scope) query.set("scopes", state.scope);
  if (state.query) { query.set("q", state.query); query.set("all", String(!state.correlate)); }
  if (state.correlate) query.set("correlate", "true");
  try {
    const next = await (await api(`/api/v1/snapshot?${query}`)).json();
    const viewKey = query.toString();
    if (state.snapshot && state.viewKey === viewKey && state.snapshot.version === next.version &&
        state.snapshot.state === next.state && JSON.stringify(state.snapshot.coverage) === JSON.stringify(next.coverage)) return;
    state.snapshot = next;
    state.viewKey = viewKey;
    renderSnapshot();
  } catch (error) { toast(error.message, "error"); }
}

let searchTimer = 0;
dom["fleet-search"].addEventListener("input", () => {
  clearTimeout(searchTimer); searchTimer = setTimeout(() => { state.query = dom["fleet-search"].value.trim(); state.selected = null; loadSnapshot(); }, 180);
});
dom["query-mode"].addEventListener("click", () => {
  state.correlate = !state.correlate;
  dom["query-mode"].setAttribute("aria-pressed", String(state.correlate));
  dom["query-mode"].textContent = state.correlate ? "Correlation mode" : "Search mode";
  dom["search-label"].textContent = state.correlate ? "Correlate the fleet" : "Search the fleet";
  dom["fleet-search"].placeholder = state.correlate ? "deploy/payments status!=Healthy" : "payments status:CrashLoopBackOff";
  state.selected = null; loadSnapshot();
});
dom["refresh-button"].addEventListener("click", async () => { try { await api("/api/v1/sync", {method: "POST", body: "{}"}); toast("Fleet refresh scheduled."); } catch (error) { toast(error.message, "error"); } });
dom["forwards-button"].addEventListener("click", showForwards);
const directoryPicker = window.go?.cli?.DesktopBridge?.ChooseKubeconfigDirectory;
if (typeof directoryPicker === "function") {
  dom["import-folder-button"].hidden = false;
  dom["import-folder-button"].addEventListener("click", async () => {
    try {
      if (await directoryPicker()) window.location.reload();
    } catch (error) { toast(error.message || "Unable to import folder.", "error"); }
  });
}
dom["dialog-close"].addEventListener("click", () => dom["action-dialog"].close());
dom["action-dialog"].addEventListener("close", () => { state.logAbort?.abort(); state.logAbort = null; });
document.addEventListener("keydown", (event) => {
  if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") { event.preventDefault(); dom["fleet-search"].focus(); }
});

(async function start() {
  try {
    state.meta = await (await api("/api/v1/meta")).json();
    renderMeta();
    await Promise.all([loadSnapshot(), refreshForwardCount()]);
    setInterval(loadSnapshot, 3000);
    setInterval(refreshForwardCount, 5000);
  } catch (error) { toast(`Fleet UI could not start: ${error.message}`, "error"); }
})();
