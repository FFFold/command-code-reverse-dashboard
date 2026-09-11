'use strict';

const $ = (id) => document.getElementById(id);

function fmtUSD(n) {
  if (n == null || isNaN(n)) return '—';
  return '$' + Number(n).toFixed(4);
}
function fmtNum(n) {
  if (n == null || isNaN(n)) return '—';
  return Number(n).toLocaleString('en-US');
}
function fmtTime(ms) {
  if (!ms) return '—';
  return new Date(ms).toLocaleString();
}

function setBadge(el, label, ok) {
  el.textContent = label + (ok ? ' OK' : ' DOWN');
  el.classList.remove('ok', 'bad');
  el.classList.add(ok ? 'ok' : 'bad');
}

function renderSummary(s) {
  $('balance').textContent = fmtUSD(s.balance);
  const c = s.credits || {};
  $('balance-sub').textContent =
    `月度 ${fmtUSD(c.monthlyCredits)} · 已购 ${fmtUSD(c.purchasedCredits)} · 免费 ${fmtUSD(c.freeCredits)}`;
  $('m-monthly').textContent = fmtUSD(c.monthlyCredits);
  $('m-purchased').textContent = fmtUSD(c.purchasedCredits);
  $('m-free').textContent = fmtUSD(c.freeCredits);

  const sub = s.subscription;
  $('m-plan').textContent = sub ? `${sub.planId} (${sub.status})` : '—';

  const w = s.windows || {};
  renderWindow('5', w.fiveHour);
  renderWindow('w', w.weekly);

  renderAlerts(s.alerts || []);

  const h = s.health || {};
  setBadge($('h-live'), 'liveness', !!h.livenessOK);
  setBadge($('h-ready'), 'readiness', !!h.readinessOK);
  $('h-ver').textContent = 'proxy ' + ((s.version && s.version.version) || '—');
  $('h-cc').textContent = 'cli ' + ((s.version && s.version.ccVersion) || '—');

  renderErrors('summary-errors', s.errors);
  $('updated').textContent = '更新于 ' + (s.fetchedAt ? new Date(s.fetchedAt).toLocaleTimeString() : '—');
}

// Fixed window durations: 5 hours and 7 days (see design spec).
const WINDOW_DURATION_MS = {
  '5': 5 * 60 * 60 * 1000,
  w: 7 * 24 * 60 * 60 * 1000,
};

function renderWindow(key, win) {
  win = win || { used: 0, cap: 0 };
  const cap = Number(win.cap) || 0;
  const used = Number(win.used) || 0;
  const pct = cap > 0 ? (used / cap) * 100 : 0;
  const fill = $('win' + key + '-fill');
  fill.style.width = Math.min(100, pct).toFixed(1) + '%';
  fill.classList.remove('warn', 'bad');
  if (win.exceeded || pct >= 95) fill.classList.add('bad');
  else if (pct >= 80) fill.classList.add('warn');
  $('win' + key + '-pct').textContent = pct.toFixed(1) + '%';

  // Time cursor: elapsed fraction of the window, derived from resetAt.
  const cursor = $('win' + key + '-cursor');
  const resetAt = Number(win.resetAt) || 0;
  const duration = WINDOW_DURATION_MS[key];
  let timePct = null;
  if (resetAt > 0 && duration > 0) {
    const elapsed = duration - (resetAt - Date.now());
    timePct = Math.max(0, Math.min(100, (elapsed / duration) * 100));
    cursor.style.left = timePct.toFixed(1) + '%';
    cursor.hidden = false;
  } else {
    cursor.hidden = true;
  }

  const timeLabel = timePct == null ? '时间 —' : `时间 ${timePct.toFixed(0)}%`;
  $('win' + key + '-text').textContent =
    `${fmtUSD(used)} / ${fmtUSD(cap)} · ${timeLabel} · 重置 ${fmtTime(win.resetAt)}`;
}

function renderAlerts(list) {
  const ul = $('alerts');
  ul.innerHTML = '';
  if (!list.length) {
    const li = document.createElement('li');
    li.className = 'info';
    li.textContent = '正常，无告警';
    ul.appendChild(li);
    return;
  }
  for (const a of list) {
    const li = document.createElement('li');
    li.className = a.level || 'info';
    const t = document.createElement('div');
    t.className = 't';
    t.textContent = a.title;
    const d = document.createElement('div');
    d.className = 'd';
    d.textContent = a.detail;
    li.append(t, d);
    ul.appendChild(li);
  }
}

function renderErrors(id, errs) {
  const box = $(id);
  box.innerHTML = '';
  if (!errs || !errs.length) return;
  for (const e of errs) {
    const div = document.createElement('div');
    div.textContent = `[${e.source}] ${e.message}`;
    box.appendChild(div);
  }
}

function renderMetrics(m) {
  if (!m.enabled) {
    $('metrics-wrap').innerHTML =
      '<div class="muted">指标未启用（代理 METRICS_ENABLED=false）</div>';
    return;
  }
  $('mt-req').textContent = fmtNum(m.requestsTotal);
  const t = m.tokens || {};
  $('mt-in').textContent = fmtNum(t.input);
  $('mt-out').textContent = fmtNum(t.output);
  $('mt-cached').textContent = fmtNum(t.cached);
  $('mt-cost').textContent = fmtUSD(m.costUSD);

  const table = $('mt-requests');
  table.innerHTML = '';
  const head = document.createElement('tr');
  for (const h of ['模型', 'stream', 'result', '次数']) {
    const th = document.createElement('th');
    th.textContent = h;
    head.appendChild(th);
  }
  table.appendChild(head);
  for (const r of (m.requests || [])) {
    const tr = document.createElement('tr');
    for (const v of [r.model, r.stream, r.result, fmtNum(r.count)]) {
      const td = document.createElement('td');
      td.textContent = v;
      tr.appendChild(td);
    }
    table.appendChild(tr);
  }
  renderErrors('metrics-errors', m.errors);
}

async function getJSON(url) {
  const res = await fetch(url, { cache: 'no-store' });
  return res.json();
}

async function refresh() {
  const btn = $('refresh');
  btn.disabled = true;
  try {
    const [summary, metrics] = await Promise.all([
      getJSON('/api/summary'),
      getJSON('/api/metrics'),
    ]);
    renderSummary(summary);
    renderMetrics(metrics);
  } catch (err) {
    console.error(err);
    $('updated').textContent = '刷新失败';
  } finally {
    btn.disabled = false;
  }
}

$('refresh').addEventListener('click', refresh);
refresh();
