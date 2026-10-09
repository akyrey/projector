// Run with: node internal/web/testdata/graph_test.js (make test-js)
"use strict";
const assert = require("node:assert/strict");
const { layoutGraph } = require("../static/graph.js");

const edge = (from, to, cmd = "start") => ({ from, fromCommand: cmd, to, toCommand: cmd });

// Mirrors the work setup: webshop → core-api → core/payment/wallet/sso …, all → traefik.
const projects = ["core", "core-api", "payment", "sso", "totalizer", "traefik", "wallet", "webshop", "donezo"];
const edges = [
  edge("webshop", "core-api"),
  edge("core-api", "core"), edge("core-api", "payment"), edge("core-api", "wallet"), edge("core-api", "sso"),
  edge("payment", "core"), edge("payment", "wallet"), edge("payment", "sso"),
  edge("core", "wallet"), edge("core", "totalizer"),
  ...["core", "core-api", "payment", "sso", "totalizer", "wallet", "webshop"].map((p) => edge(p, "traefik")),
  edge("core-api", "core", "update"),
];

const tests = {
  "dependents sit above their dependencies"() {
    const g = layoutGraph(projects, edges, "start");
    const row = Object.fromEntries(g.nodes.map((n) => [n.id, n.row]));
    for (const e of g.edges) assert.ok(row[e.from] < row[e.to], `${e.from} (row ${row[e.from]}) should be above ${e.to} (row ${row[e.to]})`);
    assert.equal(row.webshop, 0);
    assert.equal(row.traefik, Math.max(...Object.values(row)));
  },
  "projects without edges are left out"() {
    const g = layoutGraph(projects, edges, "start");
    assert.ok(!g.nodes.some((n) => n.id === "donezo"));
  },
  "command filter keeps matching edges, null keeps all once per pair"() {
    assert.equal(layoutGraph(projects, edges, "update").edges.length, 1);
    const all = layoutGraph(projects, edges, null);
    const pairs = all.edges.map((e) => `${e.from}>${e.to}`);
    assert.equal(new Set(pairs).size, pairs.length);
    assert.deepEqual(all.edges.find((e) => e.from === "core-api" && e.to === "core").commands, ["start→start", "update→update"]);
  },
  "unregistered targets are flagged as missing"() {
    const g = layoutGraph(["api"], [edge("api", "ghost")], "start");
    assert.equal(g.nodes.find((n) => n.id === "ghost").missing, true);
    assert.equal(g.nodes.find((n) => n.id === "api").missing, false);
  },
  "nodes in a row do not overlap and fit the canvas"() {
    const g = layoutGraph(projects, edges, "start");
    for (const a of g.nodes) {
      assert.ok(a.x >= 0 && a.x + a.w <= g.width && a.y + a.h <= g.height);
      for (const b of g.nodes) if (a !== b && a.row === b.row) assert.ok(a.x + a.w <= b.x || b.x + b.w <= a.x, `${a.id} overlaps ${b.id}`);
    }
  },
  "a lone self-edge draws nothing"() {
    assert.equal(layoutGraph(["a"], [edge("a", "a")], "start").nodes.length, 0);
  },
  "edges skipping rows bow sideways, inside the canvas"() {
    const g = layoutGraph(projects, edges, "start");
    const nodeAt = Object.fromEntries(g.nodes.map((n) => [n.id, n]));
    for (const e of g.edges) {
      const span = nodeAt[e.to].row - nodeAt[e.from].row;
      if (span > 1) assert.notEqual(e.c1x, e.x1, `${e.from}→${e.to} spans ${span} rows but goes straight`);
      else assert.equal(e.c1x, e.x1);
      for (const x of [e.c1x, e.c2x]) assert.ok(x >= 0 && x <= g.width, `${e.from}→${e.to} control ${x} outside 0..${g.width}`);
    }
  },
  "cycles and self-edges leave no empty rows"() {
    for (const [name, es] of [
      ["cycle", [edge("a", "b"), edge("b", "a")]],
      ["self-edge next to an edge", [edge("a", "a"), edge("a", "b")]],
      ["cycle below a chain", [edge("top", "a"), edge("a", "b"), edge("b", "a")]],
    ]) {
      const g = layoutGraph(["a", "b", "top"], es, "start");
      const rows = new Set(g.nodes.map((n) => n.row));
      const maxRow = Math.max(...rows);
      assert.equal(rows.size, maxRow + 1, `${name}: rows ${[...rows]} have a gap`);
      assert.ok(g.edges.every((e) => e.from !== e.to), `${name}: self-edge drawn`);
      assert.equal(g.height, 2 * 12 + (maxRow + 1) * 34 + maxRow * 64, `${name}: canvas has a blank band`);
    }
  },
};

let failed = 0;
for (const [name, fn] of Object.entries(tests)) {
  try {
    fn();
    console.log(`ok   ${name}`);
  } catch (e) {
    failed++;
    console.log(`FAIL ${name}\n     ${e.message}`);
  }
}
process.exitCode = failed ? 1 : 0;
