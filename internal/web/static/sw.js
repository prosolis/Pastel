// Pastel service worker (Phase 5 PWA + Web Push).
//
// Responsibilities:
//   1. Cache the app shell so the gallery is installable and survives a flaky
//      connection (navigations fall back to the cached page; static assets are
//      served stale-while-revalidate).
//   2. Receive Web Push messages and surface them as notifications, and route a
//      click to the deal URL.
//
// API responses (/api/…) and auth (/auth/…) are never cached — they are
// per-user and must stay fresh.

"use strict";

const CACHE = "pastel-shell-v1";
const SHELL = [
  "/",
  "/index.html",
  "/style.css",
  "/app.js",
  "/manifest.webmanifest",
  "/pastel.webp",
  "/pastel.avif",
  "/icon-192.png",
  "/icon-512.png",
];

self.addEventListener("install", (event) => {
  // Pre-cache the shell, then take over without waiting for old tabs to close.
  event.waitUntil(
    caches.open(CACHE).then((c) => c.addAll(SHELL)).then(() => self.skipWaiting())
  );
});

self.addEventListener("activate", (event) => {
  // Drop caches from older shell versions, then control open clients.
  event.waitUntil(
    caches.keys()
      .then((keys) => Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k))))
      .then(() => self.clients.claim())
  );
});

self.addEventListener("fetch", (event) => {
  const req = event.request;
  if (req.method !== "GET") return;

  const url = new URL(req.url);
  if (url.origin !== self.location.origin) return;
  // Never cache dynamic, per-user responses.
  if (url.pathname.startsWith("/api/") || url.pathname.startsWith("/auth/")) return;

  // Navigations: try the network, fall back to the cached app shell offline so
  // the SPA still boots (it then fetches data when connectivity returns).
  if (req.mode === "navigate") {
    event.respondWith(fetch(req).catch(() => caches.match("/")));
    return;
  }

  // Static assets: serve from cache immediately and refresh in the background.
  event.respondWith(
    caches.open(CACHE).then((cache) =>
      cache.match(req).then((cached) => {
        const network = fetch(req)
          .then((res) => {
            if (res && res.ok) cache.put(req, res.clone());
            return res;
          })
          .catch(() => cached);
        return cached || network;
      })
    )
  );
});

// A push payload is the JSON {title, body, url} that the server encrypts.
self.addEventListener("push", (event) => {
  let data = {};
  try {
    data = event.data ? event.data.json() : {};
  } catch (e) {
    data = { title: "Pastel", body: event.data ? event.data.text() : "" };
  }
  const title = data.title || "Pastel deal";
  const options = {
    body: data.body || "",
    icon: "/icon-192.png",
    badge: "/icon-192.png",
    data: { url: data.url || "/" },
    tag: "pastel-deal",
  };
  event.waitUntil(self.registration.showNotification(title, options));
});

// Clicking a notification focuses an existing tab (navigating it to the deal) or
// opens a new one.
self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const target = (event.notification.data && event.notification.data.url) || "/";
  event.waitUntil(
    self.clients.matchAll({ type: "window", includeUncontrolled: true }).then((clients) => {
      for (const client of clients) {
        if ("focus" in client) {
          client.navigate(target);
          return client.focus();
        }
      }
      return self.clients.openWindow(target);
    })
  );
});
