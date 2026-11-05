// @ts-check
// Состояние окна. Данные приходят с сервера; здесь только то, что нужно для показа.

/** @typedef {import("./cards.js").View} View */

/** remembered — значение из localStorage; в превью и приватном режиме его может не быть. */
function remembered(key, fallback) {
  try { return localStorage.getItem(key) || fallback; } catch { return fallback; }
}

export const state = {
  /** @type {View | null} */
  view: null,
  tab: remembered("tab", "needs"),
  /** раскрытые сворачиваемые группы: "вкладка:группа" */
  open: new Set(),
  /** настройки, как их отдал GET /api/config */
  config: /** @type {import("./form.js").ConfigForm | null} */ (null),
};

/** @param {string} tab */
export function setTab(tab) {
  state.tab = tab;
  try { localStorage.setItem("tab", tab); } catch { /* не запомним — не страшно */ }
}

/** card — карточка вида по ключу. */
export function card(key) {
  return state.view?.cards.find((c) => c.key === key);
}
