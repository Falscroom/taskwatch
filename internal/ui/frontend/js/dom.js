// @ts-check
// Мелочи работы с DOM.

/**
 * $ — элемент по id; отсутствие — ошибка разметки, а не повод молча продолжать.
 * @template {HTMLElement} [T=HTMLElement]
 * @param {string} id
 * @returns {T}
 */
export function $(id) {
  const el = document.getElementById(id);
  if (!el) throw new Error(`#${id} not found`);
  return /** @type {T} */ (el);
}

/** @type {ReturnType<typeof setTimeout> | undefined} */
let toastTimer;

/** toast — короткое сообщение внизу окна на 4 секунды. */
export function toast(msg) {
  const el = $("toast");
  el.textContent = msg;
  el.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { el.hidden = true; }, 4000);
}

/** @param {number} ms */
export const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

/**
 * busy выключает кнопку на время fn и возвращает её обратно.
 * @template T
 * @param {HTMLButtonElement} btn
 * @param {() => Promise<T>} fn
 */
export async function busy(btn, fn) {
  btn.disabled = true;
  try {
    return await fn();
  } finally {
    btn.disabled = false;
  }
}
