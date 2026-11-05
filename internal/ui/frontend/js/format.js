// @ts-check
// Форматирование для окна: чистые функции без DOM (их проверяют тесты в node).

/** esc экранирует текст для вставки в HTML. */
export function esc(s) {
  return String(s ?? "").replace(/[&<>"']/g, (ch) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[ch] ?? ch);
}

/** loaded — время настоящее: до первого опроса сервер присылает нулевую дату. */
export function loaded(iso) {
  const t = Date.parse(iso ?? "");
  return t > 0 && new Date(t).getFullYear() > 2000;
}

/**
 * ago — «только что», «5 мин назад», «2 ч назад».
 * @param {string} iso
 * @param {number} [now]
 */
export function ago(iso, now = Date.now()) {
  if (!loaded(iso)) return "загрузка…";
  const s = Math.max(0, Math.round((now - Date.parse(iso)) / 1000));
  if (s < 60) return "только что";
  if (s < 3600) return `${Math.floor(s / 60)} мин назад`;
  return `${Math.floor(s / 3600)} ч назад`;
}

const MONTHS = ["янв", "фев", "мар", "апр", "мая", "июн", "июл", "авг", "сен", "окт", "ноя", "дек"];

/**
 * muteLabel — «до 20:35», если заглушение снимется за сутки, иначе с датой; «навсегда».
 * @param {string | undefined} iso
 * @param {number} [now]
 */
export function muteLabel(iso, now = Date.now()) {
  if (!iso || !loaded(iso)) return "навсегда";
  const t = new Date(iso);
  const hm = `${String(t.getHours()).padStart(2, "0")}:${String(t.getMinutes()).padStart(2, "0")}`;
  if (t.getTime() - now < 86400000) return `до ${hm}`;
  return `до ${t.getDate()} ${MONTHS[t.getMonth()]} ${hm}`;
}

/**
 * fmtDur — длительность Go ("336h0m0s") в короткую запись для формы ("336h").
 * @param {string} s
 */
export function fmtDur(s) {
  const m = /^((\d+)h)?((\d+)m)?((\d+)s)?$/.exec(s || "");
  if (!m) return s || "";
  let out = "";
  if (m[2] && m[2] !== "0") out += m[2] + "h";
  if (m[4] && m[4] !== "0") out += m[4] + "m";
  if (m[6] && m[6] !== "0") out += m[6] + "s";
  return out || "0s";
}
