// @ts-check
// Диалог «Заглушить»: на сколько и заодно ли все MR с таким же названием.
import { api } from "./api.js";
import { $, busy, toast } from "./dom.js";

/** @type {{key: string, title: string} | null} */
let target = null;

/** @param {import("./cards.js").Card} card */
export function openMute(card) {
  target = { key: card.key, title: card.title };
  $("mute-title").textContent = card.title;
  /** @type {HTMLInputElement} */ ($("mute-also")).checked = false;
  $("mute").hidden = false;
}

export function closeMute() {
  $("mute").hidden = true;
  target = null;
}

/** @param {HTMLButtonElement} btn — кнопка срока, data-hours: 0 — навсегда */
export async function muteFor(btn) {
  if (!target) return;
  const hours = Number(btn.dataset.hours);
  const alsoTitle = /** @type {HTMLInputElement} */ ($("mute-also")).checked;
  await busy(btn, () => api("/api/mute", { key: target?.key, hours, alsoTitle, title: target?.title }));
  closeMute();
  toast(hours ? "Заглушено" : "Заглушено навсегда");
}
