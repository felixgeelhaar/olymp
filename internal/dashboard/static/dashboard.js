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
function firePacket(layer, failed = false) {
  const node = NODES.find((n) => n.name === layer);
  if (!node) return;
  const p = node.pos;

  const g = document.createElementNS(SVG_NS, "g");
  g.setAttribute("class", failed ? "packet failed" : "packet");
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
    li.innerHTML = `
      <span class="id">${shortID(id)}</span>
      <span class="intent">${escapeHTML(r.intent)}</span>
      <span class="status-tag">${escapeHTML(r.status)}</span>
    `;
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
      if (nextLayer) firePacket(nextLayer);
      // Track terminal status if present
      const to = ev.payload?.to;
      if (to === "completed" || to === "failed" || to === "cancelled" || to === "awaiting_approval") {
        upsertRun(ev.run_id, { status: to });
        if (to === "completed") state.completed++;
        if (to === "failed") state.failed++;
      }
      break;
    case "layer_called":
      if (ev.payload?.layer) firePacket(ev.payload.layer);
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

drawTopology();
connect();
setInterval(refreshCounters, 1000);
