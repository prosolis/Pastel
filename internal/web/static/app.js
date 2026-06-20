// Minimal frontend logic for the deal gallery. The animated "hella cute" pass
// (confetti, springy entrances, mascot, skeletons) lands in milestone M5.
"use strict";

const PAGE_SIZE = 48;
let offset = 0;
let total = 0;

const $ = (id) => document.getElementById(id);
const grid = $("grid");

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
    ${d.url ? `<a class="buy" href="${escapeAttr(d.url)}" target="_blank" rel="noopener">View deal</a>` : ""}
  `;
  return div;
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
  try {
    const res = await fetch("/api/me");
    const me = await res.json();
    $("me").textContent = me.authenticated
      ? `Hi, ${me.displayName || me.userId} ✨`
      : me.oidcEnabled
      ? "Not signed in"
      : "";
  } catch (err) {
    console.error("failed to load me", err);
  }
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

  loadMe();
  loadFacets();
  loadDeals(true);
}

init();
