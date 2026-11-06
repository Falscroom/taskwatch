// @ts-check
// Карточки и группы: чистые функции «вид с сервера → HTML», без DOM.
// Кликабельные элементы помечены data-action — их разбирает actions.js.
import { esc, loaded, muteLabel } from "./format.js";

/**
 * @typedef {{username: string, name: string, initials: string, decision?: string}} Person
 * @typedef {{text: string, tone: string}} Badge
 * @typedef {{branch: string, pipeline?: string, url?: string}} Release — релизная ветка и сборка релиза с этим MR
 * @typedef {{
 *   key: string, group: string, mine: boolean, review: boolean, title: string, url: string,
 *   issueId?: string, issueUrl?: string, issueState?: string,
 *   author?: Person, reviewers: Person[], pipeline?: string, pipelineUrl?: string,
 *   behind: number, comments: number, release: Release[], badges: Badge[],
 *   action?: {label: string, issueId: string}, wait?: string, muted?: boolean, mutedUntil?: string
 * }} Card
 * @typedef {{version: number, updatedAt: string, error?: string, needsYou: number, cards: Card[], sleepUntil?: string}} View
 */

/** @type {Record<string, string>} */
export const GROUPS = {
  work: "Твой MR требует работы",
  ready: "Пора переводить",
  review: "Ждёт твоего ревью",
  waiting: "Ждём других",
  nomr: "В спринте, без MR",
  merged: "Смёржено",
  muted: "Заглушено",
};

/**
 * Какие группы и карточки показывает вкладка. collapsed — свёрнутые по умолчанию группы.
 * @type {Record<string, {groups: string[], collapsed: string[], filter: (c: Card) => boolean}>}
 */
export const TABS = {
  needs: { groups: ["work", "ready", "review", "waiting"], collapsed: ["waiting"], filter: () => true },
  mine: { groups: ["work", "ready", "waiting", "nomr", "merged"], collapsed: ["merged"], filter: (c) => c.mine },
  review: { groups: ["review", "waiting"], collapsed: [], filter: (c) => c.review },
  all: { groups: ["work", "ready", "review", "waiting", "nomr", "merged", "muted"], collapsed: ["muted"], filter: () => true },
};

/** @type {Record<string, string>} */
const ICONS = {
  green: '<svg viewBox="0 0 24 24"><path d="M5 12.5l4.5 4.5L19 7.5"/></svg>',
  yellow: '<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="7"/><path d="M12 8.5V12l2.5 2"/></svg>',
  red: '<svg viewBox="0 0 24 24"><path d="M7 7l10 10M17 7L7 17"/></svg>',
  comments: '<svg viewBox="0 0 24 24"><path d="M12 4c4.97 0 9 3.36 9 7.5S16.97 19 12 19c-1.1 0-2.15-.16-3.12-.46L4 20l1.35-3.6C3.9 15.03 3 13.33 3 11.5 3 7.36 7.03 4 12 4z"/></svg>',
  belloff: '<svg viewBox="0 0 24 24"><path d="M18 8a6 6 0 0 0-9.3-5M6.3 6.3C6.1 6.9 6 7.4 6 8c0 7-3 9-3 9h14M10.3 21a1.9 1.9 0 0 0 3.4 0M4 4l16 16"/></svg>',
  bell: '<svg viewBox="0 0 24 24"><path d="M18 8a6 6 0 1 0-12 0c0 7-3 9-3 9h18s-3-2-3-9M10.3 21a1.9 1.9 0 0 0 3.4 0"/></svg>',
};

/**
 * releaseChip — релизная ветка; когда сборка релиза с этим MR уже есть —
 * со значком её статуса, клик открывает пайплайн.
 * @param {Release} r
 */
function releaseChip(r) {
  const tone = r.pipeline ? ` ${esc(r.pipeline)}` : "";
  const open = r.url ? ` data-action="open" data-url="${esc(r.url)}" title="Сборка релиза"` : "";
  return `<span class="chip release${tone}"${open}>${esc(r.branch)}${r.pipeline ? ICONS[r.pipeline] ?? "" : ""}</span>`;
}

/** @param {Person} p */
function avatar(p) {
  const title = p.name ? `${p.name} (@${p.username})` : `@${p.username}`;
  return `<span class="av ${esc(p.decision || "")}" title="${esc(title)}">${esc(p.initials)}</span>`;
}

/** @param {Card} c */
function tone(c) {
  return c.group === "merged" ? "purple" : c.pipeline || "";
}

/**
 * @param {Card} c
 * @param {number} [now]
 */
export function renderCard(c, now = Date.now()) {
  const meta = [];
  if (c.author) {
    const reviewers = c.reviewers.length ? `<span class="sep">›</span><span class="stack">${c.reviewers.map(avatar).join("")}</span>` : "";
    meta.push(`<span class="people">${avatar(c.author)}${reviewers}</span>`);
  }
  if (c.issueId) {
    meta.push(`<span class="chip issue" data-action="open" data-url="${esc(c.issueUrl)}" title="Открыть в YouTrack">${esc(c.issueId)}${c.issueState ? " · " + esc(c.issueState) : ""}</span>`);
  }
  if (c.behind > 0 && c.group !== "merged") meta.push(`<span class="chip behind" title="Отстаёт от target-ветки">↓${c.behind}</span>`);
  for (const r of c.release) meta.push(releaseChip(r));
  if (c.wait) meta.push(`<span class="chip wait" title="MR открыт уже">⏳ ${esc(c.wait)}</span>`);

  const side = [];
  if (c.action) {
    side.push(`<button class="action" data-action="transition" data-issue="${esc(c.action.issueId)}">→ ${esc(c.action.label)}</button>`);
  }
  if (c.pipeline) {
    side.push(`<span class="status ${esc(c.pipeline)}" data-action="open" data-url="${esc(c.pipelineUrl)}" title="Пайплайн">${ICONS[c.pipeline] ?? ""}</span>`);
  }
  if (c.group === "muted") {
    meta.push(`<span class="chip muted" title="Заглушено">${muteLabel(c.mutedUntil, now)}</span>`);
    side.push(`<button class="iconbtn" data-action="unmute" data-key="${esc(c.key)}" title="Вернуть карточку">${ICONS.bell}</button>`);
  } else if (c.key.startsWith("mr:")) {
    side.push(`<button class="iconbtn" data-action="mute" data-key="${esc(c.key)}" title="Заглушить">${ICONS.belloff}</button>`);
  }

  const foot = c.author ? `<span class="comments">${c.comments} ${ICONS.comments}</span>` : "";
  const badges = c.badges.map((b) => `<span class="badge ${esc(b.tone)}">${esc(b.text)}</span>`);

  return `
    <article class="card tone-${tone(c)}${c.group === "muted" ? " muted" : ""}" data-action="open" data-url="${esc(c.url)}">
      <div class="card-title">${esc(c.title)}</div>
      <div class="card-side">${side.join("")}</div>
      <div class="card-meta">${meta.join("")}</div>
      <div class="card-foot">${foot}</div>
      ${badges.length ? `<div class="card-badges">${badges.join("")}</div>` : ""}
    </article>`;
}

/**
 * renderList — HTML списка карточек для вкладки.
 * @param {View} v
 * @param {string} tabName
 * @param {Set<string>} open раскрытые сворачиваемые группы, ключи "вкладка:группа"
 * @param {number} [now]
 */
export function renderList(v, tabName, open, now = Date.now()) {
  const tab = TABS[tabName] ?? TABS.needs;
  const cards = v.cards.filter(tab.filter);
  let html = "";
  for (const g of tab.groups) {
    const items = cards.filter((c) => c.group === g);
    if (!items.length) continue;
    const key = `${tabName}:${g}`;
    const collapsible = tab.collapsed.includes(g);
    const isOpen = !collapsible || open.has(key);
    html += collapsible
      ? `<div class="group-title toggle ${isOpen ? "open" : ""}" data-action="toggle" data-group="${key}"><span class="chev">›</span>${GROUPS[g]} <span class="n">${items.length}</span></div>`
      : `<div class="group-title">${GROUPS[g]}</div>`;
    if (isOpen) html += items.map((c) => renderCard(c, now)).join("");
  }
  return html || `<div class="empty">${loaded(v.updatedAt) ? "Ничего не требует внимания" : "Загружаю…"}</div>`;
}
