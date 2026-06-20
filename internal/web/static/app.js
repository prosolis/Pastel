// Frontend logic for the deal gallery + watchlist. The animated "hella cute"
// pass (confetti, springy entrances, mascot, skeletons, WebGL background) lands
// in milestone M5.
"use strict";

const PAGE_SIZE = 48;
let offset = 0;
let total = 0;

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
  if ($("source").value) p.set("source", $("source").value);
  if ($("store").value) p.set("store", $("store").value);
  if ($("min_discount").value) p.set("min_discount", $("min_discount").value);
  if ($("max_price").value) p.set("max_price", $("max_price").value);
  if ($("hist_low").checked) p.set("hist_low", "1");
  if ($("free").checked) p.set("free", "1");
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
  if (d.isHistLow) badges.push(`<span class="badge low">★ historical low</span>`);
  if (d.upcoming) badges.push(`<span class="badge">upcoming</span>`);

  const price = d.isFree
    ? `<div class="price">Free</div>`
    : `<div class="price">${money(d.salePrice)}${
        d.normalPrice > d.salePrice ? `<span class="normal">${money(d.normalPrice)}</span>` : ""
      }</div>`;

  const div = document.createElement("div");
  div.className = "card";
  div.innerHTML = `
    <div class="badges">${badges.join("")}</div>
    <h3>${escapeHTML(d.title)}</h3>
    <div class="meta">${escapeHTML(d.store || d.source)}${d.rating ? ` · ★ ${d.rating}` : ""}</div>
    ${price}
    <div class="card-actions">
      ${d.url ? `<a class="buy" href="${escapeAttr(d.url)}" target="_blank" rel="noopener">View deal</a>` : ""}
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
      await fetch("/api/watchlist?id=" + encodeURIComponent(watched.get(norm)), { method: "DELETE" });
      watched.delete(norm);
    } else {
      await fetch("/api/watchlist", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ game: title }),
      });
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

async function loadDeals(reset) {
  if (reset) {
    offset = 0;
    grid.innerHTML = "";
  }
  try {
    const res = await fetch("/api/deals?" + currentParams().toString());
    const data = await res.json();
    total = data.total || 0;
    for (const d of data.deals) grid.appendChild(cardHTML(d));
    offset += data.deals.length;

    $("count").textContent = total ? `${total} deal${total === 1 ? "" : "s"}` : "";
    $("empty").hidden = total !== 0;
    $("more").hidden = offset >= total;
  } catch (err) {
    console.error("failed to load deals", err);
    $("empty").hidden = false;
    $("empty").textContent = "Couldn't load deals 😢";
  }
}

async function loadFacets() {
  try {
    const res = await fetch("/api/facets");
    const data = await res.json();
    fillSelect($("source"), data.sources);
    fillSelect($("store"), data.stores);
  } catch (err) {
    console.error("failed to load facets", err);
  }
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
    await fetch("/api/watchlist?id=" + encodeURIComponent(id), { method: "DELETE" });
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
  for (const id of ["q", "source", "store", "min_discount", "max_price", "hist_low", "free", "sort"]) {
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
      await fetch("/api/watchlist", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ game }),
      });
      input.value = "";
      await loadWatchlist();
    } catch (err) {
      console.error("add watch failed", err);
    }
  });

  loadMe();
  loadFacets();
  loadDeals(true);
}

init();
