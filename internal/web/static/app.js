// Frontend logic for the deal gallery + watchlist. The animated "hella cute"
// pass — confetti, springy entrances, mascot, skeletons, WebGL background — is
// milestone M5 and lives in the "visual engine" section just below.
"use strict";

const REDUCE_MOTION = window.matchMedia("(prefers-reduced-motion: reduce)").matches;

/* ============================================================================
   Visual engine (M5) — purely cosmetic; degrades gracefully everywhere.
   ============================================================================ */

// startBackground paints a flowing pastel gradient. It tries WebGL (a
// domain-warped fragment shader) and falls back to an animated 2D canvas, and
// if neither is available the CSS .bg-fallback blobs show through unchanged.
function startBackground() {
  const canvas = document.getElementById("bg");
  if (!canvas || REDUCE_MOTION) { if (canvas) canvas.style.display = "none"; return; }
  if (startWebGLBackground(canvas)) return;
  start2DBackground(canvas);
}

function startWebGLBackground(canvas) {
  const gl = canvas.getContext("webgl", { antialias: false, alpha: true })
          || canvas.getContext("experimental-webgl");
  if (!gl) return false;

  const vs = `attribute vec2 p; void main(){ gl_Position = vec4(p, 0.0, 1.0); }`;
  // Flowing pastel aurora: iterated domain-warped value-noise (fbm) ribbons,
  // tinted across a five-stop candy palette, with a handful of drifting bokeh
  // orbs floating over the top for that soft, alive, "hella cute" feel.
  const fs = `
    #ifdef GL_FRAGMENT_PRECISION_HIGH
    precision highp float;
    #else
    precision mediump float;
    #endif
    uniform vec2 res; uniform float t;

    vec3 pink  = vec3(1.00, 0.71, 0.86);
    vec3 rose  = vec3(1.00, 0.55, 0.78);
    vec3 lilac = vec3(0.72, 0.66, 1.00);
    vec3 sky   = vec3(0.63, 0.86, 1.00);
    vec3 mint  = vec3(0.66, 0.97, 0.85);

    float hash(vec2 p){ return fract(sin(dot(p, vec2(127.1, 311.7))) * 43758.5453); }
    float noise(vec2 p){
      vec2 i = floor(p), f = fract(p);
      vec2 u = f*f*(3.0 - 2.0*f);
      return mix(mix(hash(i + vec2(0.0,0.0)), hash(i + vec2(1.0,0.0)), u.x),
                 mix(hash(i + vec2(0.0,1.0)), hash(i + vec2(1.0,1.0)), u.x), u.y);
    }
    float fbm(vec2 p){
      float s = 0.0, a = 0.5;
      for (int i = 0; i < 5; i++){ s += a * noise(p); p *= 2.02; a *= 0.5; }
      return s;
    }

    void main(){
      vec2 uv = gl_FragCoord.xy / res;
      float asp = res.x / res.y;
      vec2 p = vec2(uv.x * asp, uv.y) * 1.6;
      float tt = t * 0.07;

      // Two rounds of domain warping → soft swirling aurora structure.
      vec2 q = vec2(fbm(p + vec2(0.0, tt)), fbm(p + vec2(5.2, tt + 1.3)));
      vec2 r = vec2(fbm(p + 2.2*q + vec2(1.7, 9.2) + tt),
                    fbm(p + 2.2*q + vec2(8.3, 2.8) - tt));
      float f = fbm(p + 2.6*r);

      vec3 col = mix(lilac, pink, smoothstep(0.10, 0.70, f));
      col = mix(col, sky,  smoothstep(0.60, 1.10, length(r)) * 0.75);
      col = mix(col, mint, smoothstep(0.28, 0.04, f) * 0.45);
      col = mix(col, rose, smoothstep(0.62, 0.92, f) * 0.70);

      // Drifting bokeh orbs — soft additive glow, gently looping paths.
      float bok = 0.0;
      for (int i = 0; i < 5; i++){
        float fi = float(i);
        vec2 c = vec2(0.5 + 0.43 * sin(t * 0.06 + fi * 2.1),
                      0.5 + 0.41 * cos(t * 0.05 + fi * 1.7));
        c.x *= asp;
        float d = length(vec2(uv.x * asp, uv.y) - c);
        bok += smoothstep(0.16, 0.0, d) * 0.09;
      }
      col += bok;

      col = mix(col, vec3(1.0), 0.08); // a whisper of haze, not a whiteout
      gl_FragColor = vec4(clamp(col, 0.0, 1.0), 0.97);
    }`;

  function compile(type, src) {
    const sh = gl.createShader(type);
    gl.shaderSource(sh, src); gl.compileShader(sh);
    if (!gl.getShaderParameter(sh, gl.COMPILE_STATUS)) return null;
    return sh;
  }
  const v = compile(gl.VERTEX_SHADER, vs), f = compile(gl.FRAGMENT_SHADER, fs);
  if (!v || !f) return false;
  const prog = gl.createProgram();
  gl.attachShader(prog, v); gl.attachShader(prog, f); gl.linkProgram(prog);
  if (!gl.getProgramParameter(prog, gl.LINK_STATUS)) return false;
  gl.useProgram(prog);

  const buf = gl.createBuffer();
  gl.bindBuffer(gl.ARRAY_BUFFER, buf);
  gl.bufferData(gl.ARRAY_BUFFER, new Float32Array([-1,-1, 3,-1, -1,3]), gl.STATIC_DRAW);
  const loc = gl.getAttribLocation(prog, "p");
  gl.enableVertexAttribArray(loc);
  gl.vertexAttribPointer(loc, 2, gl.FLOAT, false, 0, 0);
  const uRes = gl.getUniformLocation(prog, "res");
  const uT = gl.getUniformLocation(prog, "t");

  function resize() {
    const dpr = Math.min(window.devicePixelRatio || 1, 2);
    canvas.width = Math.floor(innerWidth * dpr);
    canvas.height = Math.floor(innerHeight * dpr);
    gl.viewport(0, 0, canvas.width, canvas.height);
  }
  resize();
  window.addEventListener("resize", resize);

  const start = performance.now();
  (function frame(now) {
    gl.uniform2f(uRes, canvas.width, canvas.height);
    gl.uniform1f(uT, (now - start) / 1000);
    gl.drawArrays(gl.TRIANGLES, 0, 3);
    requestAnimationFrame(frame);
  })(start);
  return true;
}

// Canvas-2D fallback: a few softly drifting radial-gradient orbs.
function start2DBackground(canvas) {
  const ctx = canvas.getContext("2d");
  if (!ctx) { canvas.style.display = "none"; return; }
  const orbs = [
    { x: 0.15, y: 0.2, r: 0.55, c: "255,150,205", px: 0.00006, py: 0.00004 },
    { x: 0.85, y: 0.25, r: 0.5, c: "176,150,255", px: -0.00005, py: 0.00006 },
    { x: 0.5, y: 0.85, r: 0.55, c: "150,210,255", px: 0.00004, py: -0.00005 },
    { x: 0.7, y: 0.55, r: 0.4, c: "150,235,200", px: -0.00003, py: -0.00006 },
  ];
  function resize() { canvas.width = innerWidth; canvas.height = innerHeight; }
  resize();
  window.addEventListener("resize", resize);
  (function frame(now) {
    const W = canvas.width, H = canvas.height, m = Math.max(W, H);
    ctx.clearRect(0, 0, W, H);
    ctx.fillStyle = "#fff5fb"; ctx.fillRect(0, 0, W, H);
    for (const o of orbs) {
      const cx = (o.x + Math.sin(now * o.px) * 0.08) * W;
      const cy = (o.y + Math.cos(now * o.py) * 0.08) * H;
      const r = o.r * m;
      const g = ctx.createRadialGradient(cx, cy, 0, cx, cy, r);
      g.addColorStop(0, `rgba(${o.c},0.85)`);
      g.addColorStop(1, `rgba(${o.c},0)`);
      ctx.fillStyle = g;
      ctx.fillRect(0, 0, W, H);
    }
    requestAnimationFrame(frame);
  })(0);
}

// burstConfetti fires a celebratory pastel shower from a screen point.
function burstConfetti(x, y) {
  if (REDUCE_MOTION) return;
  const layer = document.getElementById("confetti");
  if (!layer) return;
  const colors = ["#ff8ec6", "#b9a3ff", "#7fd6ff", "#86e8b8", "#ffcf5c", "#ff5fae"];
  const N = 34;
  for (let i = 0; i < N; i++) {
    const piece = document.createElement("span");
    piece.className = "confetti";
    piece.style.background = colors[i % colors.length];
    piece.style.left = x + "px";
    piece.style.top = y + "px";
    piece.style.borderRadius = i % 3 === 0 ? "50%" : "3px";
    layer.appendChild(piece);

    const angle = (Math.PI * 2 * i) / N + Math.random();
    const dist = 90 + Math.random() * 150;
    const dx = Math.cos(angle) * dist;
    const dy = Math.sin(angle) * dist - 60; // bias upward, then gravity
    const rot = (Math.random() * 720 - 360) + "deg";
    const anim = piece.animate(
      [
        { transform: "translate(0,0) rotate(0deg)", opacity: 1 },
        { transform: `translate(${dx * 0.6}px, ${dy}px) rotate(${rot})`, opacity: 1, offset: 0.55 },
        { transform: `translate(${dx}px, ${dy + 240}px) rotate(${rot})`, opacity: 0 },
      ],
      { duration: 1100 + Math.random() * 500, easing: "cubic-bezier(0.2, 0.7, 0.3, 1)" }
    );
    anim.onfinish = () => piece.remove();
  }
}

// boingMascot gives the topbar mascot a squash-and-stretch on demand.
function boingMascot() {
  const m = document.getElementById("mascot");
  if (!m || REDUCE_MOTION) return;
  m.classList.remove("boing");
  void m.offsetWidth; // restart the animation
  m.classList.add("boing");
}

const PAGE_SIZE = 48;
let offset = 0;
let total = 0;
// The category nav's current selection. "" means "All categories".
let activeCategory = "";

// Display metadata for known categories; unknown ones fall back to a
// title-cased label with a generic tag icon, so new verticals still render.
const CATEGORY_META = {
  games: { label: "Games", icon: "🎮" },
  tech: { label: "Tech", icon: "💻" },
  media: { label: "Media", icon: "🎬" },
  clothing: { label: "Clothing", icon: "👕" },
  home: { label: "Home", icon: "🏠" },
  sports: { label: "Sports", icon: "🏃" },
  general: { label: "Random shit", icon: "🛍️" },
};
function catMeta(c) {
  return CATEGORY_META[c] || { label: c.charAt(0).toUpperCase() + c.slice(1), icon: "🏷️" };
}

// Auth + watchlist state. `watched` maps a normalized game name -> watch id so
// cards can render the right toggle and remove entries without a round-trip.
let me = { authenticated: false, oidcEnabled: false };
const watched = new Map();

const $ = (id) => document.getElementById(id);
const grid = $("grid");

// Mirror watchlist.Normalize on the server (lowercase, keep letters/digits/
// spaces, collapse whitespace) so optimistic UI state matches what the bot
// stores. The server stays the source of truth.
function normalize(s) {
  return String(s ?? "")
    .toLowerCase()
    .replace(/[^\p{L}\p{N} ]/gu, "")
    .replace(/\s+/g, " ")
    .trim();
}

// Read the current filter state from the controls into query params.
function currentParams() {
  const p = new URLSearchParams();
  const q = $("q").value.trim();
  if (q) p.set("q", q);
  if (activeCategory) p.set("category", activeCategory);
  if ($("source").value) p.set("source", $("source").value);
  if ($("store").value) p.set("store", $("store").value);
  if ($("min_discount").value) p.set("min_discount", $("min_discount").value);
  if ($("max_price").value) p.set("max_price", $("max_price").value);
  if ($("hist_low").checked) p.set("hist_low", "1");
  if ($("free").checked) p.set("free", "1");
  if ($("great").checked) p.set("great", "1");
  p.set("sort", $("sort").value);
  p.set("limit", PAGE_SIZE);
  p.set("offset", offset);
  return p;
}

function money(n) {
  if (n == null) return "";
  return "$" + Number(n).toFixed(2);
}

function cardHTML(d) {
  const badges = [];
  if (d.discount > 0) badges.push(`<span class="badge discount">-${d.discount}%</span>`);
  if (d.isFree) badges.push(`<span class="badge free">FREE</span>`);
  // Trust verdict from Pastel's own price history. all-time-low and historical
  // low are distinct signals (own-observed vs ITAD), so show both when present.
  if (d.verdict === "all-time-low") badges.push(`<span class="badge verdict-atl">🔥 All-time low</span>`);
  else if (d.verdict === "good") badges.push(`<span class="badge verdict-good">✓ Good price</span>`);
  if (d.isHistLow) badges.push(`<span class="badge low">★ historical low</span>`);
  if (d.priceSuspect) badges.push(`<span class="badge suspect">⚠ Check price</span>`);
  if (d.upcoming) badges.push(`<span class="badge">upcoming</span>`);

  // "Seen as low as" gives context only when we've actually observed it cheaper.
  const seenLow =
    !d.isFree && d.priceLow > 0 && d.priceLow < d.salePrice
      ? `<div class="seen-low">Seen as low as ${money(d.priceLow)}</div>`
      : "";

  const price = d.isFree
    ? `<div class="price">Free</div>`
    : `<div class="price">${money(d.salePrice)}${
        d.normalPrice > d.salePrice ? `<span class="normal">${money(d.normalPrice)}</span>` : ""
      }</div>${seenLow}`;

  // Deal URLs come from external sources; only emit a link for a benign
  // http(s) scheme so a javascript:/data: URL can't run as script (XSS).
  const href = safeURL(d.url);

  const div = document.createElement("div");
  div.className = "card";
  div.innerHTML = `
    <div class="badges">${badges.join("")}</div>
    <h3>${escapeHTML(d.title)}</h3>
    <div class="meta">${escapeHTML(d.store || d.source)}${d.rating ? ` · ★ ${d.rating}` : ""}</div>
    ${price}
    <div class="card-actions">
      ${href ? `<a class="buy" href="${escapeAttr(href)}" target="_blank" rel="noopener">View deal</a>` : ""}
    </div>
  `;

  if (me.authenticated) {
    const actions = div.querySelector(".card-actions");
    actions.appendChild(watchButton(d.title));
  }
  return div;
}

// watchButton builds a ★ toggle reflecting the current watched state.
function watchButton(title) {
  const norm = normalize(title);
  const btn = document.createElement("button");
  btn.className = "watch-btn";
  btn.dataset.norm = norm;
  syncWatchButton(btn);
  btn.addEventListener("click", () => toggleWatch(title, btn));
  return btn;
}

function syncWatchButton(btn) {
  const on = watched.has(btn.dataset.norm);
  btn.classList.toggle("on", on);
  btn.textContent = on ? "★ Watching" : "☆ Watch";
}

// Refresh every card's watch button to match `watched` (after add/remove).
function refreshWatchButtons() {
  for (const btn of document.querySelectorAll(".watch-btn")) syncWatchButton(btn);
}

async function toggleWatch(title, btn) {
  const norm = normalize(title);
  btn.disabled = true;
  try {
    if (watched.has(norm)) {
      const res = await fetch("/api/watchlist?id=" + encodeURIComponent(watched.get(norm)), { method: "DELETE" });
      if (!res.ok) throw new Error("remove failed: " + res.status);
      watched.delete(norm);
    } else {
      const res = await fetch("/api/watchlist", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ game: title }),
      });
      if (!res.ok) throw new Error("add failed: " + res.status);
      // Celebrate! Confetti bursts from the button that was just toggled on.
      const r = btn.getBoundingClientRect();
      burstConfetti(r.left + r.width / 2, r.top + r.height / 2);
      boingMascot();
      // Re-fetch to learn the new row id (and stay in sync with the server).
      await loadWatchlist();
    }
  } catch (err) {
    console.error("toggle watch failed", err);
  } finally {
    btn.disabled = false;
    refreshWatchButtons();
    renderDrawer();
  }
}

function escapeHTML(s) {
  return String(s ?? "").replace(/[&<>]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;" }[c]));
}
function escapeAttr(s) {
  return escapeHTML(s).replace(/"/g, "&quot;");
}

// safeURL returns the URL only when it parses as an http(s) link, otherwise "".
// This blocks javascript:/data: and other dangerous schemes from externally
// sourced deal URLs before they reach an href.
function safeURL(u) {
  if (!u) return "";
  try {
    const parsed = new URL(u, window.location.origin);
    return parsed.protocol === "http:" || parsed.protocol === "https:" ? parsed.href : "";
  } catch {
    return "";
  }
}

// renderSkeletons fills the grid with shimmering placeholder cards while a
// fresh query is in flight, so the page never flashes empty.
function renderSkeletons(n) {
  const frag = document.createDocumentFragment();
  for (let i = 0; i < n; i++) {
    const sk = document.createElement("div");
    sk.className = "card skeleton";
    sk.innerHTML = `
      <div class="badges"><div class="sk line w40"></div></div>
      <div class="sk title w70"></div>
      <div class="sk line w50"></div>
      <div class="sk price"></div>
      <div class="sk btn"></div>`;
    frag.appendChild(sk);
  }
  grid.appendChild(frag);
}

// playEntrance staggers a springy fade-up across freshly added cards.
function playEntrance(cards) {
  if (REDUCE_MOTION) return;
  cards.forEach((card, i) => {
    card.animate(
      [
        { opacity: 0, transform: "translateY(22px) scale(0.94)" },
        { opacity: 1, transform: "translateY(0) scale(1)" },
      ],
      { duration: 460, delay: Math.min(i * 45, 700), easing: "cubic-bezier(0.34, 1.56, 0.64, 1)", fill: "backwards" }
    );
  });
}

async function loadDeals(reset) {
  if (reset) {
    offset = 0;
    grid.innerHTML = "";
    renderSkeletons(8);
  }
  try {
    const res = await fetch("/api/deals?" + currentParams().toString());
    const data = await res.json();
    total = data.total || 0;
    if (reset) grid.innerHTML = "";

    const fresh = [];
    for (const d of data.deals) {
      const card = cardHTML(d);
      grid.appendChild(card);
      fresh.push(card);
    }
    playEntrance(fresh);
    offset += data.deals.length;

    $("count").textContent = total ? `${total} deal${total === 1 ? "" : "s"}` : "";
    $("empty").hidden = total !== 0;
    $("more").hidden = offset >= total;
  } catch (err) {
    console.error("failed to load deals", err);
    if (reset) grid.innerHTML = "";
    $("empty").hidden = false;
    $("empty").textContent = "Couldn't load deals 😢";
  }
}

async function loadFacets() {
  try {
    const res = await fetch("/api/facets");
    const data = await res.json();
    buildCatNav(data.categories);
    fillSelect($("source"), data.sources);
    fillSelect($("store"), data.stores);
  } catch (err) {
    console.error("failed to load facets", err);
  }
}

// buildCatNav paints the topbar category pills from the live facet list, with a
// leading "All" pill. It is data-driven, so adding a new vertical (a new
// category in the data) makes a new pill appear with no frontend change.
function buildCatNav(categories) {
  const nav = $("catnav");
  if (!nav) return;
  nav.innerHTML = "";
  const cats = ["", ...(categories || [])]; // "" = All
  for (const c of cats) {
    const btn = document.createElement("button");
    btn.className = "cat-pill" + (c === activeCategory ? " active" : "");
    btn.dataset.cat = c;
    btn.textContent = c === "" ? "✨ All" : `${catMeta(c).icon} ${catMeta(c).label}`;
    btn.addEventListener("click", () => selectCategory(c));
    nav.appendChild(btn);
  }
}

function selectCategory(c) {
  if (activeCategory === c) return;
  activeCategory = c;
  for (const b of document.querySelectorAll(".cat-pill")) {
    b.classList.toggle("active", b.dataset.cat === c);
  }
  loadDeals(true);
}

function fillSelect(sel, values) {
  for (const v of values || []) {
    const opt = document.createElement("option");
    opt.value = v;
    opt.textContent = v;
    sel.appendChild(opt);
  }
}

async function loadMe() {
  const el = $("me");
  try {
    const res = await fetch("/api/me");
    me = await res.json();
    el.innerHTML = "";
    if (me.authenticated) {
      const who = document.createElement("span");
      who.textContent = `Hi, ${me.displayName || me.userId} ✨ `;
      const out = document.createElement("button");
      out.className = "linkbtn";
      out.textContent = "Sign out";
      out.addEventListener("click", logout);
      el.append(who, out);
      $("watchlist-toggle").hidden = false;
      await loadWatchlist();
    } else {
      $("watchlist-toggle").hidden = true;
      if (me.oidcEnabled) {
        const a = document.createElement("a");
        a.className = "linkbtn";
        a.href = "/auth/login";
        a.textContent = "Sign in";
        el.append(a);
      }
    }
  } catch (err) {
    console.error("failed to load me", err);
  }
}

async function loadWatchlist() {
  try {
    const res = await fetch("/api/watchlist");
    if (!res.ok) return;
    const data = await res.json();
    watched.clear();
    for (const w of data.watches || []) watched.set(normalize(w.gameName), w.id);
    refreshWatchButtons();
    renderDrawer(data.watches || []);
  } catch (err) {
    console.error("failed to load watchlist", err);
  }
}

// renderDrawer paints the watchlist panel. Pass the raw list to avoid a
// re-fetch; called with no args it re-fetches.
let lastWatches = [];
function renderDrawer(watches) {
  if (watches) lastWatches = watches;
  const list = $("watch-list");
  list.innerHTML = "";
  $("watch-empty").hidden = lastWatches.length !== 0;
  for (const w of lastWatches) {
    const li = document.createElement("li");
    li.className = "watch-item";
    const name = document.createElement("span");
    name.textContent = w.gameName;
    const rm = document.createElement("button");
    rm.className = "watch-remove";
    rm.textContent = "✕";
    rm.title = "Remove";
    rm.addEventListener("click", () => removeWatchById(w.id));
    li.append(name, rm);
    list.appendChild(li);
  }
}

async function removeWatchById(id) {
  try {
    const res = await fetch("/api/watchlist?id=" + encodeURIComponent(id), { method: "DELETE" });
    if (!res.ok) throw new Error("remove failed: " + res.status);
  } catch (err) {
    console.error("remove watch failed", err);
  }
  await loadWatchlist();
}

async function logout() {
  try {
    await fetch("/auth/logout", { method: "POST" });
  } catch (err) {
    console.error("logout failed", err);
  }
  watched.clear();
  closeDrawer();
  loadMe();
}

function openDrawer() {
  $("drawer").hidden = false;
  $("drawer").setAttribute("aria-hidden", "false");
  $("drawer-scrim").hidden = false;
  loadWatchlist();
}
function closeDrawer() {
  $("drawer").hidden = true;
  $("drawer").setAttribute("aria-hidden", "true");
  $("drawer-scrim").hidden = true;
}

// Debounce filter changes so typing doesn't hammer the API.
let timer;
function onFilterChange() {
  clearTimeout(timer);
  timer = setTimeout(() => loadDeals(true), 250);
}

function init() {
  for (const id of ["q", "source", "store", "min_discount", "max_price", "hist_low", "free", "great", "sort"]) {
    const el = $(id);
    el.addEventListener(el.tagName === "SELECT" || el.type === "checkbox" ? "change" : "input", onFilterChange);
  }
  $("more").addEventListener("click", () => loadDeals(false));

  $("watchlist-toggle").addEventListener("click", openDrawer);
  $("drawer-close").addEventListener("click", closeDrawer);
  $("drawer-scrim").addEventListener("click", closeDrawer);
  $("watch-add").addEventListener("submit", async (e) => {
    e.preventDefault();
    const input = $("watch-input");
    const game = input.value.trim();
    if (!game) return;
    try {
      const res = await fetch("/api/watchlist", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ game }),
      });
      if (!res.ok) throw new Error("add failed: " + res.status);
      const r = input.getBoundingClientRect();
      burstConfetti(r.left + r.width / 2, r.top);
      boingMascot();
      input.value = "";
      await loadWatchlist();
    } catch (err) {
      console.error("add watch failed", err);
    }
  });

  // Mascot says hi when poked.
  const mascot = $("mascot");
  if (mascot) mascot.addEventListener("click", boingMascot);

  startBackground();
  loadMe();
  loadFacets();
  loadDeals(true);
}

init();
