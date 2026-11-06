// @ts-check
// Вид: показать состояние и держать его свежим.
import { api } from "./api.js";
import { renderList } from "./cards.js";
import { $, sleep, toast } from "./dom.js";
import { ago, wakeLabel } from "./format.js";
import { state } from "./store.js";

export function render() {
  document.querySelectorAll("#tabs [data-tab]").forEach((b) => {
    b.classList.toggle("active", /** @type {HTMLElement} */ (b).dataset.tab === state.tab);
  });
  const v = state.view;
  if (!v) return;
  $("updated").textContent = ago(v.updatedAt);
  $("needs-count").textContent = String(v.needsYou);
  $("error").hidden = !v.error;
  $("error").textContent = v.error || "";
  $("offhours").hidden = !v.sleepUntil;
  $("offhours").textContent = v.sleepUntil ? `Вне рабочего времени · опрос ${wakeLabel(v.sleepUntil)} · «Обновить» — опросить сейчас` : "";
  $("list").innerHTML = renderList(v, state.tab, state.open);
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
