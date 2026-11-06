// @ts-check
// Обращение к Go-части окна (internal/ui/httpapi).
import { toast } from "./dom.js";

/**
 * api — GET path или POST body как JSON. Ошибка сервера приходит как {error}.
 * @param {string} path
 * @param {unknown} [body]
 */
export async function api(path, body) {
  const res = await fetch(path, body === undefined ? {} : {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    let msg = res.statusText;
    try { msg = (await res.json()).error || msg; } catch { /* тело не JSON */ }
    throw new Error(msg);
  }
  return res.status === 204 ? null : res.json();
}

/** openURL открывает ссылку в браузере пользователя (не в окне). */
export function openURL(/** @type {string | undefined} */ url) {
  if (url) api("/api/open", { url }).catch((e) => toast(e.message));
}

/** openBatch открывает пачку ссылок одним вызовом, прося не выводить браузер вперёд. */
export function openBatch(/** @type {string[]} */ urls) {
  if (urls.length) api("/api/open", { urls, background: true }).catch((e) => toast(e.message));
}
