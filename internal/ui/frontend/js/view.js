// @ts-check
// Вид: показать состояние и держать его свежим.
import { api } from "./api.js";
import { markQueued, pending } from "./batch.js";
import { renderList } from "./cards.js";
import { $, sleep, toast } from "./dom.js";
import { ago, notice } from "./format.js";
import { state } from "./store.js";

export function render() {
  document.querySelectorAll("#tabs [data-tab]").forEach((b) => {
    b.classList.toggle("active", /** @type {HTMLElement} */ (b).dataset.tab === state.tab);
  });
  const v = state.view;
  if (!v) return;
  $("updated").textContent = ago(v.updatedAt);
  $("needs-count").textContent = String(v.needsYou);
  paintNotice(v);
  $("list").innerHTML = renderList(v, state.tab, state.open);
  markQueued($("list"), pending);
}

// Подробности ошибки свёрнуты, пока их не раскроют, и остаются раскрытыми до
// следующей ошибки: render зовётся по таймеру и не должен их захлопывать.
let detailsOpen = false;
let detailsOf = "";

/** toggleDetails — «подробнее»: показать сырую ошибку или спрятать обратно. */
export function toggleDetails() {
  detailsOpen = !detailsOpen;
  render();
}

/** paintNotice — строка над списком: ошибка опроса и/или сон вне рабочего времени. */
function paintNotice(/** @type {import("./cards.js").View} */ v) {
  const n = notice(v);
  const detail = n?.detail || "";
  if (detail !== detailsOf) {
    detailsOf = detail;
    detailsOpen = false;
  }
  $("notice").hidden = !n;
  if (!n) return;
  $("notice").classList.toggle("bad", n.bad);
  $("notice-text").textContent = n.text;
  $("notice-sub").textContent = n.sub;
  $("notice-sub").hidden = !n.sub;
  $("notice-more").hidden = !detail;
  $("notice-more").textContent = detailsOpen ? "скрыть" : "подробнее";
  $("notice-raw").textContent = detail;
  $("notice-raw").hidden = !detail || !detailsOpen;
}

// watchView держит вид свежим: long-poll /api/view?after=<version> отвечает,
// как только у движка новая версия (прошёл опрос, сменились заглушения), — так
// результат любого действия приходит сам, без опроса по таймеру.
export async function watchView() {
  let failing = false;
  for (;;) {
    try {
      const after = state.view ? `?after=${state.view.version}` : "";
      state.view = await api("/api/view" + after);
      failing = false;
      $("refresh").classList.remove("spinning");
      render();
    } catch (e) {
      if (!failing) toast(e.message); // о пропаже связи — один раз, а не каждые 5 секунд
      failing = true;
      await sleep(5000);
    }
  }
}
