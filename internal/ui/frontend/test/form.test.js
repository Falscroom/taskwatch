import assert from "node:assert/strict";
import { test } from "node:test";
import { FIELDS, inputsFor, payload, probeHTML } from "../js/form.js";

const cfg = {
  configPath: "/x/config.yaml", configured: true, pollInterval: "2m0s",
  gitlab: { url: "https://gl", username: "me", tokenSet: true, ok: true },
  youtrack: { url: "", login: "", query: "", tokenSet: false, ok: false },
  rules: {
    requiredApprovals: 3, reviewState: "Code Review", nextCommand: "State QA", autoTransition: true,
    releaseBranchPrefix: "release/", mrLookback: "336h0m0s", excludeDraft: false, mrBehindCommits: 0,
  },
};

test("every field id is unique", () => {
  assert.equal(new Set(FIELDS.map((f) => f.id)).size, FIELDS.length);
});

test("inputsFor fills the form from config", () => {
  const in_ = inputsFor(cfg);
  assert.deepEqual(in_["gl-url"], { value: "https://gl" });
  assert.deepEqual(in_["r-approvals"], { value: "3" });
  assert.deepEqual(in_["r-auto"], { checked: true });
  assert.deepEqual(in_["r-nodraft"], { checked: false });
  assert.deepEqual(in_["r-lookback"], { value: "336h" });
  assert.deepEqual(in_["r-poll"], { value: "2m" });
  assert.deepEqual(in_["gl-token"], { value: "", placeholder: "сохранён в связке ключей · пусто = не менять" });
  assert.deepEqual(in_["yt-token"], { value: "", placeholder: "permanent token, perm:…" });
});

test("payload reads the form back into the request body", () => {
  const values = {
    "gl-url": " https://gl ", "gl-username": "me", "gl-token": " glpat ", "yt-url": "", "yt-login": "",
    "yt-query": "", "yt-token": "", "r-approvals": "x", "r-review-state": "Code Review", "r-next-command": "",
    "r-auto": true, "r-release": "release/", "r-lookback": "48h", "r-behind": "7", "r-poll": "5m", "r-nodraft": false,
  };
  const body = payload((id) => (typeof values[id] === "boolean" ? { checked: values[id] } : { value: values[id] }));
  assert.deepEqual(body, {
    pollInterval: "5m",
    gitlab: { url: "https://gl", username: "me", token: "glpat" },
    youtrack: { url: "", login: "", query: "", token: "" },
    rules: {
      requiredApprovals: 0, reviewState: "Code Review", nextCommand: "", autoTransition: true,
      releaseBranchPrefix: "release/", mrLookback: "48h", mrBehindCommits: 7, excludeDraft: false,
    },
  });
});

test("probeHTML", () => {
  const html = probeHTML({ gitlab: { ok: true, user: "me" }, youtrack: { skipped: true } });
  assert.match(html, /class="ok">✓ GitLab — @me/);
  assert.match(html, /YouTrack не заполнен/);
  assert.match(probeHTML({ gitlab: { error: "<401>" }, youtrack: { skipped: true } }), /✗ GitLab: &lt;401&gt;/);
});
