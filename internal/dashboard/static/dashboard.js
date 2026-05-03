// olymp dashboard — live SSE-driven view of the cognitive loop.
//
// Renders four cognitive-layer nodes around a center "olymp" runtime.
// Each `layer_called` event animates a packet from the center to the
// named layer along the connecting line. Run rows + a raw event log
// keep the timeline visible for debugging.
//
// No build step. No deps. Vanilla DOM + SVG.

const CENTER = { x: 300, y: 300 };
const RADIUS = 220;
const NODES = [
  { name: "mnemos",  sub: "memory",    angle: -90 }, // top
  { name: "chronos", sub: "time",      angle:   0 }, // right
  { name: "praxis",  sub: "execution", angle:  90 }, // bottom
  { name: "nous",    sub: "decisions", angle: 180 }, // left
];

const STAGE_TO_LAYER = {
  observing:     "mnemos",
  understanding: "chronos",
  deciding:      "nous",
  acting:        "praxis",
  learning:      "mnemos",
};

const els = {
  links:    document.getElementById("links"),
  nodes:    document.getElementById("nodes"),
  packets:  document.getElementById("packets"),
  status:   document.getElementById("status"),
  statusD:  document.getElementById("status-dot"),
  statusT:  document.getElementById("status-text"),
  inflight: document.getElementById("cnt-inflight"),
  completed:document.getElementById("cnt-completed"),
  failed:   document.getElementById("cnt-failed"),
  events:   document.getElementById("cnt-events"),
  rate:     document.getElementById("feed-rate"),
  runs:     document.getElementById("runs"),
  log:      document.getElementById("log"),
  drawer:   document.getElementById("drawer"),
  drawerTitle: document.getElementById("drawer-title"),
  drawerBody:  document.getElementById("drawer-body"),
  drawerClose: document.getElementById("drawer-close"),
};

const SVG_NS = "http://www.w3.org/2000/svg";

function polar(angleDeg, r) {
  const rad = (angleDeg * Math.PI) / 180;
  return { x: CENTER.x + r * Math.cos(rad), y: CENTER.y + r * Math.sin(rad) };
}

function drawTopology() {
  for (const n of NODES) {
    const p = polar(n.angle, RADIUS);
    n.pos = p;

    // Click handler attaches to the whole group (circle + labels).
    const onClick = () => showLayerInspector(n.name);

    // Connecting line center → node
    const line = document.createElementNS(SVG_NS, "line");
    line.setAttribute("x1", CENTER.x);
    line.setAttribute("y1", CENTER.y);
    line.setAttribute("x2", p.x);
    line.setAttribute("y2", p.y);
    line.setAttribute("data-layer", n.name);
    els.links.appendChild(line);

    // Node circle + labels
    const g = document.createElementNS(SVG_NS, "g");
    g.setAttribute("data-layer", n.name);
    g.addEventListener("click", onClick);
    const c = document.createElementNS(SVG_NS, "circle");
    c.setAttribute("cx", p.x);
    c.setAttribute("cy", p.y);
    c.setAttribute("r", 50);
    c.setAttribute("class", "node-circle");
    g.appendChild(c);
    const tName = document.createElementNS(SVG_NS, "text");
    tName.setAttribute("x", p.x);
    tName.setAttribute("y", p.y - 4);
    tName.setAttribute("class", "node-label");
    tName.textContent = n.name;
    g.appendChild(tName);
    const tSub = document.createElementNS(SVG_NS, "text");
    tSub.setAttribute("x", p.x);
    tSub.setAttribute("y", p.y + 14);
    tSub.setAttribute("class", "node-sub");
    tSub.textContent = n.sub;
    g.appendChild(tSub);
    els.nodes.appendChild(g);
  }
}

function nodeEl(layer) {
  return els.nodes.querySelector(`g[data-layer="${layer}"] circle`);
}

function flashNode(layer, failed = false) {
  const c = nodeEl(layer);
  if (!c) return;
  c.classList.add(failed ? "failed" : "active");
  clearTimeout(c.__flashTimer);
  c.__flashTimer = setTimeout(() => {
    c.classList.remove("active", "failed");
  }, 1200);
}

// Animate a packet from center → layer node along the connection.
function firePacket(layer, failed = false, runID = null) {
  const node = NODES.find((n) => n.name === layer);
  if (!node) return;
  const p = node.pos;

  const g = document.createElementNS(SVG_NS, "g");
  g.setAttribute("class", failed ? "packet failed" : "packet");
  if (runID) {
    g.dataset.runId = runID;
    g.style.cursor = "pointer";
    g.addEventListener("click", () => showRunInspector(runID));
  }
  const c = document.createElementNS(SVG_NS, "circle");
  c.setAttribute("r", 6);
  c.setAttribute("cx", CENTER.x);
  c.setAttribute("cy", CENTER.y);
  g.appendChild(c);
  els.packets.appendChild(g);

  const start = performance.now();
  const dur = 700;
  function step(t) {
    const elapsed = t - start;
    const k = Math.min(1, elapsed / dur);
    const x = CENTER.x + (p.x - CENTER.x) * k;
    const y = CENTER.y + (p.y - CENTER.y) * k;
    c.setAttribute("cx", x);
    c.setAttribute("cy", y);
    if (k < 1) {
      requestAnimationFrame(step);
    } else {
      flashNode(layer, failed);
      // Fade and remove
      g.style.opacity = 0;
      setTimeout(() => g.remove(), 400);
    }
  }
  requestAnimationFrame(step);
}

// State + counters
const state = {
  runs: new Map(),       // runId → { intent, status, lastSeen }
  completed: 0,
  failed: 0,
  events: 0,
  eventsLast60s: [],     // timestamps
};

function setCounter(el, n) {
  if (el.textContent !== String(n)) el.textContent = String(n);
}

function refreshCounters() {
  let inflight = 0;
  for (const r of state.runs.values()) {
    if (r.status !== "completed" && r.status !== "failed" && r.status !== "cancelled") {
      inflight++;
    }
  }
  setCounter(els.inflight, inflight);
  setCounter(els.completed, state.completed);
  setCounter(els.failed, state.failed);
  setCounter(els.events, state.events);

  const now = Date.now();
  state.eventsLast60s = state.eventsLast60s.filter((t) => now - t < 60000);
  els.rate.textContent = `(${state.eventsLast60s.length}/min)`;
}

function shortID(id) {
  if (!id) return "";
  return id.length > 8 ? `${id.slice(0, 8)}…` : id;
}

function upsertRun(runId, patch) {
  const cur = state.runs.get(runId) || { intent: "?", status: "running", lastSeen: 0 };
  Object.assign(cur, patch, { lastSeen: Date.now() });
  state.runs.set(runId, cur);
  renderRuns();
}

function renderRuns() {
  // Recent first, cap 12
  const ordered = [...state.runs.entries()]
    .sort((a, b) => b[1].lastSeen - a[1].lastSeen)
    .slice(0, 12);
  els.runs.innerHTML = "";
  for (const [id, r] of ordered) {
    const li = document.createElement("li");
    li.className = r.status;
    li.dataset.runId = id;
    li.title = "Click to inspect this run's full chain";
    li.innerHTML = `
      <span class="id">${shortID(id)}</span>
      <span class="intent">${escapeHTML(r.intent)}</span>
      <span class="status-tag">${escapeHTML(r.status)}</span>
    `;
    li.addEventListener("click", () => showRunInspector(id));
    els.runs.appendChild(li);
  }
}

function appendLog(ev) {
  const li = document.createElement("li");
  const ts = new Date(ev.timestamp || Date.now()).toLocaleTimeString();
  const payload = ev.payload ? JSON.stringify(ev.payload) : "";
  li.innerHTML = `
    <span class="t">${escapeHTML(ts.split(" ")[0])}</span>
    <span class="k">${escapeHTML(ev.kind || "?")}</span>
    <span class="p" title="${escapeHTML(payload)}">${escapeHTML(payload)}</span>
  `;
  els.log.prepend(li);
  while (els.log.children.length > 60) els.log.lastChild.remove();
}

function escapeHTML(s) {
  return String(s ?? "").replace(/[&<>"']/g, (c) => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
  })[c]);
}

function handleEvent(ev) {
  state.events++;
  state.eventsLast60s.push(Date.now());
  appendLog(ev);

  switch (ev.kind) {
    case "submitted":
      upsertRun(ev.run_id, {
        intent: ev.payload?.intent_type || "?",
        status: "running",
      });
      break;
    case "transitioned":
      // Fire packet for the layer that just kicked off
      const nextLayer = STAGE_TO_LAYER[ev.payload?.to];
      if (nextLayer) firePacket(nextLayer, false, ev.run_id);
      // Track terminal status if present
      const to = ev.payload?.to;
      if (to === "completed" || to === "failed" || to === "cancelled" || to === "awaiting_approval") {
        upsertRun(ev.run_id, { status: to });
        if (to === "completed") state.completed++;
        if (to === "failed") state.failed++;
      }
      break;
    case "layer_called":
      if (ev.payload?.layer) firePacket(ev.payload.layer, false, ev.run_id);
      break;
    case "approval_required":
      upsertRun(ev.run_id, { status: "awaiting_approval" });
      break;
    case "steered":
      // touch the run to keep it visible
      upsertRun(ev.run_id, {});
      break;
  }
  refreshCounters();
}

function setStatus(state) {
  els.status.classList.remove("live", "dead");
  if (state === "live") {
    els.status.classList.add("live");
    els.statusT.textContent = "live";
  } else if (state === "dead") {
    els.status.classList.add("dead");
    els.statusT.textContent = "disconnected — retrying";
  } else {
    els.statusT.textContent = "connecting…";
  }
}

function connect() {
  const es = new EventSource("/v1/runs/stream");
  setStatus("connecting");
  es.onopen = () => setStatus("live");
  es.onerror = () => {
    setStatus("dead");
    es.close();
    setTimeout(connect, 2000);
  };
  es.onmessage = (msg) => {
    try {
      const ev = JSON.parse(msg.data);
      handleEvent(ev);
    } catch (err) {
      console.warn("bad event", msg.data, err);
    }
  };
}

// --- Inspector drawer ---
//
// Two flavours:
//  - Run inspector: full provenance chain for a specific run, fetched
//    from /v1/runs/{id}. Shows per-stage Inputs/Outputs so you can
//    see what each layer received and returned.
//  - Layer inspector: scans the recently-cached run snapshots for
//    steps whose LayerRef.Layer matches the clicked node, lists the
//    most recent N with their Outputs.

// Cache the last fetched run snapshots so the layer inspector has
// something to show without re-fetching everything.
const snapshotCache = new Map(); // runID → snapshot

function openDrawer(title) {
  els.drawerTitle.textContent = title;
  els.drawer.classList.add("open");
  els.drawer.setAttribute("aria-hidden", "false");
}
function closeDrawer() {
  els.drawer.classList.remove("open");
  els.drawer.setAttribute("aria-hidden", "true");
}
els.drawerClose.addEventListener("click", closeDrawer);
document.addEventListener("keydown", (e) => {
  if (e.key === "Escape") closeDrawer();
});

async function fetchRun(runID) {
  if (snapshotCache.has(runID)) return snapshotCache.get(runID);
  const resp = await fetch(`/v1/runs/${encodeURIComponent(runID)}`, {
    headers: { Accept: "application/json" },
  });
  if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
  const snap = await resp.json();
  snapshotCache.set(runID, snap);
  return snap;
}

function durationMs(start, end) {
  if (!start || !end) return null;
  const d = new Date(end) - new Date(start);
  return isNaN(d) ? null : d;
}

function fmtJSON(v) {
  if (v == null) return null;
  try {
    return JSON.stringify(v, null, 2);
  } catch {
    return String(v);
  }
}

async function showRunInspector(runID) {
  openDrawer(`Run · ${shortID(runID)}`);
  els.drawerBody.innerHTML = `<p class="dim">Loading…</p>`;
  let snap;
  try {
    snap = await fetchRun(runID);
  } catch (err) {
    els.drawerBody.innerHTML = `<p class="dim">Could not load run: ${escapeHTML(err.message)}</p>`;
    return;
  }
  const run = snap.run || {};
  const meta = `
    <div class="meta">
      <strong>id</strong><span>${escapeHTML(run.id || runID)}</span>
      <strong>intent</strong><span>${escapeHTML(run.intent?.type || "?")} · ${escapeHTML(run.intent?.subject || "")}</span>
      <strong>status</strong><span>${escapeHTML(run.status || "?")}</span>
      <strong>iterations</strong><span>${escapeHTML(String(run.iteration ?? 0))}</span>
      <strong>started</strong><span>${escapeHTML(run.started_at || "")}</span>
    </div>
  `;
  const stages = (snap.timeline || []).map((step) => {
    const dur = durationMs(step.started_at, step.completed_at);
    const failed = !!step.error;
    const layer = step.layer_ref?.layer || "?";
    const stageTag = step.stage || "?";
    const inputs = fmtJSON(step.inputs);
    const outputs = fmtJSON(step.outputs);
    const errBlock = step.error
      ? `<div class="kv-block"><h4>error</h4><pre>${escapeHTML(fmtJSON(step.error))}</pre></div>`
      : "";
    return `
      <div class="stage-row ${failed ? "failed" : ""}">
        <div class="head">
          <span class="stage-tag">${escapeHTML(stageTag)}</span>
          <span class="layer">${escapeHTML(layer)}</span>
          <span class="dur">${dur != null ? dur + "ms" : ""}</span>
        </div>
        <div class="kv-block">
          <h4>inputs (sent to ${escapeHTML(layer)})</h4>
          ${inputs ? `<pre>${escapeHTML(inputs)}</pre>` : `<div class="none">— none recorded —</div>`}
        </div>
        <div class="kv-block">
          <h4>outputs (received back)</h4>
          ${outputs ? `<pre>${escapeHTML(outputs)}</pre>` : `<div class="none">— none recorded —</div>`}
        </div>
        ${errBlock}
      </div>
    `;
  }).join("");
  els.drawerBody.innerHTML = meta + (stages || `<p class="dim">No timeline steps yet.</p>`);
}

async function showLayerInspector(layer) {
  openDrawer(`Layer · ${layer}`);
  // Pull the recent runs we know about and surface every step that
  // touched this layer.
  const ids = [...state.runs.keys()].slice(0, 8);
  if (ids.length === 0) {
    els.drawerBody.innerHTML = `<p class="dim">No runs observed yet. Submit one and click again.</p>`;
    return;
  }
  els.drawerBody.innerHTML = `<p class="dim">Loading recent traffic to ${escapeHTML(layer)}…</p>`;
  const items = [];
  await Promise.all(ids.map(async (id) => {
    try {
      const snap = await fetchRun(id);
      for (const step of snap.timeline || []) {
        if (step.layer_ref?.layer === layer) {
          items.push({ runID: id, step, intent: snap.run?.intent });
        }
      }
    } catch { /* skip unreachable */ }
  }));
  items.sort((a, b) => new Date(b.step.completed_at || 0) - new Date(a.step.completed_at || 0));
  if (items.length === 0) {
    els.drawerBody.innerHTML = `<p class="dim">No recent runs touched <strong>${escapeHTML(layer)}</strong>.</p>`;
    return;
  }
  const html = `
    <p class="dim">Last ${items.length} step(s) routed through <strong>${escapeHTML(layer)}</strong>:</p>
    <ul class="layer-list">
      ${items.slice(0, 12).map((it) => `
        <li>
          <div class="top">
            <span class="id" data-run-id="${escapeHTML(it.runID)}">${shortID(it.runID)} · ${escapeHTML(it.intent?.type || "?")}</span>
            <span>${escapeHTML(it.step.stage || "?")}</span>
          </div>
          ${it.step.outputs ? `<pre>${escapeHTML(fmtJSON(it.step.outputs))}</pre>` : `<div class="none">— no outputs recorded —</div>`}
        </li>
      `).join("")}
    </ul>
  `;
  els.drawerBody.innerHTML = html;
  // Click an id to drill into the full chain
  els.drawerBody.querySelectorAll("[data-run-id]").forEach((el) => {
    el.style.cursor = "pointer";
    el.addEventListener("click", () => showRunInspector(el.dataset.runId));
  });
}

// Invalidate snapshot cache when a run transitions to a terminal state
// so the next inspect picks up the final timeline entries.
const origHandle = handleEvent;
handleEvent = function (ev) {
  if (ev.kind === "transitioned" &&
    ["completed", "failed", "cancelled"].includes(ev.payload?.to)) {
    snapshotCache.delete(ev.run_id);
  }
  origHandle(ev);
};

drawTopology();
connect();
setInterval(refreshCounters, 1000);
