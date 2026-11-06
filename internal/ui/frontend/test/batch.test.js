import assert from "node:assert/strict";
import { test } from "node:test";
import { DELAY, queue } from "../js/batch.js";

// fakeTimer — один отложенный вызов, время двигает тест.
function fakeTimer() {
  let job = null;
  let now = 0;
  return {
    set: (fn, ms) => (job = { fn, at: now + ms }),
    clear: (id) => { if (job === id) job = null; },
    advance(ms) {
      now += ms;
      if (job && job.at <= now) { const { fn } = job; job = null; fn(); }
    },
  };
}

function setup() {
  const sent = [];
  const timer = fakeTimer();
  return { sent, timer, q: queue((urls) => sent.push(urls), timer) };
}

test("пачка уходит одним вызовом через DELAY после последнего клика, в порядке кликов", () => {
  const { sent, timer, q } = setup();
  q.toggle("https://gl/mr/1");
  timer.advance(DELAY - 100);
  q.toggle("https://gl/mr/2"); // следующий клик заново отсчитывает задержку
  timer.advance(DELAY - 100);
  assert.deepEqual(sent, []);
  timer.advance(100);
  assert.deepEqual(sent, [["https://gl/mr/1", "https://gl/mr/2"]]);
  assert.equal(q.has("https://gl/mr/1"), false, "после отправки очередь пуста");
});

test("повторный клик убирает ссылку; опустевшая очередь ничего не отправляет", () => {
  const { sent, timer, q } = setup();
  assert.equal(q.toggle("https://gl/mr/1"), true);
  assert.equal(q.has("https://gl/mr/1"), true);
  assert.equal(q.toggle("https://gl/mr/1"), false);
  assert.equal(q.has("https://gl/mr/1"), false);
  timer.advance(DELAY * 2);
  assert.deepEqual(sent, []);
});

test("flush — уход из окна: отправить сразу, без ожидания", () => {
  const { sent, timer, q } = setup();
  q.toggle("https://gl/mr/1");
  q.flush();
  assert.deepEqual(sent, [["https://gl/mr/1"]]);
  timer.advance(DELAY);
  q.flush();
  assert.deepEqual(sent, [["https://gl/mr/1"]], "таймер снят, пустая очередь не отправляется");
});
