"use strict";

const GROUPS = {
  work: "Твой MR требует работы",
  ready: "Пора переводить",
  review: "Ждёт твоего ревью",
  waiting: "Ждём других",
  nomr: "В спринте, без MR",
  merged: "Смёржено",
  muted: "Заглушено",
};

// Какие группы и карточки показывает вкладка. collapsed — свёрнутые по умолчанию группы.
const TABS = {
  needs: { groups: ["work", "ready", "review", "waiting"], collapsed: ["waiting"], filter: () => true },
  mine: { groups: ["work", "ready", "waiting", "nomr", "merged"], collapsed: ["merged"], filter: (c) => c.mine },
  review: { groups: ["review", "waiting"], collapsed: [], filter: (c) => c.review },
  all: { groups: ["work", "ready", "review", "waiting", "nomr", "merged", "muted"], collapsed: ["muted"], filter: () => true },
};

const ICONS = {
  green: '<svg viewBox="0 0 24 24"><path d="M5 12.5l4.5 4.5L19 7.5"/></svg>',
  yellow: '<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="7"/><path d="M12 8.5V12l2.5 2"/></svg>',
  red: '<svg viewBox="0 0 24 24"><path d="M7 7l10 10M17 7L7 17"/></svg>',
  comments: '<svg viewBox="0 0 24 24"><path d="M12 4c4.97 0 9 3.36 9 7.5S16.97 19 12 19c-1.1 0-2.15-.16-3.12-.46L4 20l1.35-3.6C3.9 15.03 3 13.33 3 11.5 3 7.36 7.03 4 12 4z"/></svg>',
  belloff: '<svg viewBox="0 0 24 24"><path d="M18 8a6 6 0 0 0-9.3-5M6.3 6.3C6.1 6.9 6 7.4 6 8c0 7-3 9-3 9h14M10.3 21a1.9 1.9 0 0 0 3.4 0M4 4l16 16"/></svg>',
  bell: '<svg viewBox="0 0 24 24"><path d="M18 8a6 6 0 1 0-12 0c0 7-3 9-3 9h18s-3-2-3-9M10.3 21a1.9 1.9 0 0 0 3.4 0"/></svg>',
};

const state = {
  view: null,
  tab: load("tab", "needs"),
  open: new Set(),
  settings: { cfg: null },
};

const $ = (id) => document.getElementById(id);

function load(key, fallback) {
  try { return localStorage.getItem(key) || fallback; } catch { return fallback; }
}
function save(key, value) {
  try { localStorage.setItem(key, value); } catch { /* ignore */ }
}

function esc(s) {
  return String(s ?? "").replace(/[&<>"']/g, (ch) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[ch]));
}

function ago(iso) {
  const t = Date.parse(iso);
  if (!t || t < 0 || new Date(iso).getFullYear() < 2000) return "загрузка…";
  const s = Math.max(0, Math.round((Date.now() - t) / 1000));
  if (s < 60) return "только что";
  if (s < 3600) return `${Math.floor(s / 60)} мин назад`;
  return `${Math.floor(s / 3600)} ч назад`;
}

async function api(path, body) {
  const res = await fetch(path, body === undefined ? {} : {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    let msg = res.statusText;
    try { msg = (await res.json()).error || msg; } catch { /* ignore */ }
    throw new Error(msg);
  }
  return res.status === 204 ? null : res.json();
}

function toast(msg) {
  const el = $("toast");
  el.textContent = msg;
  el.hidden = false;
  clearTimeout(toast.timer);
  toast.timer = setTimeout(() => { el.hidden = true; }, 4000);
}

function open(url) {
  if (url) api("/api/open", { url }).catch((e) => toast(e.message));
}

function avatar(p) {
  const title = p.name ? `${p.name} (@${p.username})` : `@${p.username}`;
  return `<span class="av ${esc(p.decision || "")}" title="${esc(title)}">${esc(p.initials)}</span>`;
}

function tone(card) {
  if (card.group === "merged") return "purple";
  return card.pipeline || "";
}

// muteLabel — «до 20:35», если заглушение снимется за сутки, иначе с датой; «навсегда».
function muteLabel(iso) {
  if (!iso) return "навсегда";
  const t = new Date(iso);
  if (!t.getTime() || t.getFullYear() < 2000) return "навсегда";
  const hm = `${String(t.getHours()).padStart(2, "0")}:${String(t.getMinutes()).padStart(2, "0")}`;
  const months = ["янв", "фев", "мар", "апр", "мая", "июн", "июл", "авг", "сен", "окт", "ноя", "дек"];
  if (t - Date.now() < 86400000) return `до ${hm}`;
  return `до ${t.getDate()} ${months[t.getMonth()]} ${hm}`;
}

function renderCard(c) {
  const meta = [];
  if (c.author) {
    meta.push(`<span class="people">${avatar(c.author)}${c.reviewers.length ? `<span class="sep">›</span><span class="stack">${c.reviewers.map(avatar).join("")}</span>` : ""}</span>`);
  }
  if (c.issueId) {
    meta.push(`<span class="chip issue" data-open="${esc(c.issueUrl)}" title="Открыть в YouTrack">${esc(c.issueId)}${c.issueState ? " · " + esc(c.issueState) : ""}</span>`);
  }
  if (c.behind > 0 && c.group !== "merged") meta.push(`<span class="chip behind" title="Отстаёт от target-ветки">↓${c.behind}</span>`);
  for (const r of c.release) meta.push(`<span class="chip release">${esc(r)}</span>`);
  if (c.wait) meta.push(`<span class="chip wait" title="MR открыт уже">⏳ ${esc(c.wait)}</span>`);

  const badges = c.badges.map((b) => `<span class="badge ${esc(b.tone)}">${esc(b.text)}</span>`);
  const side = [];
  if (c.action) {
    side.push(`<button class="action" data-transition="${esc(c.action.issueId)}">→ ${esc(c.action.label)}</button>`);
  }
  if (c.pipeline) {
    side.push(`<span class="status ${esc(c.pipeline)}" data-open="${esc(c.pipelineUrl)}" title="Пайплайн">${ICONS[c.pipeline]}</span>`);
  }
  if (c.group === "muted") {
    meta.push(`<span class="chip muted" title="Заглушено">${muteLabel(c.mutedUntil)}</span>`);
    side.push(`<button class="iconbtn" data-unmute="${esc(c.key)}" title="Вернуть карточку">${ICONS.bell}</button>`);
  } else if (c.key.startsWith("mr:")) {
    side.push(`<button class="iconbtn" data-mute="${esc(c.key)}" title="Заглушить">${ICONS.belloff}</button>`);
  }

  const foot = c.author ? `<span class="comments">${c.comments} ${ICONS.comments}</span>` : "";

  return `
    <article class="card tone-${tone(c)}${c.group === "muted" ? " muted" : ""}" data-open="${esc(c.url)}">
      <div class="card-title">${esc(c.title)}</div>
      <div class="card-side">${side.join("")}</div>
      <div class="card-meta">${meta.join("")}</div>
      <div class="card-foot">${foot}</div>
      ${badges.length ? `<div class="card-badges">${badges.join("")}</div>` : ""}
    </article>`;
}

function render() {
  const v = state.view;
  document.querySelectorAll("#tabs button").forEach((b) => b.classList.toggle("active", b.dataset.tab === state.tab));
  if (!v) return;

  $("updated").textContent = ago(v.updatedAt);
  $("needs-count").textContent = v.needsYou;
  $("error").hidden = !v.error;
  $("error").textContent = v.error || "";

  const tab = TABS[state.tab];
  const cards = v.cards.filter(tab.filter);
  let html = "";

  for (const g of tab.groups) {
    const items = cards.filter((c) => c.group === g);
    if (!items.length) continue;
    const key = `${state.tab}:${g}`;
    const collapsible = tab.collapsed.includes(g);
    const isOpen = !collapsible || state.open.has(key);
    html += collapsible
      ? `<div class="group-title toggle ${isOpen ? "open" : ""}" data-toggle="${key}"><span class="chev">›</span>${GROUPS[g]} <span class="n">${items.length}</span></div>`
      : `<div class="group-title">${GROUPS[g]}</div>`;
    if (isOpen) html += items.map(renderCard).join("");
  }

  if (!html) {
    html = `<div class="empty">${v.updatedAt && new Date(v.updatedAt).getFullYear() > 2000 ? "Ничего не требует внимания" : "Загружаю…"}</div>`;
  }
  $("list").innerHTML = html;
}

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

// watchView держит вид свежим: long-poll /api/view?after=<version> отвечает,
// как только у движка новая версия (прошёл опрос, сменились заглушения), — так
// результат любого действия приходит сам, без опроса по таймеру.
async function watchView() {
  let failing = false;
  for (;;) {
    try {
      const after = state.view ? `?after=${state.view.version}` : "";
      state.view = await api("/api/view" + after);
      failing = false;
      $("refresh").classList.remove("spinning");
      render();
    } catch (e) {
      if (!failing) toast(e.message); // о пропаже связи — один раз, а не каждые 5 секунд
      failing = true;
      await sleep(5000);
    }
  }
}

$("tabs").addEventListener("click", (e) => {
  const b = e.target.closest("button[data-tab]");
  if (!b) return;
  state.tab = b.dataset.tab;
  save("tab", state.tab);
  render();
});

// Крутилка останавливается, когда watchView получит итог внеочередного опроса.
$("refresh").addEventListener("click", async () => {
  const btn = $("refresh");
  btn.classList.add("spinning");
  try {
    await api("/api/refresh", {});
  } catch (e) {
    btn.classList.remove("spinning");
    toast(e.message);
  }
});

$("list").addEventListener("click", async (e) => {
  const toggle = e.target.closest("[data-toggle]");
  if (toggle) {
    const key = toggle.dataset.toggle;
    state.open.has(key) ? state.open.delete(key) : state.open.add(key);
    render();
    return;
  }

  const action = e.target.closest("[data-transition]");
  if (action) {
    e.stopPropagation();
    action.disabled = true;
    try {
      await api("/api/transition", { issueId: action.dataset.transition });
      action.textContent = "Переведена ✓";
    } catch (err) {
      action.disabled = false;
      toast(`Не удалось перевести: ${err.message}`);
    }
    return;
  }

  const mute = e.target.closest("[data-mute]");
  if (mute) {
    e.stopPropagation();
    const card = mute.closest(".card");
    muteKey = mute.dataset.mute;
    muteTitle = card?.querySelector(".card-title")?.textContent || "";
    $("mute-title").textContent = muteTitle;
    $("mute-also").checked = false;
    $("mute").hidden = false;
    return;
  }

  const unmute = e.target.closest("[data-unmute]");
  if (unmute) {
    e.stopPropagation();
    unmute.disabled = true;
    try {
      await api("/api/unmute", {
        key: unmute.dataset.unmute,
        title: unmute.closest(".card")?.querySelector(".card-title")?.textContent || "",
      });
      toast("Вернул карточку");
    } catch (err) {
      unmute.disabled = false;
      toast(err.message);
    }
    return;
  }

  const target = e.target.closest("[data-open]");
  if (target) open(target.dataset.open);
});

// --- заглушение ---

let muteKey = null;
let muteTitle = "";

$("mute-close").addEventListener("click", () => { $("mute").hidden = true; });

$("mute-options").addEventListener("click", async (e) => {
  const b = e.target.closest("[data-hours]");
  if (!b || !muteKey) return;
  b.disabled = true;
  try {
    await api("/api/mute", {
      key: muteKey,
      hours: Number(b.dataset.hours),
      alsoTitle: $("mute-also").checked,
      title: muteTitle,
    });
    $("mute").hidden = true;
    toast(Number(b.dataset.hours) ? "Заглушено" : "Заглушено навсегда");
  } catch (err) {
    toast(err.message);
  } finally {
    b.disabled = false;
    muteKey = null;
    muteTitle = "";
  }
});

// Данные приходят через watchView; таймер только обновляет «N мин назад».
window.addEventListener("focus", render);
setInterval(render, 30000);

// --- настройки ---

function fmtDur(s) {
  const m = /^((\d+)h)?((\d+)m)?((\d+)s)?$/.exec(s || "");
  if (!m) return s || "";
  let out = "";
  if (m[2] && m[2] !== "0") out += m[2] + "h";
  if (m[4] && m[4] !== "0") out += m[4] + "m";
  if (m[6] && m[6] !== "0") out += m[6] + "s";
  return out || "0s";
}

function fillSettings(cfg) {
  state.settings.cfg = cfg;
  $("settings-path").textContent = cfg.configPath;
  $("gl-url").value = cfg.gitlab.url || "";
  $("gl-username").value = cfg.gitlab.username || "";
  $("gl-token").value = "";
  $("gl-token").placeholder = cfg.gitlab.tokenSet ? "сохранён в связке ключей · пусто = не менять" : "personal token, read_api";
  $("yt-url").value = cfg.youtrack.url || "";
  $("yt-login").value = cfg.youtrack.login || "";
  $("yt-query").value = cfg.youtrack.query || "";
  $("yt-token").value = "";
  $("yt-token").placeholder = cfg.youtrack.tokenSet ? "сохранён в связке ключей · пусто = не менять" : "permanent token, perm:…";
  $("r-approvals").value = cfg.rules.requiredApprovals ?? 2;
  $("r-review-state").value = cfg.rules.reviewState || "";
  $("r-next-command").value = cfg.rules.nextCommand || "";
  $("r-auto").checked = !!cfg.rules.autoTransition;
  $("r-release").value = cfg.rules.releaseBranchPrefix || "";
  $("r-lookback").value = fmtDur(cfg.rules.mrLookback);
  $("r-behind").value = cfg.rules.mrBehindCommits ?? 0;
  $("r-poll").value = fmtDur(cfg.pollInterval);
  $("r-nodraft").checked = !!cfg.rules.excludeDraft;
  $("settings-probe").hidden = true;
}

function readSettings() {
  return {
    pollInterval: $("r-poll").value.trim(),
    gitlab: {
      url: $("gl-url").value.trim(),
      username: $("gl-username").value.trim(),
      token: $("gl-token").value.trim(),
    },
    youtrack: {
      url: $("yt-url").value.trim(),
      login: $("yt-login").value.trim(),
      query: $("yt-query").value.trim(),
      token: $("yt-token").value.trim(),
    },
    rules: {
      requiredApprovals: Number($("r-approvals").value) || 0,
      reviewState: $("r-review-state").value.trim(),
      nextCommand: $("r-next-command").value.trim(),
      autoTransition: $("r-auto").checked,
      releaseBranchPrefix: $("r-release").value.trim(),
      mrLookback: $("r-lookback").value.trim(),
      mrBehindCommits: Number($("r-behind").value) || 0,
      excludeDraft: $("r-nodraft").checked,
    },
  };
}

function openSettings() {
  if (state.settings.cfg) fillSettings(state.settings.cfg);
  $("settings").hidden = false;
  $("gl-url").focus();
}

function closeSettings() {
  $("settings").hidden = true;
}

function showProbe(results) {
  const el = $("settings-probe");
  const line = (name, p) => {
    if (p.skipped) return `<div class="skip">— ${name} не заполнен</div>`;
    return `<div class="${p.ok ? "ok" : "fail"}">${p.ok ? "✓" : "✗"} ${name}${p.user ? ` — @${esc(p.user)}` : ""}${p.error ? `: ${esc(p.error)}` : ""}</div>`;
  };
  el.innerHTML = line("GitLab", results.gitlab) + line("YouTrack", results.youtrack);
  el.hidden = false;
}

$("settings-btn").addEventListener("click", openSettings);
$("settings-close").addEventListener("click", closeSettings);

$("settings-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const data = readSettings();
  const cur = state.settings.cfg || { gitlab: { tokenSet: false }, youtrack: { tokenSet: false } };
  const glOK = data.gitlab.url && data.gitlab.username && (data.gitlab.token || cur.gitlab.tokenSet);
  const ytOK = data.youtrack.url && data.youtrack.login && data.youtrack.query && (data.youtrack.token || cur.youtrack.tokenSet);
  if (!glOK && !ytOK) {
    toast("Заполни хотя бы один сервис целиком — второй можно позже");
    return;
  }
  const btn = $("settings-form").querySelector('[type="submit"]');
  btn.disabled = true;
  try {
    await api("/api/config", data);
    state.settings.cfg = await api("/api/config");
    closeSettings();
    toast("Настройки сохранены");
  } catch (err) {
    toast(err.message);
  } finally {
    btn.disabled = false;
  }
});

$("settings-test").addEventListener("click", async () => {
  const btn = $("settings-test");
  btn.disabled = true;
  btn.textContent = "Проверяю…";
  try {
    showProbe(await api("/api/config/test", readSettings()));
  } catch (err) {
    toast(err.message);
  } finally {
    btn.disabled = false;
    btn.textContent = "Проверить";
  }
});

async function boot() {
  try {
    state.settings.cfg = await api("/api/config");
    if (!state.settings.cfg.configured) openSettings();
  } catch (e) {
    toast(e.message);
  }
  watchView();
}

boot();
