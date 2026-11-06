// @ts-check
// Форма настроек как таблица полей: одно описание и для заполнения, и для чтения.
// Чистые функции без DOM — доступ к полям передаётся снаружи (settings.js, тесты).
import { esc, fmtDur } from "./format.js";

/**
 * @typedef {{url: string, tokenSet: boolean, ok: boolean}} Service
 * @typedef {{
 *   configPath: string, configured: boolean, pollInterval: string,
 *   gitlab: Service & {username: string}, youtrack: Service & {login: string, query: string},
 *   rules: {requiredApprovals: number, reviewState: string, nextCommand: string, autoTransition: boolean,
 *     releaseBranchPrefix: string, mrLookback: string, excludeDraft: boolean, mrBehindCommits: number}
 * }} ConfigForm
 * @typedef {"text" | "number" | "check" | "duration" | "token"} Kind
 * @typedef {{id: string, path: string, kind?: Kind, fallback?: number, hint?: string}} Field
 * @typedef {{value?: string, checked?: boolean, placeholder?: string}} Input
 */

/** @type {Field[]} */
export const FIELDS = [
  { id: "gl-url", path: "gitlab.url" },
  { id: "gl-username", path: "gitlab.username" },
  { id: "gl-token", path: "gitlab.token", kind: "token", hint: "personal token, read_api" },
  { id: "yt-url", path: "youtrack.url" },
  { id: "yt-login", path: "youtrack.login" },
  { id: "yt-query", path: "youtrack.query" },
  { id: "yt-token", path: "youtrack.token", kind: "token", hint: "permanent token, perm:…" },
  { id: "r-approvals", path: "rules.requiredApprovals", kind: "number", fallback: 2 },
  { id: "r-review-state", path: "rules.reviewState" },
  { id: "r-next-command", path: "rules.nextCommand" },
  { id: "r-auto", path: "rules.autoTransition", kind: "check" },
  { id: "r-release", path: "rules.releaseBranchPrefix" },
  { id: "r-lookback", path: "rules.mrLookback", kind: "duration" },
  { id: "r-behind", path: "rules.mrBehindCommits", kind: "number", fallback: 0 },
  { id: "r-poll", path: "pollInterval", kind: "duration" },
  { id: "r-nodraft", path: "rules.excludeDraft", kind: "check" },
];

const TOKEN_SAVED = "сохранён в связке ключей · пусто = не менять";

/** @param {any} obj @param {string} path */
function get(obj, path) {
  return path.split(".").reduce((o, k) => o?.[k], obj);
}

/** @param {any} obj @param {string} path @param {unknown} value */
function set(obj, path, value) {
  const keys = path.split(".");
  const last = /** @type {string} */ (keys.pop());
  const parent = keys.reduce((o, k) => (o[k] ??= {}), obj);
  parent[last] = value;
}

/**
 * inputsFor — что показать в каждом поле для настроек cfg. Токены сервер не
 * отдаёт: поле пустое, а подсказка говорит, сохранён ли токен.
 * @param {ConfigForm} cfg
 * @returns {Record<string, Input>}
 */
export function inputsFor(cfg) {
  /** @type {Record<string, Input>} */
  const out = {};
  for (const f of FIELDS) {
    const v = get(cfg, f.path);
    switch (f.kind) {
      case "check":
        out[f.id] = { checked: Boolean(v) };
        break;
      case "number":
        out[f.id] = { value: String(v ?? f.fallback ?? 0) };
        break;
      case "duration":
        out[f.id] = { value: fmtDur(v) };
        break;
      case "token": {
        const saved = get(cfg, f.path.replace(/token$/, "tokenSet"));
        out[f.id] = { value: "", placeholder: saved ? TOKEN_SAVED : f.hint };
        break;
      }
      default:
        out[f.id] = { value: v || "" };
    }
  }
  return out;
}

/**
 * payload — тело POST /api/config из значений полей.
 * @param {(id: string) => Input} read
 */
export function payload(read) {
  const out = {};
  for (const f of FIELDS) {
    const input = read(f.id);
    switch (f.kind) {
      case "check":
        set(out, f.path, Boolean(input.checked));
        break;
      case "number":
        set(out, f.path, Number(input.value) || 0);
        break;
      default:
        set(out, f.path, (input.value || "").trim());
    }
  }
  return out;
}

/**
 * boardsHTML — доски с текущим спринтом для кнопки «Подобрать»: клик по доске
 * подставляет её запрос тасок.
 * @param {{name: string, sprint: string, query: string}[]} boards
 */
export function boardsHTML(boards) {
  if (!boards.length) return `<div class="skip">Нет досок с текущим спринтом — впиши запрос сам</div>`;
  return boards
    .map((b) => `<button type="button" class="board" data-action="board-pick" data-query="${esc(b.query)}">${esc(b.name)}<span class="sprint">${esc(b.sprint)}</span></button>`)
    .join("");
}

/**
 * probeHTML — итог кнопки «Проверить».
 * @param {Record<"gitlab" | "youtrack", {ok?: boolean, user?: string, error?: string, skipped?: boolean}>} results
 */
export function probeHTML(results) {
  /** @param {string} name @param {{ok?: boolean, user?: string, error?: string, skipped?: boolean}} p */
  const line = (name, p) => {
    if (p.skipped) return `<div class="skip">— ${name} не заполнен</div>`;
    return `<div class="${p.ok ? "ok" : "fail"}">${p.ok ? "✓" : "✗"} ${name}${p.user ? ` — @${esc(p.user)}` : ""}${p.error ? `: ${esc(p.error)}` : ""}</div>`;
  };
  return line("GitLab", results.gitlab) + line("YouTrack", results.youtrack);
}
