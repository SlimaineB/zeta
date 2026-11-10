"use strict";

const els = {
  loginView: document.getElementById("login-view"),
  appView: document.getElementById("app-view"),
  who: document.getElementById("who"),
  logout: document.getElementById("logout"),
  themeToggle: document.getElementById("theme-toggle"),
  buildsBody: document.getElementById("builds-body"),
  summary: document.getElementById("summary"),
  durationChart: document.getElementById("duration-chart"),
  cacheChart: document.getElementById("cache-chart"),
  refresh: document.getElementById("refresh"),
  lastRefresh: document.getElementById("last-refresh"),
  logsDialog: document.getElementById("logs-dialog"),
  logsTitle: document.getElementById("logs-title"),
  logs: document.getElementById("logs"),
  logsClose: document.getElementById("logs-close"),
  volumesBody: document.getElementById("volumes-body"),
  volumeQuota: document.getElementById("volume-quota"),
  volumeQuotaBar: document.getElementById("volume-quota-bar"),
  volumeError: document.getElementById("volume-error"),
  volumesRefresh: document.getElementById("volumes-refresh"),
  resizeDialog: document.getElementById("resize-dialog"),
  resizeForm: document.getElementById("resize-form"),
  resizeName: document.getElementById("resize-name"),
  resizeCurrent: document.getElementById("resize-current"),
  resizeSize: document.getElementById("resize-size"),
  resizeError: document.getElementById("resize-error"),
  resizeClose: document.getElementById("resize-close"),
  resizeCancel: document.getElementById("resize-cancel"),
  resizeSubmit: document.getElementById("resize-submit"),
};

const state = {
  me: null,
  builds: [],
  metrics: new Map(),
  volumes: [],
  volumeQuota: null,
  resizeVolume: null,
  logAbort: null,
  poll: null,
};

async function api(path, opts = {}) {
  const res = await fetch(path, {credentials: "same-origin", ...opts});
  if (res.status === 401) {
    location.href = "/login";
    throw new Error("not authenticated");
  }
  if (!res.ok) {
    const text = await res.text();
    let message = text;
    try { message = JSON.parse(text).error || text; } catch {}
    throw new Error(message || res.statusText);
  }
  return res;
}

async function loadMe() {
  try {
    state.me = await (await api("/api/me")).json();
    els.who.textContent = state.me.email || state.me.username || "";
    els.logout.hidden = false;
    return true;
  } catch {
    return false;
  }
}

async function loadBuilds() {
  try {
    state.builds = await (await api("/api/builds")).json();
    await loadMetrics(state.builds);
    render();
    els.lastRefresh.textContent = "updated " + new Date().toLocaleTimeString("en-US", {hour: "2-digit", minute: "2-digit", second: "2-digit"});
  } catch (err) {
    els.buildsBody.innerHTML = `<tr><td colspan="6"><p class="error">${escapeHTML(err.message)}</p></td></tr>`;
  }
}

async function loadVolumes() {
  try {
    const data = await (await api("/api/volumes")).json();
    state.volumes = data.volumes || [];
    state.volumeQuota = data.quota || null;
    els.volumeError.hidden = true;
    renderVolumes();
  } catch (err) {
    els.volumeError.textContent = err.message;
    els.volumeError.hidden = false;
  }
}

async function loadMetrics(builds) {
  const pending = builds.filter((b) => b.status === "succeeded" && !state.metrics.has(b.id));
  if (!pending.length) return;
  await Promise.allSettled(pending.map(async (b) => {
    state.metrics.set(b.id, null);
    try {
      const m = await (await api(`/api/builds/${encodeURIComponent(b.id)}/metrics`)).json();
      state.metrics.set(b.id, m || {});
    } catch {
      state.metrics.set(b.id, {});
    }
  }));
}

function render() {
  renderSummary();
  renderDurationChart();
  renderCacheChart();
  renderBuilds();
}

function renderVolumes() {
  const quota = state.volumeQuota;
  if (quota && quota.maxTotalBytes > 0) {
    const pct = Math.min(100, Math.round((quota.usedBytes / quota.maxTotalBytes) * 100));
    els.volumeQuotaBar.hidden = false;
    els.volumeQuotaBar.dataset.tone = pct >= 95 ? "bad" : (pct >= 80 ? "warn" : "");
    els.volumeQuotaBar.firstElementChild.style.width = pct + "%";
    els.volumeQuota.textContent = `${formatBytes(quota.usedBytes)} / ${formatBytes(quota.maxTotalBytes)} · ${formatBytes(quota.remainingBytes)} free · max ${formatBytes(quota.maxVolumeBytes)}/volume`;
  } else {
    els.volumeQuotaBar.hidden = true;
    els.volumeQuota.textContent = "Unlimited quota";
  }
  if (!state.volumes.length) {
    els.volumesBody.innerHTML = `<tr><td colspan="6"><div class="empty compact"><strong>No volumes</strong><span>Volumes will appear after the first build.</span></div></td></tr>`;
    return;
  }
  els.volumesBody.innerHTML = state.volumes.map((v) => {
    const last = v.lastUsedAt ? `${timeAgo(v.lastUsedAt)} <span class="muted small">(${new Date(v.lastUsedAt).toLocaleString("en-US")})</span>` : '<span class="muted small">never</span>';
    const resizeTitle = v.inUse ? "Build in progress" : (!v.resizable ? "StorageClass does not allow volume expansion" : "");
    return `<tr>
      <td><strong class="mono">${escapeHTML(v.name)}</strong></td>
      <td><span class="chip">${escapeHTML(v.role)}</span></td>
      <td>${formatBytes(v.capacityBytes)}</td>
      <td>${last}</td>
      <td class="mono small">${escapeHTML(v.lastBuild || "—")}</td>
      <td class="actions-cell">
        <button class="ghost small" data-resize="${escapeHTML(v.name)}" ${v.inUse || !v.resizable ? "disabled" : ""} title="${escapeHTML(resizeTitle)}">Expand</button>
        <button class="ghost small danger" data-delete="${escapeHTML(v.name)}" ${v.inUse ? "disabled" : ""} title="${v.inUse ? "Build in progress" : ""}">Delete</button>
      </td>
    </tr>`;
  }).join("");
}

function openResize(name) {
  const volume = state.volumes.find((v) => v.name === name);
  if (!volume || volume.inUse || !volume.resizable) return;
  state.resizeVolume = volume;
  els.resizeName.textContent = volume.name;
  els.resizeCurrent.textContent = formatBytes(volume.capacityBytes);
  els.resizeSize.value = "";
  els.resizeError.hidden = true;
  els.resizeSubmit.disabled = false;
  els.resizeDialog.showModal();
  els.resizeSize.focus();
}

async function resizeVolume(name, size) {
  await api(`/api/volumes/${encodeURIComponent(name)}/resize`, {
    method: "POST",
    headers: {"Content-Type": "application/json"},
    body: JSON.stringify({size}),
  });
}

async function deleteVolume(name) {
  if (!confirm(`Delete volume ${name}? All data will be lost.`)) return;
  await api(`/api/volumes/${encodeURIComponent(name)}`, {method: "DELETE"});
}

function renderSummary() {
  const builds = state.builds;
  const total = builds.length;
  const running = builds.filter((b) => b.status === "running" || b.status === "pending").length;
  const succeeded = builds.filter((b) => b.status === "succeeded").length;
  const failed = builds.filter((b) => b.status === "failed").length;
  const finished = succeeded + failed;
  const successRate = finished ? Math.round((succeeded / finished) * 100) : 0;
  const durations = builds.filter((b) => b.status === "succeeded" && b.durationMs > 0).map((b) => b.durationMs);
  const avgDuration = durations.length ? durations.reduce((a, b) => a + b, 0) / durations.length : 0;
  const hitRates = [...state.metrics.values()].filter((m) => m && m.hitRate > 0).map((m) => m.hitRate);
  const avgHitRate = hitRates.length ? Math.round(hitRates.reduce((a, b) => a + b, 0) / hitRates.length) : 0;

  const cards = [
    {label: "Builds", value: total, hint: "total", tone: ""},
    {label: "Running", value: running, hint: running ? "pending / running" : "none", tone: running ? "warn" : ""},
    {label: "Success", value: finished ? successRate + "%" : "—", hint: `${succeeded} succeeded · ${failed} failed`, tone: finished ? (successRate >= 80 ? "ok" : "bad") : ""},
    {label: "Average duration", value: avgDuration ? formatDuration(avgDuration) : "—", hint: "successful builds", tone: ""},
    {label: "Cache hit", value: avgHitRate ? avgHitRate + "%" : "—", hint: "ccache average", tone: avgHitRate >= 70 ? "ok" : ""},
  ];
  els.summary.innerHTML = cards.map((c) => `<div class="stat"${c.tone ? ` data-tone="${c.tone}"` : ""}>
    <div class="stat-label">${escapeHTML(c.label)}</div>
    <div class="stat-value">${escapeHTML(String(c.value))}</div>
    <div class="stat-hint">${escapeHTML(c.hint)}</div>
  </div>`).join("");
}

function renderBuilds() {
  if (!state.builds.length) {
    els.buildsBody.innerHTML = `<tr><td colspan="6"><div class="empty compact"><strong>No builds</strong><span>Start one with <code>zeta build</code>.</span></div></td></tr>`;
    return;
  }
  els.buildsBody.innerHTML = state.builds.map(renderBuildRow).join("");
  els.buildsBody.querySelectorAll("[data-logs]").forEach((btn) => {
    btn.addEventListener("click", () => openLogs(btn.dataset.logs));
  });
}

function renderBuildRow(b) {
  const metrics = state.metrics.get(b.id);
  const badge = `<span class="badge ${escapeHTML(b.status)}">${escapeHTML(statusLabel(b.status))}</span>`;
  const artifact = b.status === "succeeded"
    ? `<a class="button small ghost" href="/api/builds/${encodeURIComponent(b.id)}/artifact">Artifacts</a>`
    : "";
  const duration = b.durationMs ? formatDuration(b.durationMs) : (b.status === "succeeded" ? "<1 s" : "—");
  const init = b.initDurationMs ? formatDuration(b.initDurationMs) : (b.status === "succeeded" ? "<1 s" : "—");
  const build = b.buildDurationMs ? formatDuration(b.buildDurationMs) : (b.status === "succeeded" ? "<1 s" : "—");
  const progress = b.status === "running" || b.status === "pending" ? '<div class="progress"><span></span></div>' : "";
  const profile = b.profile || b.language || "build";
  const repo = b.repo || "unknown repository";
  const commit = b.commit || "HEAD";
  const cache = renderCache(metrics);
  const created = timeAgo(b.createdAt);

  return `<tr class="status-${escapeHTML(b.status)}" title="${escapeHTML(b.id)} · created ${escapeHTML(created)}">
    <td>
      <div class="build-name">
        <span class="dot"></span>
        <div>
          <strong>${escapeHTML(profile)}</strong>
          <span class="muted small mono">${escapeHTML(b.id)}</span>
        </div>
      </div>
    </td>
    <td>
      <div class="repo-cell" title="${escapeHTML(repo)} @ ${escapeHTML(commit)}">
        <span class="repo-url">${escapeHTML(repo)}</span>
        <span class="muted small mono">${escapeHTML(commit)}</span>
      </div>
    </td>
    <td>${badge}${progress}</td>
    <td>
      <div class="duration-cell">
        <span><b>${duration}</b><small>total</small></span>
        <span><b>${init}</b><small>checkout</small></span>
        <span><b>${build}</b><small>build</small></span>
      </div>
    </td>
    <td><div class="cache-cell">${cache}</div></td>
    <td class="actions-cell">
      <button class="ghost small" data-logs="${escapeHTML(b.id)}">Logs</button>
      ${artifact}
    </td>
  </tr>`;
}

function renderCache(m) {
  if (m === undefined || m === null) {
    return '<span class="muted small">…</span>';
  }
  if (!m || (!m.hitRate && !m.hits && !m.misses && !m.sizeBytes)) {
    return '<span class="muted small">—</span>';
  }
  const chips = [];
  if (m.hitRate) chips.push(`<span class="chip ok">hit ${Math.round(m.hitRate)}%</span>`);
  if (m.hits || m.misses) chips.push(`<span class="chip">${m.hits || 0} / ${m.misses || 0}</span>`);
  if (m.sizeBytes) chips.push(`<span class="chip">${formatBytes(m.sizeBytes)}</span>`);
  for (const [k, v] of Object.entries(m.extra || {})) {
    chips.push(`<span class="chip">${escapeHTML(k)} ${escapeHTML(v)}</span>`);
  }
  return chips.join("");
}

function renderDurationChart() {
  const finished = state.builds.filter((b) => b.durationMs > 0).slice(0, 12).reverse();
  if (!finished.length) {
    els.durationChart.innerHTML = '<div class="empty"><strong>No data</strong><span>Durations will appear after the first completed build.</span></div>';
    return;
  }
  const max = Math.max(...finished.map((b) => b.durationMs), 1);
  const bars = finished.map((b) => {
    const height = Math.max(4, Math.round((b.durationMs / max) * 154));
    const label = timeShort(b.createdAt) || b.id.slice(-4);
    return `<div class="bar-wrap" title="${escapeHTML(b.id)} · ${formatDuration(b.durationMs)} · ${escapeHTML(b.commit || "HEAD")}">
      <div class="bar-area"><div class="bar" style="height:${height}px"></div></div>
      <span class="bar-label">${escapeHTML(label)}</span>
    </div>`;
  }).join("");
  els.durationChart.innerHTML = `<div class="chart-bars">${bars}</div>`;
}

function renderCacheChart() {
  const items = state.builds
    .filter((b) => b.status === "succeeded" && state.metrics.get(b.id) && state.metrics.get(b.id).hitRate > 0)
    .slice(0, 12)
    .reverse();
  if (!items.length) {
    els.cacheChart.innerHTML = '<div class="empty"><strong>No data</strong><span>Cache rates will appear after a successful build.</span></div>';
    return;
  }

  const w = 620;
  const h = 186;
  const left = 38;
  const right = 14;
  const top = 14;
  const bottom = 28;
  const plotW = w - left - right;
  const plotH = h - top - bottom;
  const n = items.length;
  const x = (i) => n === 1 ? left + plotW / 2 : left + i * (plotW / (n - 1));
  const y = (v) => top + (1 - v / 100) * plotH;
  const points = items.map((b, i) => [x(i), y(state.metrics.get(b.id).hitRate)]);
  const linePath = points.map(([px, py], i) => `${i ? "L" : "M"} ${px.toFixed(1)} ${py.toFixed(1)}`).join(" ");
  const areaPath = `M ${points[0][0].toFixed(1)} ${(top + plotH).toFixed(1)} L ${points[0][0].toFixed(1)} ${points[0][1].toFixed(1)} ` +
    points.slice(1).map(([px, py]) => `L ${px.toFixed(1)} ${py.toFixed(1)}`).join(" ") +
    ` L ${points[points.length - 1][0].toFixed(1)} ${(top + plotH).toFixed(1)} Z`;

  const grid = [0, 25, 50, 75, 100].map((v) => {
    const gy = y(v);
    return `<line x1="${left}" y1="${gy}" x2="${w - right}" y2="${gy}" stroke="var(--chart-grid)" stroke-width="1"></line>
      <text x="${left - 8}" y="${gy + 3}" text-anchor="end" fill="var(--muted)" font-size="10">${v}%</text>`;
  }).join("");

  const circles = points.map(([px, py], i) => {
    const b = items[i];
    const rate = Math.round(state.metrics.get(b.id).hitRate);
    return `<circle cx="${px.toFixed(1)}" cy="${py.toFixed(1)}" r="3.5" fill="var(--surface)" stroke="var(--accent)" stroke-width="2">
      <title>${escapeHTML(b.id)} · ${rate}% cache</title>
    </circle>`;
  }).join("");

  const last = points[points.length - 1];
  const lastRate = Math.round(state.metrics.get(items[items.length - 1].id).hitRate);
  const firstLabel = timeShort(items[0].createdAt);
  const lastLabel = timeShort(items[items.length - 1].createdAt);

  els.cacheChart.innerHTML = `<svg viewBox="0 0 ${w} ${h}" role="img" aria-label="Cache rate of recent builds">
    <defs>
      <linearGradient id="cacheFill" x1="0" y1="0" x2="0" y2="1">
        <stop offset="0%" style="stop-color:var(--accent);stop-opacity:.30"></stop>
        <stop offset="100%" style="stop-color:var(--accent);stop-opacity:0"></stop>
      </linearGradient>
    </defs>
    ${grid}
    <path d="${areaPath}" fill="url(#cacheFill)"></path>
    <path d="${linePath}" fill="none" stroke="var(--accent)" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"></path>
    ${circles}
    <text x="${last[0].toFixed(1)}" y="${Math.max(12, last[1] - 9).toFixed(1)}" text-anchor="middle" fill="var(--accent)" font-size="11" font-weight="700">${lastRate}%</text>
    ${firstLabel ? `<text x="${left}" y="${h - 8}" fill="var(--muted)" font-size="10">${escapeHTML(firstLabel)}</text>` : ""}
    ${lastLabel ? `<text x="${w - right}" y="${h - 8}" text-anchor="end" fill="var(--muted)" font-size="10">${escapeHTML(lastLabel)}</text>` : ""}
  </svg>`;
}

function openLogs(id) {
  if (state.logAbort) state.logAbort.abort();
  const controller = new AbortController();
  state.logAbort = controller;
  els.logsTitle.textContent = "Logs — " + id;
  els.logs.textContent = "";
  if (!els.logsDialog.open) els.logsDialog.showModal();
  fetch(`/api/builds/${encodeURIComponent(id)}/logs`, {credentials: "same-origin", signal: controller.signal})
    .then(async (res) => {
      if (!res.ok) throw new Error(await res.text());
      const reader = res.body.getReader();
      const decoder = new TextDecoder();
      for (;;) {
        const {value, done} = await reader.read();
        if (done) break;
        els.logs.textContent += decoder.decode(value, {stream: true});
        els.logs.scrollTop = els.logs.scrollHeight;
      }
    })
    .catch((err) => {
      if (err.name !== "AbortError") els.logs.textContent += "\n[error: " + err.message + "]";
    });
}

function applyTheme(theme) {
  document.documentElement.dataset.theme = theme;
  localStorage.setItem("zeta-theme", theme);
}

els.refresh.addEventListener("click", loadBuilds);
els.volumesRefresh.addEventListener("click", loadVolumes);
els.volumesBody.addEventListener("click", async (e) => {
  const resize = e.target.closest("[data-resize]");
  if (resize) { openResize(resize.dataset.resize); return; }
  const del = e.target.closest("[data-delete]");
  if (!del) return;
  try {
    await deleteVolume(del.dataset.delete);
    await loadVolumes();
  } catch (err) {
    els.volumeError.textContent = err.message;
    els.volumeError.hidden = false;
  }
});
els.resizeForm.addEventListener("submit", async (e) => {
  e.preventDefault();
  if (!state.resizeVolume) return;
  els.resizeSubmit.disabled = true;
  els.resizeError.hidden = true;
  try {
    await resizeVolume(state.resizeVolume.name, els.resizeSize.value.trim());
    els.resizeDialog.close();
    state.resizeVolume = null;
    await loadVolumes();
  } catch (err) {
    els.resizeError.textContent = err.message;
    els.resizeError.hidden = false;
    els.resizeSubmit.disabled = false;
  }
});
els.resizeClose.addEventListener("click", () => els.resizeDialog.close());
els.resizeCancel.addEventListener("click", () => els.resizeDialog.close());
els.resizeDialog.addEventListener("close", () => { state.resizeVolume = null; });
els.themeToggle.addEventListener("click", () => {
  applyTheme(document.documentElement.dataset.theme === "dark" ? "light" : "dark");
});
els.logout.addEventListener("click", async () => {
  await fetch("/api/logout", {method: "POST"});
  location.href = "/login";
});
els.logsClose.addEventListener("click", () => els.logsDialog.close());
els.logsDialog.addEventListener("close", () => {
  if (state.logAbort) state.logAbort.abort();
  state.logAbort = null;
});

function statusLabel(s) {
  return ({pending: "pending", running: "running", succeeded: "succeeded", failed: "failed"})[s] || s;
}

function formatDuration(ms) {
  if (!ms) return "—";
  if (ms < 1000) return Math.round(ms) + " ms";
  if (ms < 60000) return (ms / 1000).toFixed(1) + " s";
  return Math.floor(ms / 60000) + "m " + Math.round((ms % 60000) / 1000) + "s";
}

function formatBytes(bytes) {
  if (!bytes) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  let n = Number(bytes);
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
  return n.toFixed(n >= 10 ? 0 : 1) + " " + units[i];
}

function timeAgo(iso) {
  if (!iso) return "—";
  const d = new Date(iso).getTime();
  if (!d) return "—";
  const s = Math.max(0, Math.floor((Date.now() - d) / 1000));
  if (s < 60) return "just now";
  if (s < 3600) return Math.floor(s / 60) + " min";
  if (s < 86400) return Math.floor(s / 3600) + " h";
  return Math.floor(s / 86400) + "d";
}

function timeShort(iso) {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return d.toLocaleTimeString("en-US", {hour: "2-digit", minute: "2-digit", hour12: false});
}

function escapeHTML(s) {
  return String(s).replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}

(async () => {
  if (!(await loadMe())) {
    els.loginView.hidden = false;
    return;
  }
  els.appView.hidden = false;
  try {
    await loadBuilds();
  } catch (err) {
    els.buildsBody.innerHTML = `<tr><td colspan="6"><p class="error">${escapeHTML(err.message)}</p></td></tr>`;
  }
  await loadVolumes();
  state.poll = setInterval(() => {
    if (!document.hidden) {
      loadBuilds();
      loadVolumes();
    }
  }, 4000);
})();
