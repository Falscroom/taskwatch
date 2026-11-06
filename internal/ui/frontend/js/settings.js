// @ts-check
// Окно настроек: заполнить по таблице полей, сохранить, проверить связь.
// Что форма заполнена правильно, решает сервер — его ошибку показываем как есть.
import { api } from "./api.js";
import { $, busy, toast } from "./dom.js";
import { boardsHTML, FIELDS, inputsFor, payload, probeHTML } from "./form.js";
import { state } from "./store.js";

/** @typedef {import("./form.js").ConfigForm} ConfigForm */

/** @param {string} id */
const input = (id) => /** @type {HTMLInputElement} */ ($(id));

function fill(/** @type {ConfigForm} */ cfg) {
  $("settings-path").textContent = cfg.configPath;
  for (const [id, v] of Object.entries(inputsFor(cfg))) {
    const el = input(id);
    if (v.checked !== undefined) el.checked = v.checked;
    if (v.value !== undefined) el.value = v.value;
    if (v.placeholder !== undefined) el.placeholder = v.placeholder;
  }
  $("settings-probe").hidden = true;
  $("yt-boards").hidden = true;
}

function read() {
  return payload((id) => ({ value: input(id).value, checked: input(id).checked }));
}

export function openSettings() {
  if (state.config) fill(state.config);
  loadLoginItem().catch((err) => toast(err.message));
  $("settings").hidden = false;
  input(FIELDS[0].id).focus();
}

export function closeSettings() {
  $("settings").hidden = true;
}

/** loadConfig подтягивает настройки с сервера; без них окно сразу открывает форму. */
export async function loadConfig() {
  state.config = await api("/api/config");
  if (!state.config?.configured) openSettings();
}

/** @param {SubmitEvent} e */
export async function saveSettings(e) {
  e.preventDefault();
  const btn = /** @type {HTMLButtonElement} */ ($("settings-form").querySelector('[type="submit"]'));
  try {
    await busy(btn, async () => {
      await api("/api/config", read());
      state.config = await api("/api/config");
    });
    closeSettings();
    toast("Настройки сохранены");
  } catch (err) {
    toast(err.message);
  }
}

/** @param {HTMLButtonElement} btn */
export async function testSettings(btn) {
  btn.textContent = "Проверяю…";
  try {
    const results = await busy(btn, () => api("/api/config/test", read()));
    $("settings-probe").innerHTML = probeHTML(results);
    $("settings-probe").hidden = false;
  } finally {
    btn.textContent = "Проверить";
  }
}

/** pickBoards показывает доски YouTrack с текущим спринтом по адресу и токену из формы. */
export async function pickBoards(/** @type {HTMLButtonElement} */ btn) {
  const boards = await busy(btn, () => api("/api/config/boards", read()));
  $("yt-boards").innerHTML = boardsHTML(boards);
  $("yt-boards").hidden = false;
}

/** useBoard подставляет запрос тасок выбранной доски. */
export function useBoard(/** @type {HTMLElement} */ el) {
  input("yt-query").value = el.dataset.query || "";
  $("yt-boards").hidden = true;
}

/** loadLoginItem показывает «Запускать при входе», если он есть: только у taskwatch.app. */
async function loadLoginItem() {
  const st = await api("/api/login-item");
  $("login-item-row").hidden = !st.supported;
  input("login-item").checked = st.enabled;
}

/**
 * toggleLoginItem включает или выключает запуск при входе — сразу, без «Сохранить»,
 * как в системных настройках. Показывает то, что вышло на деле.
 * @param {HTMLInputElement} box
 */
export async function toggleLoginItem(box) {
  const want = box.checked;
  try {
    const st = await api("/api/login-item", { enabled: want });
    box.checked = st.enabled;
    toast(st.enabled ? "taskwatch будет запускаться при входе" : "Запуск при входе выключен");
  } catch (err) {
    box.checked = !want;
    throw err;
  }
}

const CLEAR_LABEL = "Удалить токены из Связки ключей";
/** @type {ReturnType<typeof setTimeout> | undefined} */
let clearTimer;

/**
 * clearSecrets удаляет токены — перед тем как выбросить приложение в Корзину.
 * Первое нажатие только спрашивает: кнопка ждёт второго 4 секунды.
 * @param {HTMLButtonElement} btn
 */
export async function clearSecrets(btn) {
  if (!btn.dataset.armed) {
    btn.dataset.armed = "1";
    btn.textContent = "Точно удалить? Нажми ещё раз";
    clearTimer = setTimeout(() => {
      delete btn.dataset.armed;
      btn.textContent = CLEAR_LABEL;
    }, 4000);
    return;
  }
  clearTimeout(clearTimer);
  delete btn.dataset.armed;
  btn.textContent = CLEAR_LABEL;
  await busy(btn, () => api("/api/secrets/clear", {}));
  state.config = await api("/api/config");
  if (state.config) fill(state.config);
  toast("Токены удалены из Связки ключей");
}
