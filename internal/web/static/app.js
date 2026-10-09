"use strict";

// Polling intervals (ms).
const STATUS_INTERVAL = 15000;
const RUNS_INTERVAL = 2000;

// Strips ANSI escape sequences (colors, cursor moves) from command output.
const ANSI = /\x1b\[[0-9;?]*[ -\/]*[@-~]|\x1b\][^\x07]*(\x07|\x1b\\)/g;

// The token arrives in the URL fragment (never sent to a server) and is kept
// in localStorage, which only this origin (host and port) can read.
const TOKEN_KEY = "projector.token";

function readStoredToken() {
  try {
    return localStorage.getItem(TOKEN_KEY) || "";
  } catch {
    return "";
  }
}

function storeToken(token) {
  try {
    if (token) localStorage.setItem(TOKEN_KEY, token);
    else localStorage.removeItem(TOKEN_KEY);
  } catch {
    // Storage unavailable: the token lives only in memory for this tab.
  }
}

// takeTokenFromURL moves a #token=... fragment into storage and strips it from
// the address bar and history.
function takeTokenFromURL() {
  const match = location.hash.match(/^#token=([0-9a-f]+)$/);
  if (!match) return readStoredToken();
  storeToken(match[1]);
  history.replaceState(null, "", location.pathname);
  return match[1];
}

const state = {
  token: takeTokenFromURL(),
  projects: [],
  selected: null, // project name
  detail: null,
  runs: [],
  activeRun: null, // run id shown in the output pane
  stream: null, // AbortController of the active run's output stream
  view: "project", // middle panel: "project" or "graph"
  graph: null, // last /api/graph response
  graphKey: "", // graphStatusKey() of the last render
  mine: new Set(), // ids of runs started from this tab, for notifications
};

class AuthError extends Error {}

const $ = (id) => document.getElementById(id);

function el(tag, attrs = {}, ...children) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k === "class") node.className = v;
    else if (k === "text") node.textContent = v;
    else if (k.startsWith("on")) node.addEventListener(k.slice(2), v);
    else node.setAttribute(k, v);
  }
  for (const c of children) if (c != null) node.append(c);
  return node;
}

function badge(text, kind = "") {
  return el("span", { class: `badge ${kind}`, text });
}

function toast(msg) {
  if (state.token === "") return; // the logged-out view explains what to do
  const t = $("toast");
  t.textContent = msg;
  t.hidden = false;
  clearTimeout(toast.timer);
  toast.timer = setTimeout(() => (t.hidden = true), 6000);
}

function authHeaders(extra = {}) {
  return { ...extra, Authorization: `Bearer ${state.token}` };
}

function checkAuth(res) {
  if (res.status !== 401) return;
  storeToken("");
  state.token = "";
  showLoggedOut();
  throw new AuthError("Not signed in: open the link printed by `projector serve`.");
}

function showLoggedOut() {
  $("logged-out").hidden = false;
  $("app").hidden = true;
  $("refresh").hidden = true;
}

async function api(method, path, body) {
  const res = await fetch(path, {
    method,
    headers: authHeaders(body ? { "Content-Type": "application/json" } : {}),
    body: body ? JSON.stringify(body) : undefined,
  });
  checkAuth(res);
  const data = await res.json().catch(() => null);
  if (!res.ok) throw new Error((data && data.error) || `${res.status} ${res.statusText}`);
  return data;
}

/* ---------- Projects ---------- */

function gitBadges(p) {
  if (p.gitError) return [badge("git error", "err")];
  if (!p.git.isRepo) return [badge("no git")];
  const out = [badge(p.git.branch || "?", "info")];
  if (p.git.ahead) out.push(badge(`↑${p.git.ahead}`, "warn"));
  if (p.git.behind) out.push(badge(`↓${p.git.behind}`, "warn"));
  if (!p.git.hasUpstream) out.push(badge("no upstream"));
  if (p.git.changed) out.push(badge(`${p.git.changed} changed`, "warn"));
  if (!p.git.ahead && !p.git.behind && !p.git.changed && p.git.hasUpstream) out.push(badge("clean", "ok"));
  return out;
}

function containerInfo(p) {
  if (!p.containers) return { cls: "", text: "docker unavailable" };
  const { running, total } = p.containers;
  if (total === 0) return { cls: "", text: "no containers" };
  return { cls: running === total ? "up" : running > 0 ? "partial" : "", text: `${running}/${total} running` };
}

function onceBadge(p) {
  if (p.pendingError) return badge("config error", "err");
  return p.pending ? badge(`${p.pending} once pending`, "warn") : null;
}

function renderProjects() {
  const list = $("project-list");
  list.replaceChildren(
    ...state.projects.map((p) => {
      const c = containerInfo(p);
      const li = el(
        "li",
        {
          tabindex: "0",
          class: p.name === state.selected ? "selected" : "",
          title: p.gitError || p.pendingError || p.dir,
          onclick: () => selectProject(p.name),
          onkeydown: (e) => (e.key === "Enter" || e.key === " ") && (e.preventDefault(), selectProject(p.name)),
        },
        el("div", { class: "project-name" }, el("span", { class: `dot ${c.cls}`, title: c.text }), p.name),
        el("div", { class: "project-meta" }, ...gitBadges(p), onceBadge(p)),
      );
      return li;
    }),
  );
  if (state.projects.length === 0) {
    list.append(el("li", { class: "muted", text: "No projects registered. Use `projector project add`." }));
  }
  renderDetailStatus();
}

async function loadProjects() {
  $("refresh").disabled = true;
  try {
    state.projects = await api("GET", "/api/projects");
    $("refreshed").textContent = `updated ${new Date().toLocaleTimeString()}`;
    renderProjects();
    if (state.view === "graph") refreshGraphStatus();
    if (!state.selected && state.projects.length) {
      const wanted = decodeURIComponent(location.hash.slice(1));
      selectProject(state.projects.some((p) => p.name === wanted) ? wanted : state.projects[0].name);
    }
  } catch (e) {
    toast(e.message);
  } finally {
    $("refresh").disabled = false;
  }
}

/* ---------- Project detail ---------- */

function renderDetailStatus() {
  const p = state.projects.find((x) => x.name === state.selected);
  const line = $("detail-status");
  if (!p) return line.replaceChildren();
  const c = containerInfo(p);
  const badges = [...gitBadges(p), badge(c.text, c.cls === "up" ? "ok" : c.cls === "partial" ? "warn" : ""), onceBadge(p)];
  line.replaceChildren(...badges.filter(Boolean));
}

function renderDetail() {
  const d = state.detail;
  $("detail-empty").hidden = !!d;
  $("detail").hidden = !d;
  if (!d) return;

  $("detail-name").textContent = d.name;
  $("detail-dir").textContent = d.dir;
  renderDetailStatus();

  $("commands").replaceChildren(
    ...d.commands.map((c) => {
      const pending = c.once.filter((o) => o.pending).length;
      const tip = [c.cmd, c.dependsOn.length ? `depends on: ${c.dependsOn.join(", ")}` : "", c.aliases.length ? `aliases: ${c.aliases.join(", ")}` : "", ...c.once.map((o) => `once ${o.pending ? "(pending) " : ""}${o.id}: ${o.cmd}`)]
        .filter(Boolean)
        .join("\n");
      return el(
        "button",
        { type: "button", class: "command", title: tip, onclick: () => startRun(d.name, c.name) },
        el("span", { class: "cmd-name" }, c.name, pending ? badge(`${pending} once`, "warn") : null),
        c.description ? el("span", { class: "cmd-desc", text: c.description }) : null,
        el("code", { class: "cmd-line", text: c.cmd || "(no command)" }),
      );
    }),
  );

  $("info-section").hidden = !d.hasInfo;
  $("info").replaceChildren();
}

async function selectProject(name) {
  state.selected = name;
  if (state.view === "graph") renderGraph();
  if (state.view === "project") history.replaceState(null, "", `#${encodeURIComponent(name)}`);
  renderProjects();
  try {
    state.detail = await api("GET", `/api/projects/${encodeURIComponent(name)}`);
  } catch (e) {
    state.detail = null;
    toast(e.message);
  }
  renderDetail();
}

async function loadInfo() {
  const name = state.selected;
  const btn = $("load-info");
  btn.disabled = true;
  btn.textContent = "Loading…";
  try {
    const items = await api("GET", `/api/projects/${encodeURIComponent(name)}/info`);
    if (name !== state.selected) return;
    $("info").replaceChildren(
      ...items.map((it) =>
        el(
          "div",
          { class: "info-item" },
          el("div", { class: "info-title" }, el("strong", { text: it.name }), it.description ? el("span", { class: "muted", text: it.description }) : null, it.error ? badge(it.error, "err") : null),
          el("pre", { class: "output", text: (it.output || "").replace(ANSI, "") || "(no output)" }),
        ),
      ),
    );
  } catch (e) {
    toast(e.message);
  } finally {
    btn.disabled = false;
    btn.textContent = "Reload";
  }
}

/* ---------- Dependency graph ---------- */

const SVG_NS = "http://www.w3.org/2000/svg";

// GRAPH_HASH deep-links the graph; "=" keeps it apart from project names.
const GRAPH_HASH = "#view=graph";

function svg(tag, attrs = {}, ...children) {
  const node = document.createElementNS(SVG_NS, tag);
  for (const [k, v] of Object.entries(attrs)) node.setAttribute(k, String(v));
  for (const c of children) if (c != null) node.append(c);
  return node;
}

function showView(view) {
  state.view = view;
  $("tab-project").setAttribute("aria-selected", String(view === "project"));
  $("tab-graph").setAttribute("aria-selected", String(view === "graph"));
  $("project-view").hidden = view !== "project";
  $("graph-view").hidden = view !== "graph";
  history.replaceState(null, "", view === "graph" ? GRAPH_HASH : state.selected ? `#${encodeURIComponent(state.selected)}` : location.pathname);
  if (view === "graph") loadGraph();
}

async function loadGraph() {
  try {
    state.graph = await api("GET", "/api/graph");
  } catch (e) {
    return toast(e.message);
  }
  const select = $("graph-command");
  const commands = [...new Set(state.graph.edges.map((e) => e.fromCommand))].sort();
  // Keep the user's choice; default to "start", the usual dependency chain.
  const wanted = select.dataset.chosen ?? (commands.includes("start") ? "start" : "");
  select.replaceChildren(el("option", { value: "", text: "all commands" }), ...commands.map((c) => el("option", { value: c, text: c })));
  select.value = wanted === "" || commands.includes(wanted) ? wanted : "";
  renderGraph();
}

function nodeClass(id, missing) {
  if (missing) return "missing";
  const p = state.projects.find((x) => x.name === id);
  return p ? containerInfo(p).cls : "";
}

// graphStatusKey summarises what node borders depend on, so polling only
// redraws the graph (losing keyboard focus) when that changes.
function graphStatusKey() {
  return state.projects.map((p) => `${p.name}:${containerInfo(p).cls}`).join(",");
}

function refreshGraphStatus() {
  if (state.graphKey !== graphStatusKey()) renderGraph();
}

function renderGraph() {
  state.graphKey = graphStatusKey();
  const g = state.graph;
  if (!g) return;
  const command = $("graph-command").value || null;
  const layout = layoutGraph(g.projects, g.edges, command);
  const box = $("graph");

  if (layout.nodes.length === 0) {
    box.replaceChildren(el("p", { class: "muted", text: "No cross-project dependencies (depends_on: [^project:command]) for this command." }));
  } else {
    const arrow = svg("marker", { id: "arrow", viewBox: "0 0 10 10", refX: 9, refY: 5, markerWidth: 7, markerHeight: 7, orient: "auto-start-reverse" }, svg("path", { d: "M 0 0 L 10 5 L 0 10 z", class: "arrow" }));
    const edges = layout.edges.map((e) => {
      const related = state.selected === e.from || state.selected === e.to;
      return svg(
        "path",
        { d: `M ${e.x1} ${e.y1} C ${e.c1x} ${e.c1y}, ${e.c2x} ${e.c2y}, ${e.x2} ${e.y2}`, class: `edge${related ? " highlight" : ""}`, "marker-end": "url(#arrow)" },
        svg("title", {}, `${e.from} → ${e.to}\n${e.commands.join("\n")}`),
      );
    });
    const nodes = layout.nodes.map((n) => {
      const cls = ["node", nodeClass(n.id, n.missing), n.id === state.selected ? "selected" : ""].filter(Boolean).join(" ");
      const open = () => {
        if (n.missing) return toast(`${n.id} is not a registered project.`);
        selectProject(n.id);
        showView("project");
      };
      const group = svg(
        "g",
        { class: cls, tabindex: 0, role: "button", "aria-label": n.missing ? `${n.id} (not registered)` : `Open ${n.id}` },
        svg("title", {}, n.missing ? `${n.id}: not a registered project` : n.id),
        svg("rect", { x: n.x, y: n.y, width: n.w, height: n.h, rx: 7 }),
        svg("text", { x: n.x + n.w / 2, y: n.y + n.h / 2 }, n.id),
      );
      group.addEventListener("click", open);
      group.addEventListener("keydown", (e) => (e.key === "Enter" || e.key === " ") && (e.preventDefault(), open()));
      return group;
    });
    // Draw edges before nodes so arrows pass under the boxes.
    box.replaceChildren(svg("svg", { width: layout.width, height: layout.height, viewBox: `0 0 ${layout.width} ${layout.height}`, role: "img", "aria-label": "Cross-project dependency graph" }, svg("defs", {}, arrow), ...edges, ...nodes));
  }

  $("graph-errors").replaceChildren(...Object.entries(g.errors).map(([name, err]) => el("li", { text: `${name}: ${err}` })));
}

/* ---------- Notifications ---------- */

function askNotificationPermission() {
  if ("Notification" in window && Notification.permission === "default") {
    Notification.requestPermission().catch(() => {});
  }
}

// notifyFinished tells the user a run from this tab ended while they were
// looking elsewhere. Runs the user stopped are not announced.
function notifyFinished(run) {
  if (run.status === "cancelled" || !document.hidden) return;
  if (!("Notification" in window) || Notification.permission !== "granted") return;
  try {
    const n = new Notification(`${runLabel(run)}: ${run.status}`, { body: run.error || "projector", tag: `run-${run.id}` });
    n.onclick = () => {
      window.focus();
      showRun(run.id);
      n.close();
    };
  } catch {
    // Some platforms only allow notifications from a service worker.
  }
}

/* ---------- Runs ---------- */

const STATUS_KIND = { running: "info", succeeded: "ok", failed: "err", cancelled: "warn" };

function runLabel(r) {
  return [r.project, r.command, ...(r.args || [])].join(" ");
}

function renderRuns() {
  const list = $("run-list");
  list.replaceChildren(
    ...state.runs.map((r) =>
      el(
        "li",
        {
          tabindex: "0",
          class: r.id === state.activeRun ? "selected" : "",
          onclick: () => showRun(r.id),
          onkeydown: (e) => (e.key === "Enter" || e.key === " ") && (e.preventDefault(), showRun(r.id)),
        },
        badge(r.status, STATUS_KIND[r.status]),
        el("span", { class: "run-label", text: runLabel(r), title: r.error || runLabel(r) }),
        el("span", { class: "run-time", text: new Date(r.startedAt).toLocaleTimeString() }),
      ),
    ),
  );
  if (state.runs.length === 0) list.append(el("li", { class: "muted", text: "Click a command to run it." }));
  renderRunHead();
}

function renderRunHead() {
  const r = state.runs.find((x) => x.id === state.activeRun);
  $("run-view").hidden = !r;
  if (!r) return;
  $("run-title").textContent = runLabel(r);
  const s = $("run-status");
  s.textContent = r.error ? `${r.status}: ${r.error}` : r.status;
  s.className = `badge ${STATUS_KIND[r.status] || ""}`;
  $("stop").hidden = r.status !== "running";
}

async function loadRuns() {
  try {
    const prev = new Map(state.runs.map((r) => [r.id, r.status]));
    state.runs = await api("GET", "/api/runs");
    renderRuns();
    if (!state.activeRun && state.runs.length) showRun(state.runs[0].id);
    const justFinished = state.runs.filter((r) => prev.get(r.id) === "running" && r.status !== "running");
    justFinished.filter((r) => state.mine.has(r.id)).forEach(notifyFinished);
    // A run of the selected project finished: its once steps may have applied.
    if (justFinished.some((r) => r.project === state.selected)) {
      selectProject(state.selected);
      loadProjects();
    }
  } catch (e) {
    toast(e.message);
  }
}

function appendOutput(text, replace) {
  const out = $("output");
  const clean = text.replace(ANSI, "");
  if (replace) out.textContent = clean;
  else out.append(clean);
  if ($("follow").checked) out.scrollTop = out.scrollHeight;
}

function handleStreamEvent(name, data) {
  const value = JSON.parse(data);
  if (name === "reset") appendOutput(value, true);
  else if (name === "output") appendOutput(value, false);
  else if (name === "end") {
    state.runs = state.runs.map((r) => (r.id === value.id ? value : r));
    renderRuns();
  }
}

// streamRun reads the server-sent events of a run with fetch, since
// EventSource cannot send the Authorization header.
async function streamRun(id, signal) {
  const res = await fetch(`/api/runs/${encodeURIComponent(id)}/stream`, { headers: authHeaders(), signal });
  checkAuth(res);
  if (!res.ok) throw new Error(`stream: ${res.status} ${res.statusText}`);

  const reader = res.body.pipeThrough(new TextDecoderStream()).getReader();
  let buffer = "";
  for (;;) {
    const { value, done } = await reader.read();
    if (done) return;
    buffer += value;
    let sep;
    while ((sep = buffer.indexOf("\n\n")) >= 0) {
      const block = buffer.slice(0, sep);
      buffer = buffer.slice(sep + 2);
      let name = "message";
      let data = "";
      for (const line of block.split("\n")) {
        if (line.startsWith("event: ")) name = line.slice(7);
        else if (line.startsWith("data: ")) data = line.slice(6);
      }
      if (data) handleStreamEvent(name, data);
    }
  }
}

function showRun(id) {
  if (state.stream) state.stream.abort();
  state.activeRun = id;
  $("output").textContent = "";
  renderRuns();

  // One stream at a time: browsers allow few connections per host.
  const controller = new AbortController();
  state.stream = controller;
  streamRun(id, controller.signal).catch((e) => {
    if (e.name !== "AbortError") toast(e.message);
  });
}

// splitArgs splits a command line into words like a POSIX shell does for
// quotes and backslashes. Each word reaches the command as one literal arg.
function splitArgs(line) {
  const words = [];
  let word = "";
  let inWord = false;
  let quote = "";
  for (let i = 0; i < line.length; i++) {
    const c = line[i];
    if (quote === "'") {
      if (c === "'") quote = "";
      else word += c;
    } else if (quote === '"') {
      if (c === '"') quote = "";
      else if (c === "\\" && i + 1 < line.length && '"\\$`'.includes(line[i + 1])) word += line[++i];
      else word += c;
    } else if (c === "'" || c === '"') {
      quote = c;
      inWord = true;
    } else if (c === "\\" && i + 1 < line.length) {
      word += line[++i];
      inWord = true;
    } else if (/\s/.test(c)) {
      if (inWord) words.push(word);
      word = "";
      inWord = false;
    } else {
      word += c;
      inWord = true;
    }
  }
  if (quote) throw new Error(`Unclosed ${quote} in extra args.`);
  if (inWord) words.push(word);
  return words;
}

async function startRun(project, command) {
  askNotificationPermission(); // needs a user gesture, which this click is
  try {
    const run = await api("POST", "/api/runs", { project, command, args: splitArgs($("args").value) });
    state.mine.add(run.id);
    state.runs = [run, ...state.runs.filter((r) => r.id !== run.id)];
    showRun(run.id);
  } catch (e) {
    toast(e.message);
  }
}

async function stopRun() {
  if (!state.activeRun) return;
  try {
    await api("POST", `/api/runs/${encodeURIComponent(state.activeRun)}/stop`);
  } catch (e) {
    toast(e.message);
  }
}

/* ---------- Boot ---------- */

function boot() {
  if (!state.token) return showLoggedOut();
  $("refresh").addEventListener("click", loadProjects);
  $("load-info").addEventListener("click", loadInfo);
  $("stop").addEventListener("click", stopRun);
  $("tab-project").addEventListener("click", () => showView("project"));
  $("tab-graph").addEventListener("click", () => showView("graph"));
  $("graph-command").addEventListener("change", (e) => {
    e.target.dataset.chosen = e.target.value;
    renderGraph();
  });

  if (location.hash === GRAPH_HASH) showView("graph");
  loadProjects();
  loadRuns();
  const timers = [setInterval(loadProjects, STATUS_INTERVAL), setInterval(loadRuns, RUNS_INTERVAL)];
  // Stop polling once signed out.
  const stopWhenLoggedOut = setInterval(() => {
    if (state.token) return;
    timers.forEach(clearInterval);
    clearInterval(stopWhenLoggedOut);
  }, RUNS_INTERVAL);
}

boot();
