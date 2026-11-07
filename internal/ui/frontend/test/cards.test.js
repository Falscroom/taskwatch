import assert from "node:assert/strict";
import { test } from "node:test";
import { renderCard, renderList } from "../js/cards.js";

/** @returns {import("../js/cards.js").Card} */
function card(over = {}) {
  return {
    key: "mr:1", group: "work", mine: true, review: false, title: "Fix <login>", url: "https://gl/mr/1",
    author: { username: "me", name: "Me", initials: "ME" }, reviewers: [], behind: 0, comments: 3,
    release: [], badges: [], ...over,
  };
}

const view = (cards, updatedAt = "2026-11-05T10:00:00Z") => ({ version: 1, updatedAt, needsYou: 0, cards });

test("card: title is escaped, the card opens its URL", () => {
  const html = renderCard(card());
  assert.match(html, /Fix &lt;login&gt;/);
  assert.match(html, /<article class="card tone-" data-action="open" data-url="https:\/\/gl\/mr\/1">/);
  assert.match(html, /data-action="mute" data-key="mr:1"/);
  assert.doesNotMatch(html, /data-action="unmute"/);
});

test("card: transition, pipeline, issue chip and badges", () => {
  const html = renderCard(card({
    action: { label: "Ready for QA", issueId: "ABC-1" },
    pipeline: "red", pipelineUrl: "https://gl/p/1",
    issueId: "ABC-1", issueUrl: "https://yt/ABC-1", issueState: "Review",
    badges: [{ text: "Конфликты", tone: "red" }], behind: 4, wait: "2д 1ч", release: [{ branch: "release/1" }],
  }));
  assert.match(html, /data-action="transition" data-issue="ABC-1">→ Ready for QA/);
  assert.match(html, /class="status red" data-action="open" data-url="https:\/\/gl\/p\/1"/);
  assert.match(html, /data-action="open" data-url="https:\/\/yt\/ABC-1"[^>]*>ABC-1 · Review/);
  assert.match(html, /<span class="badge red">Конфликты<\/span>/);
  assert.match(html, /↓4/);
  assert.match(html, /⏳ 2д 1ч/);
  assert.match(html, /release\/1/);
  assert.match(html, /tone-red/);
});

test("card: release chip shows the release build and opens it", () => {
  const html = renderCard(card({
    group: "merged",
    release: [{ branch: "release/1", pipeline: "red", url: "https://gl/p/90" }, { branch: "release/<2>" }],
  }));
  assert.match(html, /class="chip release red" data-action="open" data-url="https:\/\/gl\/p\/90" title="Сборка релиза">release\/1<svg/);
  assert.match(html, /<span class="chip release">release\/&lt;2&gt;<\/span>/);
});

test("card: muted cards offer unmute and show until when", () => {
  const html = renderCard(card({ group: "muted", muted: true }));
  assert.match(html, /data-action="unmute" data-key="mr:1"/);
  assert.match(html, /навсегда/);
  assert.match(html, /class="card tone- muted"/);
});

test("card: task without MR cannot be muted", () => {
  const html = renderCard(card({ key: "task:ABC-1", group: "nomr", author: undefined }));
  assert.doesNotMatch(html, /data-action="mute"/);
  assert.doesNotMatch(html, /class="comments"/);
});

test("list: groups in tab order, collapsed groups toggle", () => {
  const v = view([card({ key: "mr:2", group: "waiting" }), card({ key: "mr:1", group: "work" })]);
  const closed = renderList(v, "needs", new Set());
  assert.ok(closed.indexOf("Твой MR требует работы") < closed.indexOf("Ждём других"));
  assert.match(closed, /data-action="toggle" data-group="needs:waiting"/);
  assert.doesNotMatch(closed, /mr:2/, "collapsed group hides its cards");
  assert.match(renderList(v, "needs", new Set(["needs:waiting"])), /mr:2/);
});

test("list: tab filters and empty states", () => {
  const v = view([card({ key: "mr:9", group: "review", mine: false, review: true })]);
  assert.match(renderList(v, "review", new Set()), /mr:9/);
  assert.match(renderList(v, "mine", new Set()), /Ничего не требует внимания/);
  assert.match(renderList(view([], "0001-01-01T00:00:00Z"), "needs", new Set()), /Загружаю…/);
  const failed = { ...view([], "0001-01-01T00:00:00Z"), error: "Нет связи с gitlab.xbet.lan" };
  assert.match(renderList(failed, "needs", new Set()), /Данных пока нет/, "упавший первый опрос — не загрузка"); 
  assert.match(renderList(v, "unknown-tab", new Set()), /mr:9/, "unknown tab falls back to needs");
});
