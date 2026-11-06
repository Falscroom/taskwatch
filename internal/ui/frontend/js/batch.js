// @ts-check
// Пачка ссылок: колесико и ⌘+клик не открывают ссылку сразу, а ставят её в
// очередь. Пачка уходит в браузер одним вызовом через DELAY после последнего
// клика или сразу, когда окно теряет фокус. По одной нельзя: Firefox на каждую
// ссылку из другого приложения выходит вперёд, окно taskwatch прячется, и
// следующую карточку уже не кликнуть.

import { openBatch } from "./api.js";

export const DELAY = 2000;

/**
 * @typedef {{set: (fn: () => void, ms: number) => unknown, clear: (id: unknown) => void}} Timer
 * @typedef {{toggle: (url: string) => boolean, has: (url: string) => boolean, flush: () => void}} Queue
 */

/** @type {Timer} */
const realTimer = {
  set: (fn, ms) => setTimeout(fn, ms),
  clear: (id) => clearTimeout(/** @type {ReturnType<typeof setTimeout>} */ (id)),
};

/**
 * queue — очередь ссылок с отложенной отправкой: каждый toggle заново отсчитывает
 * DELAY, flush отправляет сразу. Пустую очередь flush не отправляет.
 * @param {(urls: string[]) => void} send
 * @param {Timer} [timer]
 * @returns {Queue}
 */
export function queue(send, timer = realTimer) {
  /** @type {string[]} */
  let urls = [];
  /** @type {unknown} */
  let pending;
  const flush = () => {
    timer.clear(pending);
    const batch = urls;
    urls = [];
    if (batch.length) send(batch);
  };
  return {
    // Повторный клик по той же ссылке убирает её из очереди; true — теперь в очереди.
    toggle(url) {
      const queued = !urls.includes(url);
      urls = queued ? [...urls, url] : urls.filter((u) => u !== url);
      timer.clear(pending);
      if (urls.length) pending = timer.set(flush, DELAY);
      return queued;
    },
    has: (url) => urls.includes(url),
    flush,
  };
}

/**
 * markQueued подсвечивает в root ссылки из очереди q — после клика и после
 * перерисовки списка, которая подсветку стирает.
 * @param {ParentNode} root
 * @param {Queue} q
 */
export function markQueued(root, q) {
  root.querySelectorAll("[data-url]").forEach((el) => {
    el.classList.toggle("queued", q.has(/** @type {HTMLElement} */ (el).dataset.url || ""));
  });
}

/** pending — очередь окна; после отправки подсветка снимается. */
export const pending = queue((urls) => {
  openBatch(urls);
  markQueued(document, pending);
});
