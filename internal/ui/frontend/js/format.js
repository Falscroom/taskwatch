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

/**
 * updatedLabel — что стоит у кнопки «Обновить». Пока данных нет, движок держит
 * нулевое время: без ошибки это ожидание первого опроса, с ошибкой — опрос уже
 * был и не удался, и «загрузка…» висела бы вечно.
 * @param {{updatedAt: string, error?: string}} v
 * @param {number} [now]
 */
export function updatedLabel(v, now = Date.now()) {
  if (!loaded(v.updatedAt)) return v.error ? "не удалось" : "загрузка…";
  return ago(v.updatedAt, now);
}

/**
 * emptyText — что писать вместо списка: ждём первый опрос, он не удался
 * (причина — строкой выше) или всё спокойно.
 * @param {{updatedAt: string, error?: string}} v
 */
export function emptyText(v) {
  if (loaded(v.updatedAt)) return "Ничего не требует внимания";
  return v.error ? "Данных пока нет" : "Загружаю…";
}

const WEEKDAYS = ["вс", "пн", "вт", "ср", "чт", "пт", "сб"];

/**
 * wakeLabel — когда опрос проснётся: «в 10:00» сегодня, «завтра в 10:00», «в пн 10:00».
 * @param {string} iso
 * @param {number} [now]
 */
export function wakeLabel(iso, now = Date.now()) {
  const t = new Date(iso);
  const hm = `${String(t.getHours()).padStart(2, "0")}:${String(t.getMinutes()).padStart(2, "0")}`;
  const day = (/** @type {number} */ ms) => new Date(ms).toDateString();
  if (day(t.getTime()) === day(now)) return `в ${hm}`;
  if (day(t.getTime()) === day(now + 864e5)) return `завтра в ${hm}`;
  return `в ${WEEKDAYS[t.getDay()]} ${hm}`;
}

/**
 * notice — что показать строкой над списком: ошибку опроса, сон вне рабочего
 * времени или ничего. Раньше это были две плашки сразу, красная и серая, и они
 * спорили друг с другом; теперь сон уходит второй строкой под ошибку.
 * @param {{error?: string, errorDetail?: string, sleepUntil?: string}} v
 * @param {number} [now]
 * @returns {{bad: boolean, text: string, sub: string, detail: string} | null}
 */
export function notice(v, now = Date.now()) {
  const asleep = v.sleepUntil ? `Вне рабочего времени · опрос ${wakeLabel(v.sleepUntil, now)} · «Обновить» — опросить сейчас` : "";
  if (v.error) return { bad: true, text: v.error, sub: asleep, detail: v.errorDetail || "" };
  if (asleep) return { bad: false, text: asleep, sub: "", detail: "" };
  return null;
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
