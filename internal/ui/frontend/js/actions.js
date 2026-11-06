// @ts-check
// Все клики окна — через один делегированный обработчик: элемент помечен
// data-action="имя", а здесь — что это имя делает. Срабатывает ближайший
// к месту клика элемент, поэтому кнопка на карточке не открывает саму карточку.
import { api, openURL } from "./api.js";
import { toast } from "./dom.js";
import { closeMute, muteFor, openMute } from "./mute.js";
import { closeSettings, openSettings, pickBoards, testSettings, useBoard } from "./settings.js";
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
};

/** listen вешает делегированный обработчик кликов на root. */
export function listen(/** @type {Document | HTMLElement} */ root) {
  root.addEventListener("click", (e) => {
    const el = /** @type {HTMLElement | null} */ (/** @type {Element} */ (e.target).closest("[data-action]"));
    const action = el && actions[el.dataset.action || ""];
    if (!action) return;
    Promise.resolve(action(el)).catch((err) => toast(err.message));
  });
}
