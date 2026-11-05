import assert from "node:assert/strict";
import { test } from "node:test";
import { ago, esc, fmtDur, loaded, muteLabel } from "../js/format.js";

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
