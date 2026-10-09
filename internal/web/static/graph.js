"use strict";

// Layered layout of the cross-project dependency graph: every project sits
// one row above the deepest project it depends on, so the dependency chain
// reads top (dependents) to bottom (shared infrastructure).

const GRAPH_NODE = { w: 128, h: 34, hgap: 22, vgap: 64, pad: 12, bow: 70 };

// layoutGraph returns positioned nodes and edges. command filters edges by
// the depending command; null keeps every edge, merged per project pair.
function layoutGraph(projects, edges, command) {
  const registered = new Set(projects);

  // Merge edges per (from, to) pair, recording which commands link them.
  const pairs = new Map();
  for (const e of edges) {
    if (command !== null && e.fromCommand !== command) continue;
    if (e.from === e.to) continue; // a project depending on itself has nothing to draw
    const key = `${e.from}\u0000${e.to}`;
    if (!pairs.has(key)) pairs.set(key, { from: e.from, to: e.to, commands: [] });
    pairs.get(key).commands.push(`${e.fromCommand}→${e.toCommand}`);
  }
  const merged = [...pairs.values()];

  const deps = new Map(); // id → ids it depends on
  for (const e of merged) {
    for (const id of [e.from, e.to]) if (!deps.has(id)) deps.set(id, []);
    deps.get(e.from).push(e.to);
  }

  // depth = longest path to a project without deps; back edges of a cycle
  // are ignored so the recursion terminates.
  const depth = new Map();
  const visiting = new Set();
  const depthOf = (id) => {
    if (depth.has(id)) return depth.get(id);
    if (visiting.has(id)) return 0;
    visiting.add(id);
    let d = 0;
    for (const dep of deps.get(id)) d = Math.max(d, depthOf(dep) + 1);
    visiting.delete(id);
    depth.set(id, d);
    return d;
  };
  const ids = [...deps.keys()].sort();
  ids.forEach(depthOf);
  // A cycle can skip depths (its back edge counts as 0); renumber the depths
  // in use densely so no row is left empty.
  const used = [...new Set(depth.values())].sort((a, b) => a - b);
  for (const id of ids) depth.set(id, used.indexOf(depth.get(id)));
  const maxDepth = Math.max(0, ...depth.values());

  // Rows from the bottom up, each ordered by the mean position of its deps
  // (barycenter) to reduce crossings, ties broken by name.
  const rows = [];
  for (let r = 0; r <= maxDepth; r++) rows.push([]);
  for (const id of ids) rows[maxDepth - depth.get(id)].push(id);
  const order = new Map();
  for (let r = maxDepth; r >= 0; r--) {
    const score = (id) => {
      const placed = deps.get(id).filter((d) => order.has(d)).map((d) => order.get(d));
      return placed.length ? placed.reduce((a, b) => a + b, 0) / placed.length : Infinity;
    };
    rows[r].sort((a, b) => score(a) - score(b) || a.localeCompare(b));
    rows[r].forEach((id, i) => order.set(id, i));
  }

  const { w, h, hgap, vgap, pad, bow } = GRAPH_NODE;
  const widest = Math.max(1, ...rows.map((row) => row.length));
  // Side margins leave room for edges that bow around intermediate rows.
  const margin = pad + bow;
  const width = margin * 2 + widest * w + (widest - 1) * hgap;
  const height = pad * 2 + rows.length * h + (rows.length - 1) * vgap;

  const nodes = [];
  const byId = new Map();
  rows.forEach((row, r) => {
    const rowWidth = row.length * w + (row.length - 1) * hgap;
    const left = (width - rowWidth) / 2;
    row.forEach((id, i) => {
      const node = { id, row: r, x: left + i * (w + hgap), y: pad + r * (h + vgap), w, h, missing: !registered.has(id) };
      nodes.push(node);
      byId.set(id, node);
    });
  });

  // Edges are cubic curves. One that skips rows bows towards the nearer side
  // so it passes beside, not through, the boxes in between.
  const laidOut = merged.map((e) => {
    const a = byId.get(e.from);
    const b = byId.get(e.to);
    const x1 = a.x + w / 2;
    const x2 = b.x + w / 2;
    const y1 = a.y + h;
    const y2 = b.y;
    const span = b.row - a.row;
    let c1x = x1;
    let c2x = x2;
    let c1y = (y1 + y2) / 2;
    let c2y = c1y;
    if (span > 1) {
      const side = (x1 + x2) / 2 < width / 2 ? -1 : 1;
      const edgeX = side < 0 ? Math.min(x1, x2) - w / 2 - bow : Math.max(x1, x2) + w / 2 + bow;
      c1x = c2x = Math.min(width, Math.max(0, edgeX));
      c1y = y1 + vgap;
      c2y = y2 - vgap;
    }
    return { ...e, x1, y1, x2, y2, c1x, c1y, c2x, c2y };
  });
  return { nodes, edges: laidOut, width, height };
}

if (typeof module !== "undefined") module.exports = { layoutGraph };
