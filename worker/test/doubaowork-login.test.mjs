import assert from "node:assert/strict";
import { test } from "node:test";
import { isDoubaoSession, normalizeCookies, normalizeWebContext } from "../src/doubaowork-login.mjs";

test("recognises a doubao session cookie", () => {
  assert.equal(isDoubaoSession({ name: "sessionid", value: "abc", domain: ".doubao.com" }), true);
  assert.equal(isDoubaoSession({ name: "sessionid_ss", value: "abc", domain: "www.doubao.com" }), true);
  assert.equal(isDoubaoSession({ name: "sessionid", value: "", domain: ".doubao.com" }), false);
  assert.equal(isDoubaoSession({ name: "ttwid", value: "abc", domain: ".doubao.com" }), false);
  assert.equal(isDoubaoSession({ name: "sessionid", value: "abc", domain: ".example.com" }), false);
});

test("normalises only doubao cookies into the provider credential shape", () => {
  const out = normalizeCookies([
    { name: "sessionid", value: "abc", domain: ".doubao.com", path: "/", expires: 1795760557, secure: true, httpOnly: true },
    { name: "ttwid", value: "t", domain: ".doubao.com" },
    { name: "other", value: "x", domain: ".example.com" },
  ]);
  assert.equal(out.length, 2);
  assert.deepEqual(out[0], {
    name: "sessionid", value: "abc", domain: ".doubao.com", path: "/",
    expires: 1795760557, secure: true, http_only: true,
  });
  assert.equal(out[1].http_only, false);
  assert.equal(out[1].path, "/");
});

test("keeps only observed web context values", () => {
  const web = normalizeWebContext({
    web_id: " 555 ",
    device_id: " dev-1 ",
    client_version: " 2.8.0 ",
    chromium_version: " 147 ",
    channel: " chrome ",
    user_agent: " Mozilla/5.0 ",
    ignored: "nope",
  });
  assert.deepEqual(web, {
    web_id: "555",
    device_id: "dev-1",
    client_version: "2.8.0",
    chromium_version: "147",
    channel: "chrome",
    user_agent: "Mozilla/5.0",
    client_profile: "web",
  });
  assert.equal(normalizeWebContext().web_id, "");
});
