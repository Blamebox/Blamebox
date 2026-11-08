"use strict";
// BlameBox dashboard. No framework, no external resource: it works offline on a home network.

const T = {
  fr: {
    loading: "Chargement…", demo: "DÉMO", demo_cta: "Démo avec des données simulées, figée pendant une coupure.", demo_join: "Rejoindre la liste d'attente →", report: "Rapport opérateur", report_short: "Rapport", l_box: "Box",
    k_gw: "Latence vers la box", k_net: "Latence vers Internet", k_loss: "Pertes vers Internet (10 min)",
    k_avail: "Disponibilité sur 7 jours", history: "Historique", r24: "24 h", r7: "7 jours",
    lg_ok: "En ligne", lg_deg: "Pertes", lg_down: "Coupure", lg_none: "Pas de mesure",
    incidents: "Incidents", export: "Exporter (CSV)", more: "Voir plus", less: "Voir moins",
    settings: "Comment BlameBox mesure", test: "Envoyer une alerte de test",
    st_ok: "En ligne", st_degraded: "Connexion dégradée", st_down: "Coupure en cours", st_loading: "Chargement…",
    since: "depuis", here: "c'est ici", unreachable: "ne répond pas", ago: "il y a", ongoing: "en cours",
    no_incident: "Aucun incident pour l'instant. BlameBox enregistre tout, 24 h/24.",
    p_isp: "Box joignable pendant {a} des {n} mesures en échec.", p_dns: "DNS publics joignables pendant {a} des {n} mesures.",
    p_lan: "Box injoignable depuis la sonde.", p_web: "Web injoignable alors que les noms sont traduits.",
    p_dnsall: "Aucun DNS ne répond.", p_loss: "Pertes moyennes : {a} vers Internet, {b} vers la box.",
    p_ip: "Nouvelle IP publique ensuite.", p_int: "Surveillance interrompue pendant l'incident.",
    lat: "Latence (ms)", loss: "Pertes vers Internet (%)", box: "Box", net: "Internet",
    i_method: "Méthode de ping", i_gw: "Box (passerelle)", i_dnsl: "DNS de la box", i_dnsp: "DNS publics",
    i_targets: "Serveurs testés", i_web: "Test web", i_every: "Fréquence", i_keep: "Conservation", i_version: "Version",
    every: "toutes les {n} s", days: "{n} jours", disabled: "désactivé (la box ne fait pas DNS)", none_auto: "détectée automatiquement",
    alerts_none: "Aucune alerte configurée (ntfy, Telegram ou webhook : voir le README).", alerts_on: "Alertes : {c}.",
    pending: "{n} alerte(s) en attente d'envoi.", sent_ok: "Envoyée ✓", sent_err: "Échec : {e}",
    w_ping_tcp: "Le ping ICMP n'est pas autorisé ici : BlameBox mesure par TCP, de façon moins précise. Lance-le en root ou avec la capacité NET_RAW.",
    w_dns_local_disabled: "Ta box ne semble pas faire serveur DNS : ce test est désactivé.",
    w_docker_bridge: "BlameBox tourne dans le réseau isolé de Docker : il mesure Docker, pas ta box. Ajoute « network_mode: host » dans docker-compose.yml.",
    w_wifi: "La sonde est connectée en Wi-Fi : les mesures incluent ton Wi-Fi. Branche-la en câble sur la box pour un diagnostic fiable.",
    w_stale: "Aucune mesure récente : BlameBox est-il bloqué ?", w_no_data: "Première mesure en cours…",
    w_offline: "Impossible de joindre BlameBox. La page se remet à jour toute seule dès qu'il répond.",
    tcp: "TCP (repli)", icmp: "ICMP", "icmp-dg": "ICMP sans privilège",
  },
  en: {
    loading: "Loading…", demo: "DEMO", demo_cta: "Demo with simulated data, frozen during an outage.", demo_join: "Join the waitlist →", report: "ISP report", report_short: "Report", l_box: "Router",
    k_gw: "Latency to the router", k_net: "Latency to the Internet", k_loss: "Loss to the Internet (10 min)",
    k_avail: "Availability over 7 days", history: "History", r24: "24 h", r7: "7 days",
    lg_ok: "Online", lg_deg: "Loss", lg_down: "Outage", lg_none: "No data",
    incidents: "Incidents", export: "Export (CSV)", more: "Show more", less: "Show less",
    settings: "How BlameBox measures", test: "Send a test alert",
    st_ok: "Online", st_degraded: "Degraded connection", st_down: "Outage in progress", st_loading: "Loading…",
    since: "since", here: "here", unreachable: "not answering", ago: "ago", ongoing: "ongoing",
    no_incident: "No incident yet. BlameBox records everything, around the clock.",
    p_isp: "Router reachable during {a} of {n} failed checks.", p_dns: "Public DNS reachable during {a} of {n} checks.",
    p_lan: "Router unreachable from the probe.", p_web: "Web unreachable while names resolve.",
    p_dnsall: "No DNS answers.", p_loss: "Average loss: {a} to the Internet, {b} to the router.",
    p_ip: "New public IP afterwards.", p_int: "Monitoring interrupted during the incident.",
    lat: "Latency (ms)", loss: "Loss to the Internet (%)", box: "Router", net: "Internet",
    i_method: "Ping method", i_gw: "Router (gateway)", i_dnsl: "Router DNS", i_dnsp: "Public DNS",
    i_targets: "Tested servers", i_web: "Web check", i_every: "Frequency", i_keep: "Retention", i_version: "Version",
    every: "every {n} s", days: "{n} days", disabled: "disabled (the router has no DNS server)", none_auto: "detected automatically",
    alerts_none: "No alert configured (ntfy, Telegram or webhook: see the README).", alerts_on: "Alerts: {c}.",
    pending: "{n} alert(s) waiting to be sent.", sent_ok: "Sent ✓", sent_err: "Failed: {e}",
    w_ping_tcp: "ICMP ping is not allowed here: BlameBox measures over TCP, less precisely. Run it as root or with the NET_RAW capability.",
    w_dns_local_disabled: "Your router does not seem to run a DNS server: this check is disabled.",
    w_docker_bridge: "BlameBox runs in Docker's isolated network: it measures Docker, not your router. Add \"network_mode: host\" to docker-compose.yml.",
    w_wifi: "The probe is on Wi-Fi: measurements include your Wi-Fi. Wire it to the router for a reliable diagnosis.",
    w_stale: "No recent measurement: is BlameBox stuck?", w_no_data: "First measurement in progress…",
    w_offline: "Cannot reach BlameBox. The page refreshes by itself as soon as it answers.",
    tcp: "TCP (fallback)", icmp: "ICMP", "icmp-dg": "Unprivileged ICMP",
  },
};

const COLORS = { isp: "#FF4D6D", dns_isp: "#F5A524", dns_all: "#F5A524", web: "#A855F7", lan: "#9AA4B8", loss_isp: "#FB923C", loss_lan: "#9AA4B8" };

const $ = (id) => document.getElementById(id);
const store = {
  get(k) { try { return localStorage.getItem(k); } catch (e) { return null; } },
  set(k, v) { try { localStorage.setItem(k, v); } catch (e) { /* private mode */ } },
};

let lang = store.get("bb_lang") || ((navigator.language || "fr").toLowerCase().startsWith("fr") ? "fr" : "en");
let range = Number(store.get("bb_range")) || 24;
let snap = null, series = null, incidents = [], showAll = false, offline = false;
// A frozen copy of the dashboard (the public demo) reads plain files instead of the API.
const STATIC = document.body.dataset.mode === "static";
const URLS = STATIC
  ? { status: "status.json", series: (h) => "samples-" + h + ".json", incidents: "incidents.json", report: (l) => "report-" + l + ".html", csv: "incidents.csv" }
  : { status: "api/status", series: (h) => "api/samples?hours=" + h + "&points=" + (h <= 24 ? 288 : 336), incidents: "api/incidents?days=30", report: (l) => "report?lang=" + l, csv: "api/export/incidents.csv" };
const nowMs = () => (STATIC && snap ? Date.parse(snap.now) : Date.now());

function t(key, vars) {
  let s = (T[lang] && T[lang][key]) || T.fr[key] || key;
  if (vars) for (const k in vars) s = s.split("{" + k + "}").join(vars[k]);
  return s;
}

function el(tag, attrs, ...kids) {
  const e = document.createElement(tag);
  for (const k in attrs || {}) {
    if (k === "class") e.className = attrs[k];
    else if (k === "style") e.style.cssText = attrs[k];
    else e.setAttribute(k, attrs[k]);
  }
  for (const c of kids) if (c != null) e.append(c);
  return e;
}

const nf = () => new Intl.NumberFormat(lang === "fr" ? "fr-FR" : "en-GB", { maximumFractionDigits: 1 });
const ms = (v) => (v == null || v < 0 ? "–" : nf().format(v < 10 ? Math.round(v * 10) / 10 : Math.round(v)) + " ms");
const pct = (v, d = 1) => (v == null || v < 0 ? "–" : new Intl.NumberFormat(lang === "fr" ? "fr-FR" : "en-GB", { maximumFractionDigits: d }).format(v * 100) + " %");
const hm = (d) => new Date(d).toLocaleTimeString(lang === "fr" ? "fr-FR" : "en-GB", { hour: "2-digit", minute: "2-digit" });
const day = (d) => new Date(d).toLocaleDateString(lang === "fr" ? "fr-FR" : "en-GB", { weekday: "short", day: "numeric", month: "short" });

function dur(s) {
  s = Math.max(0, Math.round(s));
  if (s < 60) return s + " s";
  if (s < 3600) return Math.floor(s / 60) + " min";
  const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60);
  if (h < 48) return h + " h " + String(m).padStart(2, "0");
  return Math.floor(h / 24) + (lang === "fr" ? " jours" : " days");
}

function sinceText(iso) {
  const d = new Date(iso);
  const s = (nowMs() - d.getTime()) / 1000;
  const sameDay = new Date(nowMs()).toDateString() === d.toDateString();
  return t("since") + " " + (sameDay ? hm(d) : day(d) + " " + hm(d)) + " · " + dur(s);
}

function applyStatic() {
  document.documentElement.lang = lang;
  document.querySelectorAll("[data-t]").forEach((e) => { e.textContent = t(e.dataset.t); });
  $("lang").textContent = lang === "fr" ? "EN" : "FR";
  $("reportLink").href = URLS.report(lang);
  $("exportLink").href = URLS.csv;
  document.querySelectorAll(".seg button").forEach((b) => b.classList.toggle("on", Number(b.dataset.range) === range));
  $("more").textContent = showAll ? t("less") : t("more");
}

// ---- status ----

function layerMetric(layer, s) {
  if (!s) return "";
  switch (layer) {
    case "box": return s.gw.r > 0 ? ms(s.gw.rtt) : (s.gw.e || "–");
    case "internet": {
      let best = -1;
      for (const p of s.inet || []) if (p.r > 0 && (best < 0 || p.rtt < best)) best = p.rtt;
      return best >= 0 ? ms(best) : ((s.inet && s.inet[0] && s.inet[0].e) || "–");
    }
    case "dns": {
      const d = s.dnsl && !s.dnsl.skip ? s.dnsl : (s.dnsp || []).find((x) => x.ok) || (s.dnsp || [])[0];
      return d ? (d.ok ? ms(d.ms) : (d.e || "–")) : "–";
    }
    case "web": return s.web ? (s.web.ok ? ms(s.web.ms) : (s.web.e || "–")) : "–";
  }
  return "";
}

function renderStatus() {
  if (!snap) return;
  const d = snap.diag, tx = snap.texts[lang] || snap.texts.fr;
  const st = snap.last ? d.s : "loading";
  $("hero").dataset.status = st;
  $("pillText").textContent = t("st_" + st);
  const from = snap.outage ? snap.outage.start : (snap.degradation && d.s === "degraded" ? snap.degradation.start : snap.since);
  $("since").textContent = snap.last && from ? sinceText(from) : "";
  $("title").textContent = snap.last ? tx.title : "BlameBox";
  $("body").textContent = snap.last ? tx.body : t("w_no_data");
  $("advice").textContent = tx.advice || "";
  $("advice").hidden = !tx.advice || d.s === "ok";
  for (const li of $("chain").children) {
    const layer = li.dataset.layer;
    li.dataset.state = snap.last ? d.l[layer] : "";
    li.querySelector(".nm").dataset.here = t("here");
    const sub = li.querySelector(".sub"), raw = layerMetric(layer, snap.last);
    sub.textContent = d.l[layer] === "skip" ? "–" : d.l[layer] === "fail" ? t("unreachable") : raw;
    sub.title = d.l[layer] === "fail" ? raw : "";
  }
  const s = snap.last;
  $("k-gw").textContent = s && s.gw.r > 0 ? ms(s.gw.rtt) : "–";
  let best = -1;
  for (const p of (s && s.inet) || []) if (p.r > 0 && (best < 0 || p.rtt < best)) best = p.rtt;
  $("k-net").textContent = ms(best);
  $("k-loss").textContent = snap.window && snap.window.n >= 4 ? pct(snap.window.net_loss) : "–";
  $("k-avail").textContent = s ? pct(snap.stats_7d.availability, 2) : "–";
  $("demo").hidden = !snap.info.demo;
  $("version").textContent = snap.version;
  document.title = (st === "down" ? "⚠ " : "") + (snap.last ? tx.title + " · " : "") + "BlameBox";

  const warn = offline ? ["offline"] : (snap.warnings || []);
  $("banner").hidden = warn.length === 0;
  $("banner").textContent = warn.map((w) => t("w_" + w)).join(" ");
  renderInfo();
}

function renderInfo() {
  const i = snap.info, dl = $("info");
  dl.replaceChildren();
  const add = (k, v) => { dl.append(el("dt", {}, t(k)), el("dd", {}, v)); };
  add("i_method", t(i.ping_method));
  add("i_gw", i.gateway ? i.gateway + (i.iface ? " (" + i.iface + ")" : "") : t("none_auto"));
  add("i_dnsl", i.dns_local_disabled ? t("disabled") : (i.dns_local || "–"));
  add("i_dnsp", (i.dns_public || []).join(", "));
  add("i_targets", (i.targets || []).join(", "));
  add("i_web", (i.web_urls || []).join(" · "));
  add("i_every", t("every", { n: snap.interval_s }));
  add("i_keep", t("days", { n: snap.retention_days }));
  add("i_version", snap.version);
  const ch = snap.channels || [];
  let txt = ch.length ? t("alerts_on", { c: ch.join(", ") }) : t("alerts_none");
  if (snap.pending_alerts) txt += " " + t("pending", { n: snap.pending_alerts });
  $("alertsText").textContent = txt;
  $("testAlert").hidden = ch.length === 0;
}

// ---- history ----

function renderSeries() {
  if (!series) return;
  const strip = $("strip"), cls = ["ok", "degraded", "down"];
  strip.replaceChildren();
  series.st.forEach((v, i) => {
    const c = v < 0 ? "none" : cls[v];
    const label = hm(series.t[i] * 1000) + " · " + t(v < 0 ? "lg_none" : ["lg_ok", "lg_deg", "lg_down"][v]);
    strip.append(el("i", { class: c, title: label }));
  });
  strip.setAttribute("aria-label", t("history"));
  // Axis labels.
  const axis = $("stripAxis");
  axis.replaceChildren();
  for (const tk of ticks()) axis.append(el("span", { style: "left:" + (tk.f * 100) + "%" }, tk.label));
  drawChart();
}

// ticks returns evenly spaced time labels: every 3 hours over 24 h, every day over 7 days.
function ticks() {
  const n = series.t.length, out = [];
  if (!n) return out;
  const start = series.t[0] * 1000, end = (series.t[n - 1] + series.bucket_s) * 1000;
  const c = new Date(start);
  if (range <= 24) c.setMinutes(0, 0, 0); else c.setHours(0, 0, 0, 0);
  const every = $("strip").clientWidth < 520 ? 6 : 3; // fewer labels on a phone
  for (let x = c.getTime(); x < end; x += 3600e3) {
    const d = new Date(x);
    if (range <= 24 ? d.getHours() % every !== 0 : d.getHours() !== 0) continue;
    const f = (x - start) / (end - start);
    if (f < 0.03 || f > 0.97) continue;
    out.push({ f, label: range <= 24 ? hm(x) : day(x) });
  }
  return out;
}

function drawChart() {
  // Draw at the real width so the text keeps its size on a phone.
  const W = Math.max(320, Math.min(960, $("chart").clientWidth || 960)), H = 236, L = 36, R = 6, top = 22, mid = 140, bot = 214;
  const n = series.t.length;
  if (!n) return;
  const x = (i) => L + ((W - L - R) * i) / Math.max(1, n - 1);
  const vals = series.net_rtt.concat(series.gw_rtt).filter((v) => v >= 0).sort((a, b) => a - b);
  let maxR = vals.length ? vals[Math.floor(vals.length * 0.98)] : 10;
  maxR = Math.max(5, Math.ceil(maxR / 10) * 10);
  const yR = (v) => mid - ((mid - top) * Math.min(v, maxR)) / maxR;
  const lossVals = series.net_loss.filter((v) => v >= 0);
  let maxL = Math.max(0.02, ...lossVals);
  maxL = Math.ceil(maxL * 50) / 50;
  const yL = (v) => bot - ((bot - (mid + 22)) * Math.min(v, maxL)) / maxL;
  const path = (arr, y) => {
    let d = "", pen = false;
    arr.forEach((v, i) => {
      if (v < 0) { pen = false; return; }
      d += (pen ? "L" : "M") + x(i).toFixed(1) + " " + y(v).toFixed(1);
      pen = true;
    });
    return d;
  };
  let svg = `<svg viewBox="0 0 ${W} ${H}" role="img" aria-label="${t("lat")}">`;
  for (let k = 0; k <= 2; k++) {
    const v = (maxR * k) / 2, yy = yR(v);
    svg += `<line x1="${L}" x2="${W - R}" y1="${yy}" y2="${yy}" stroke="#26314F"/><text class="t" x="${L - 6}" y="${yy + 4}" text-anchor="end">${Math.round(v)}</text>`;
  }
  svg += `<text class="t" x="${L}" y="12">${t("lat")}</text>`;
  svg += `<rect x="${W - 150}" y="5" width="12" height="3" fill="#FFE135"/><text class="t" x="${W - 134}" y="11">${t("net")}</text>`;
  svg += `<rect x="${W - 66}" y="5" width="12" height="3" fill="#40D6BA"/><text class="t" x="${W - 50}" y="11">${t("box")}</text>`;
  svg += `<path d="${path(series.gw_rtt, yR)}" fill="none" stroke="#40D6BA" stroke-width="1.6"/>`;
  svg += `<path d="${path(series.net_rtt, yR)}" fill="none" stroke="#FFE135" stroke-width="1.8"/>`;
  // Outages shaded on the latency panel.
  series.st.forEach((v, i) => {
    if (v === 2) svg += `<rect x="${(x(i) - (W - L - R) / n / 2).toFixed(1)}" y="${top}" width="${((W - L - R) / n + 0.5).toFixed(1)}" height="${mid - top}" fill="#FF4D6D" opacity=".25"/>`;
  });
  svg += `<text class="t" x="${L}" y="${mid + 16}">${t("loss")}</text>`;
  svg += `<line x1="${L}" x2="${W - R}" y1="${bot}" y2="${bot}" stroke="#26314F"/><text class="t" x="${L - 6}" y="${yL(maxL) + 4}" text-anchor="end">${Math.round(maxL * 100)}</text>`;
  const bw = Math.max(1, (W - L - R) / n - 1);
  series.net_loss.forEach((v, i) => {
    if (v > 0) svg += `<rect x="${(x(i) - bw / 2).toFixed(1)}" y="${yL(v).toFixed(1)}" width="${bw.toFixed(1)}" height="${(bot - yL(v)).toFixed(1)}" fill="#FB923C"/>`;
  });
  for (const tk of ticks()) {
    const xx = (L + (W - L - R) * tk.f).toFixed(1);
    svg += `<line x1="${xx}" x2="${xx}" y1="${bot}" y2="${bot + 4}" stroke="#4A5674"/><text class="t" x="${xx}" y="${H - 4}" text-anchor="middle">${tk.label}</text>`;
  }
  svg += "</svg>";
  $("chart").innerHTML = svg; // built from numbers and our own labels only
}

// ---- incidents ----

function proof(i) {
  const e = i.evidence || {}, parts = [];
  switch (i.cause) {
    case "isp": parts.push(t("p_isp", { a: e.gw_ok, n: i.cycles })); break;
    case "dns_isp": parts.push(t("p_dns", { a: e.pub_dns_ok, n: i.cycles })); break;
    case "dns_all": parts.push(t("p_dnsall")); break;
    case "web": parts.push(t("p_web")); break;
    case "lan": parts.push(t("p_lan")); break;
    default: parts.push(t("p_loss", { a: pct(e.inet_loss_avg), b: pct(e.gw_loss_avg) }));
  }
  if (e.ip_before && e.ip_after && e.ip_before !== e.ip_after) parts.push(t("p_ip"));
  if (i.note === "interrupted") parts.push(t("p_int"));
  return parts.join(" ");
}

function renderIncidents() {
  const ul = $("incidents");
  ul.replaceChildren();
  if (!incidents.length) {
    ul.append(el("li", { class: "empty" }, t("no_incident")));
    $("more").hidden = true;
    return;
  }
  const list = showAll ? incidents : incidents.slice(0, 8);
  for (const i of list) {
    const tx = i.texts[lang] || i.texts.fr;
    const home = i.cause === "lan" || i.cause === "loss_lan";
    const when = day(i.start) + " · " + hm(i.start) + " → " + (i.ongoing ? t("ongoing") : hm(i.end));
    ul.append(el("li", { class: i.ongoing ? "ongoing" : "" },
      el("span", { class: "dot", style: "background:" + (COLORS[i.cause] || "#9AA4B8") }),
      el("div", {}, el("div", { class: "what" }, tx.short), el("div", { class: "when" }, when)),
      el("div", { class: "right" }, el("div", { class: "dur" }, dur(i.duration_s)), el("span", { class: "badge " + (home ? "home" : "isp") }, tx.side)),
      el("div", { class: "proof" }, proof(i))));
  }
  $("more").hidden = incidents.length <= 8;
}

// ---- data loading ----

async function get(url) {
  const r = await fetch(url, { cache: "no-store" });
  if (!r.ok) throw new Error(r.status);
  return r.json();
}

async function loadStatus() {
  try {
    snap = await get(URLS.status);
    offline = false;
  } catch (e) {
    offline = true;
  }
  renderStatus();
  setTimeout(loadStatus, Math.max(5, Math.min(30, (snap ? snap.interval_s : 30) / 2)) * 1000);
}

async function loadSeries() {
  try { series = await get(URLS.series(range)); renderSeries(); } catch (e) { /* retried */ }
}

async function loadIncidents() {
  try { incidents = await get(URLS.incidents); renderIncidents(); } catch (e) { /* retried */ }
}

$("lang").addEventListener("click", () => {
  lang = lang === "fr" ? "en" : "fr";
  store.set("bb_lang", lang);
  applyStatic(); renderStatus(); renderSeries(); renderIncidents();
});
document.querySelectorAll(".seg button").forEach((b) => b.addEventListener("click", () => {
  range = Number(b.dataset.range);
  store.set("bb_range", String(range));
  applyStatic(); loadSeries();
}));
$("more").addEventListener("click", () => { showAll = !showAll; applyStatic(); renderIncidents(); });
$("testAlert").addEventListener("click", async () => {
  $("testResult").textContent = "…";
  try {
    const r = await fetch("api/test-alert", { method: "POST", headers: { "X-BlameBox": "1" } });
    const res = await r.json();
    $("testResult").textContent = Object.entries(res).map(([k, v]) => k + " : " + (v === "ok" ? t("sent_ok") : t("sent_err", { e: v }))).join(" · ");
  } catch (e) {
    $("testResult").textContent = t("sent_err", { e: String(e) });
  }
});

$("demoCta").hidden = !STATIC;
applyStatic();
loadStatus();
loadSeries();
loadIncidents();
setInterval(loadSeries, 60000);
setInterval(loadIncidents, 30000);
let resizeTimer;
window.addEventListener("resize", () => { clearTimeout(resizeTimer); resizeTimer = setTimeout(renderSeries, 200); });
