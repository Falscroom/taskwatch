// @ts-check
// Все клики окна — через один делегированный обработчик: элемент помечен
// data-action="имя", а здесь — что это имя делает. Срабатывает ближайший
// к месту клика элемент, поэтому кнопка на карточке не открывает саму карточку.
import { api, openURL } from "./api.js";
import { toast } from "./dom.js";
import { closeMute, muteFor, openMute } from "./mute.js";
import { clearSecrets, closeSettings, openSettings, pickBoards, testSettings, toggleLoginItem, useBoard } from "./settings.js";
import { card, setTab, state } from "./store.js";
import { render } from "./view.js";

/** @typedef {(el: HTMLElement) => void | Promise<void>} Action */

/** @type {Record<string, Action>} */
const actions = {
  open: (el) => openURL(el.dataset.url),

  tab: (el) => {
    setTab(el.dataset.tab || "needs");
    render();
  },

  toggle: (el) => {
    const key = el.dataset.group || "";
    if (!state.open.delete(key)) state.open.add(key);
    render();
  },

  // Крутилка останавливается, когда watchView получит итог внеочередного опроса.
  refresh: async (el) => {
    el.classList.add("spinning");
    try {
      await api("/api/refresh", {});
    } catch (err) {
      el.classList.remove("spinning");
      throw err;
    }
  },

  transition: async (el) => {
    const btn = /** @type {HTMLButtonElement} */ (el);
    btn.disabled = true;
    try {
      await api("/api/transition", { issueId: btn.dataset.issue });
      btn.textContent = "Переведена ✓";
    } catch (err) {
      btn.disabled = false;
      toast(`Не удалось перевести: ${err.message}`);
    }
  },

  mute: (el) => {
    const c = card(el.dataset.key);
    if (c) openMute(c);
  },
  "mute-for": (el) => muteFor(/** @type {HTMLButtonElement} */ (el)),
  "mute-close": closeMute,

  unmute: async (el) => {
    const btn = /** @type {HTMLButtonElement} */ (el);
    btn.disabled = true;
    try {
      await api("/api/unmute", { key: btn.dataset.key, title: card(btn.dataset.key)?.title || "" });
      toast("Вернул карточку");
    } catch (err) {
      btn.disabled = false;
      throw err;
    }
  },

  "settings-open": openSettings,
  "settings-close": closeSettings,
  "settings-test": (el) => testSettings(/** @type {HTMLButtonElement} */ (el)),
  "settings-boards": (el) => pickBoards(/** @type {HTMLButtonElement} */ (el)),
  "board-pick": useBoard,
  "login-item": (el) => toggleLoginItem(/** @type {HTMLInputElement} */ (el)),
  "secrets-clear": (el) => clearSecrets(/** @type {HTMLButtonElement} */ (el)),
};

/** @param {Event} e @returns {HTMLElement | null} ближайший к месту клика элемент с data-action */
const target = (e) => /** @type {HTMLElement | null} */ (/** @type {Element} */ (e.target).closest("[data-action]"));

/**
 * openInBackground открывает ссылку элемента фоном: фокус остаётся здесь, окно
 * не прячется, можно открыть несколько MR подряд. Браузер вперёд не выходит,
 * поэтому элемент коротко подсвечивается.
 */
function openInBackground(/** @type {HTMLElement} */ el) {
  openURL(el.dataset.url, true);
  el.classList.remove("opened");
  void el.offsetWidth; // перезапуск анимации при повторном клике
  el.classList.add("opened");
}

/** listen вешает делегированные обработчики кликов на root. */
export function listen(/** @type {Document | HTMLElement} */ root) {
  root.addEventListener("click", (e) => {
    const el = target(e);
    // ⌘+клик — фоном, как в браузерах на Mac; на трекпаде вместо колесика.
    if (/** @type {MouseEvent} */ (e).metaKey && el?.dataset.action === "open") {
      openInBackground(el);
      return;
    }
    const action = el && actions[el.dataset.action || ""];
    if (!action) return;
    Promise.resolve(action(el)).catch((err) => toast(err.message));
  });
  // Колесико — тоже фоном. mouseup, а не auxclick: mouseup средней кнопкой
  // приходит в любом WebKit.
  root.addEventListener("mouseup", (e) => {
    const el = target(e);
    if (/** @type {MouseEvent} */ (e).button !== 1 || el?.dataset.action !== "open") return;
    e.preventDefault();
    openInBackground(el);
  });
}
