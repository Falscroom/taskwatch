import assert from "node:assert/strict";
import { test } from "node:test";
import { ago, emptyText, esc, fmtDur, loaded, muteLabel, notice, updatedLabel, wakeLabel } from "../js/format.js";

test("esc", () => {
  assert.equal(esc(`<a href="x">'&'</a>`), "&lt;a href=&quot;x&quot;&gt;&#39;&amp;&#39;&lt;/a&gt;");
  assert.equal(esc(null), "");
  assert.equal(esc(42), "42");
});

test("loaded: zero date from the server is not a real time", () => {
  assert.equal(loaded("0001-01-01T00:00:00Z"), false);
  assert.equal(loaded(""), false);
  assert.equal(loaded(undefined), false);
  assert.equal(loaded("2026-11-05T10:00:00Z"), true);
});

test("ago", () => {
  const now = Date.parse("2026-11-05T12:00:00Z");
  assert.equal(ago("0001-01-01T00:00:00Z", now), "загрузка…");
  assert.equal(ago("2026-11-05T11:59:30Z", now), "только что");
  assert.equal(ago("2026-11-05T11:55:00Z", now), "5 мин назад");
  assert.equal(ago("2026-11-05T09:30:00Z", now), "2 ч назад");
  assert.equal(ago("2026-11-05T12:00:10Z", now), "только что"); // часы сервера чуть впереди
});

test("muteLabel", () => {
  const now = new Date(2026, 10, 5, 12, 0).getTime();
  assert.equal(muteLabel(undefined, now), "навсегда");
  assert.equal(muteLabel("", now), "навсегда");
  assert.equal(muteLabel(new Date(2026, 10, 5, 20, 5).toISOString(), now), "до 20:05");
  assert.equal(muteLabel(new Date(2026, 10, 7, 9, 30).toISOString(), now), "до 7 ноя 09:30");
});

test("fmtDur", () => {
  assert.equal(fmtDur("336h0m0s"), "336h");
  assert.equal(fmtDur("2m0s"), "2m");
  assert.equal(fmtDur("1h30m0s"), "1h30m");
  assert.equal(fmtDur("0s"), "0s");
  assert.equal(fmtDur(""), "0s");
  assert.equal(fmtDur("1.5h"), "1.5h"); // не наш формат — как есть
});

test("wakeLabel", () => {
  const now = new Date(2026, 10, 6, 20, 0).getTime(); // пятница, вечер
  assert.equal(wakeLabel(new Date(2026, 10, 6, 21, 30).toISOString(), now), "в 21:30");
  assert.equal(wakeLabel(new Date(2026, 10, 7, 10, 0).toISOString(), now), "завтра в 10:00");
  assert.equal(wakeLabel(new Date(2026, 10, 9, 10, 0).toISOString(), now), "в пн 10:00");
});

test("notice: ошибка, сон, и то и другое", () => {
  const now = Date.parse("2026-11-07T22:00:00Z");
  const sleep = "2026-11-09T09:00:00+03:00";

  assert.equal(notice({}, now), null);

  assert.deepEqual(notice({ sleepUntil: sleep }, now), {
    bad: false,
    text: `Вне рабочего времени · опрос ${wakeLabel(sleep, now)} · «Обновить» — опросить сейчас`,
    sub: "",
    detail: "",
  });

  // Ошибка важнее сна, но сон не теряется — уходит второй строкой.
  const both = notice({ error: "Нет связи с gitlab.xbet.lan — включи VPN или проверь сеть", errorDetail: "dial tcp: boom", sleepUntil: sleep }, now);
  assert.equal(both.bad, true);
  assert.equal(both.text, "Нет связи с gitlab.xbet.lan — включи VPN или проверь сеть");
  assert.equal(both.detail, "dial tcp: boom");
  assert.match(both.sub, /^Вне рабочего времени/);

  // Подробностей нет — «подробнее» показывать нечего.
  assert.equal(notice({ error: "boom" }, now).detail, "");
});

// Первый опрос упал — данных нет и не будет до следующего; «загрузка…»
// и «Загружаю…» висели бы вечно.
test("updatedLabel / emptyText: провалившийся первый опрос — не загрузка", () => {
  const now = Date.parse("2026-11-07T12:00:00Z");
  const never = "0001-01-01T00:00:00Z";

  assert.equal(updatedLabel({ updatedAt: never }, now), "загрузка…");
  assert.equal(updatedLabel({ updatedAt: never, error: "Нет связи" }, now), "не удалось");
  assert.equal(updatedLabel({ updatedAt: "2026-11-07T10:00:00Z", error: "Нет связи" }, now), "2 ч назад");

  assert.equal(emptyText({ updatedAt: never }), "Загружаю…");
  assert.equal(emptyText({ updatedAt: never, error: "Нет связи" }), "Данных пока нет");
  assert.equal(emptyText({ updatedAt: "2026-11-07T10:00:00Z", error: "Нет связи" }), "Ничего не требует внимания");
});
