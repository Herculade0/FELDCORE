import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import vm from "node:vm";

const html = readFileSync(new URL("./index.html", import.meta.url), "utf8");
const script = html.match(/<script>([\s\S]*?)<\/script>/)[1];
const context = { TextEncoder };
context.globalThis = context;
vm.createContext(context);
vm.runInContext(script, context);
const buildInstall = context.buildInstall;

const key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFakeKeyForTestsOnly answer";
const pass = "Correct Horse Battery 1";

function base(extra) {
  return Object.assign({
    hostname: "feldbox",
    timezone: "Australia/Brisbane",
    netMode: "dhcp",
    dataPool: true,
  }, extra);
}

function run(input) {
  const built = buildInstall(input);
  return { errors: Array.from(built.errors, String), json: String(built.json || "") };
}

test("page stays local", () => {
  assert.match(html, /not encrypted/);
  assert.match(html, /SSH public key instead of a password/);
  assert.match(html, /aria-hidden="true"/);
  assert.match(html, /A FELDCORP SYSTEM/);
  assert.match(html, /--accent-2:\s*#38bdf8/);
  assert.doesNotMatch(html, /<img\b/);
  assert.doesNotMatch(html, /url\(\s*https?:/);
  assert.doesNotMatch(html, /Signed OS image/);
  assert.doesNotMatch(html, /feldcore-os\.raw/);
  assert.doesNotMatch(html, /name="setupCode"|name="claim"|name="userName"|name="userKeys"|name="userPass"|name="userSudo"/);
  assert.doesNotMatch(html, /fetch\s*\(/);
  assert.doesNotMatch(html, /XMLHttpRequest/);
  assert.doesNotMatch(html, /sendBeacon/);
  assert.doesNotMatch(html, /google-analytics/);
  assert.doesNotMatch(html, /<script[^>]+src=/);
});

test("time zone defaults to Australia/Brisbane", () => {
  const input = html.match(/<input name="timezone"[^>]*>/)[0];
  assert.match(input, /value="Australia\/Brisbane"/);
  const zones = [...html.matchAll(/<option value="([^"]+)"><\/option>/g)].map((m) => m[1]);
  const australia = [
    "Australia/Brisbane",
    "Australia/Sydney",
    "Australia/Melbourne",
    "Australia/Adelaide",
    "Australia/Perth",
    "Australia/Darwin",
    "Australia/Hobart",
    "Australia/Broken_Hill",
    "Australia/Lord_Howe",
    "Australia/Lindeman",
    "Australia/Eucla",
  ];
  assert.equal(zones.filter((zone) => zone.startsWith("Australia/")).length, australia.length);
  for (const zone of australia) assert.ok(zones.includes(zone), zone);
  for (const zone of ["UTC", "Europe/London", "Europe/Paris", "Europe/Berlin", "America/New_York", "America/Chicago", "America/Denver", "America/Los_Angeles", "America/Sao_Paulo", "Asia/Tokyo", "Asia/Shanghai", "Asia/Singapore", "Asia/Kolkata", "Asia/Dubai", "Pacific/Auckland"]) {
    assert.ok(zones.includes(zone), zone);
  }
  for (const zone of zones) {
    const built = run(base({ timezone: zone }));
    assert.deepEqual(built.errors, [], zone);
    assert.equal(JSON.parse(built.json).timezone, zone);
  }
});

test("blank administrator omits admin and setupCode", () => {
  const built = run(base({
    adminUser: "",
    adminPass: "",
    setupCode: "ABCD-2345",
    claim: "console",
    userName: "ada",
    userKeys: key,
    userPass: pass,
    userSudo: true,
    dataPool: false,
  }));
  assert.deepEqual(built.errors, []);
  const file = JSON.parse(built.json);
  assert.equal(file.version, 1);
  assert.equal(file.hostname, "feldbox");
  assert.equal(file.timezone, "Australia/Brisbane");
  assert.equal(file.network.mode, "dhcp");
  assert.equal(file.network.address, undefined);
  assert.equal(file.admin, undefined);
  assert.equal(file.setupCode, undefined);
  assert.equal(file.user, undefined);
  assert.equal(file.ssh, undefined);
  assert.equal(file.wifi, undefined);
  assert.equal(file.dataPool, false);
  assert.equal(built.json.endsWith("\n"), true);
});

test("filled administrator is written", () => {
  const built = run(base({
    adminUser: "admin",
    adminPass: pass,
  }));
  assert.deepEqual(built.errors, []);
  const file = JSON.parse(built.json);
  assert.equal(file.admin.username, "admin");
  assert.equal(file.admin.password, pass);
  assert.equal(file.admin.skipTotp, true);
  assert.equal(file.setupCode, undefined);
  assert.equal(file.user, undefined);
});

test("password strength applies only when a password is entered", () => {
  const blank = run(base({ adminUser: "   ", adminPass: "" }));
  assert.deepEqual(blank.errors, []);
  assert.equal(JSON.parse(blank.json).admin, undefined);
  const nameOnly = run(base({ adminUser: "admin", adminPass: "" }));
  assert.equal(nameOnly.json, "");
  assert.ok(nameOnly.errors.some((e) => /password/i.test(e)));
  assert.ok(!nameOnly.errors.some((e) => /12 characters/.test(e)));
  const weak = run(base({ adminUser: "admin", adminPass: "password" }));
  assert.equal(weak.json, "");
  assert.ok(weak.errors.some((e) => /password/i.test(e)));
});

test("country select value flows through only with WiFi", () => {
  const select = html.match(/<select name="country">[\s\S]*?<\/select>/)[0];
  assert.match(select, /<option value="">—<\/option>/);
  assert.match(select, /<option value="AU" selected>Australia<\/option>/);
  assert.match(select, /<option value="NZ">New Zealand<\/option>/);
  assert.match(select, /<option value="GB">United Kingdom<\/option>/);
  assert.match(select, /<option value="US">United States<\/option>/);
  assert.match(select, /<option value="CA">Canada<\/option>/);
  assert.match(select, /<option value="DE">Germany<\/option>/);
  assert.match(select, /<option value="FR">France<\/option>/);
  const codes = [...select.matchAll(/<option value="([A-Za-z]{2})"/g)].map((m) => m[1]);
  assert.equal(new Set(codes).size, codes.length);
  assert.equal(codes.length, 249);
  assert.equal([...select.matchAll(/ selected/g)].length, 1);

  const built = run(base({
    ssid: "lab",
    psk: "wifi-secret-value",
    country: "au",
  }));
  assert.deepEqual(built.errors, []);
  assert.equal(JSON.parse(built.json).wifi.ssid, "lab");
  assert.equal(JSON.parse(built.json).wifi.country, "AU");

  const wired = run(base({ country: "AU" }));
  assert.deepEqual(wired.errors, []);
  assert.equal(JSON.parse(wired.json).wifi, undefined);

  const bad = run(base({ country: "Australia" }));
  assert.equal(bad.json, "");
  assert.ok(bad.errors.some((e) => /two-letter/i.test(e)));
});

test("admin, static wifi, and ssh key", () => {
  const built = run(base({
    adminUser: "admin",
    adminPass: pass,
    netMode: "static",
    address: "192.168.1.20/24",
    gateway: "192.168.1.1",
    dns: "1.1.1.1, 8.8.8.8",
    ssid: "lab",
    psk: "wifi-secret-value",
    country: "gb",
    ssh: true,
    permitRoot: true,
    rootKeys: key,
    dataPool: true,
  }));
  assert.deepEqual(built.errors, []);
  const file = JSON.parse(built.json);
  assert.equal(file.admin.username, "admin");
  assert.equal(file.admin.password, pass);
  assert.equal(file.admin.skipTotp, true);
  assert.equal(file.setupCode, undefined);
  assert.equal(file.user, undefined);
  assert.equal(file.network.address, "192.168.1.20/24");
  assert.equal(file.network.iface, undefined);
  assert.equal(file.wifi.ssid, "lab");
  assert.equal(file.wifi.psk, "wifi-secret-value");
  assert.equal(file.wifi.country, "GB");
  assert.equal(file.ssh.enable, true);
  assert.equal(file.ssh.permitRoot, true);
  assert.deepEqual(file.ssh.authorizedKeys, [key]);
  assert.equal(file.dataPool, true);
  assert.equal(Object.hasOwn(file, "unixUser"), false);
});

test("refuses a private key, a weak password, and a wired address beside wifi", () => {
  const built = run(base({
    adminUser: "admin",
    adminPass: "password",
    netMode: "static",
    iface: "eth0",
    address: "192.168.1.20/24",
    gateway: "10.0.0.1",
    ssid: "lab",
    ssh: true,
    rootKeys: "-----BEGIN OPENSSH PRIVATE KEY-----\nsecret",
  }));
  assert.ok(built.errors.length >= 3);
  assert.equal(built.json, "");
  assert.ok(built.errors.some((e) => /private key/i.test(e)));
  assert.ok(built.errors.some((e) => /12 characters|password/i.test(e)));
  assert.ok(built.errors.some((e) => /wireless/i.test(e)));
});

test("a root key requires SSH", () => {
  const built = run(base({ ssh: false, rootKeys: key }));
  assert.equal(built.json, "");
  assert.ok(built.errors.some((e) => /Turn SSH on/i.test(e)));
  assert.ok(!built.errors.some((e) => /Unix user/i.test(e)));
});

test("ssh stays out of the file until it is enabled", () => {
  const off = run(base({ ssh: false, permitRoot: true, rootKeys: "" }));
  assert.deepEqual(off.errors, []);
  assert.equal(JSON.parse(off.json).ssh, undefined);
  const on = run(base({
    ssh: true,
    permitRoot: false,
    rootKeys: key + "\n\n" + key,
  }));
  assert.deepEqual(on.errors, []);
  const file = JSON.parse(on.json);
  assert.equal(file.ssh.permitRoot, false);
  assert.equal(file.ssh.authorizedKeys.length, 2);
  assert.equal(file.user, undefined);
  assert.equal(file.admin, undefined);
  assert.equal(file.setupCode, undefined);
});
