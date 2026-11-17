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
    timezone: "Europe/London",
    netMode: "dhcp",
    claim: "console",
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
  assert.match(html, /releases\/latest\/download\/feldcore-os\.raw\.zst/);
  assert.doesNotMatch(html, /fetch\s*\(/);
  assert.doesNotMatch(html, /XMLHttpRequest/);
  assert.doesNotMatch(html, /sendBeacon/);
  assert.doesNotMatch(html, /google-analytics/);
  assert.doesNotMatch(html, /<script[^>]+src=/);
});

test("setup code file omits the administrator", () => {
  const built = run(base({
    claim: "code",
    setupCode: "abcd-2345",
    netMode: "dhcp",
    dataPool: false,
    adminUser: "admin",
    adminPass: pass,
  }));
  assert.deepEqual(built.errors, []);
  const file = JSON.parse(built.json);
  assert.equal(file.version, 1);
  assert.equal(file.hostname, "feldbox");
  assert.equal(file.timezone, "Europe/London");
  assert.equal(file.network.mode, "dhcp");
  assert.equal(file.network.address, undefined);
  assert.equal(file.setupCode, "ABCD2345");
  assert.equal(file.admin, undefined);
  assert.equal(file.ssh, undefined);
  assert.equal(file.wifi, undefined);
  assert.equal(file.dataPool, false);
  assert.equal(built.json.endsWith("\n"), true);
});

test("admin, static wifi, and ssh key", () => {
  const built = run(base({
    claim: "admin",
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
    userName: "ada",
    userKeys: key,
    userSudo: true,
    dataPool: true,
  }));
  assert.deepEqual(built.errors, []);
  const file = JSON.parse(built.json);
  assert.equal(file.admin.username, "admin");
  assert.equal(file.admin.password, pass);
  assert.equal(file.admin.skipTotp, true);
  assert.equal(file.setupCode, undefined);
  assert.equal(file.network.address, "192.168.1.20/24");
  assert.equal(file.network.iface, undefined);
  assert.equal(file.wifi.ssid, "lab");
  assert.equal(file.wifi.psk, "wifi-secret-value");
  assert.equal(file.wifi.country, "GB");
  assert.equal(file.ssh.enable, true);
  assert.equal(file.ssh.permitRoot, true);
  assert.deepEqual(file.ssh.authorizedKeys, [key]);
  assert.equal(file.user.name, "ada");
  assert.equal(file.user.password, undefined);
  assert.equal(file.user.sudo, true);
  assert.deepEqual(file.user.authorizedKeys, [key]);
  assert.equal(file.dataPool, true);
  assert.equal(Object.hasOwn(file, "unixUser"), false);
});

test("refuses a private key, a weak password, and a wired address beside wifi", () => {
  const built = run(base({
    claim: "admin",
    adminUser: "admin",
    adminPass: "password",
    netMode: "static",
    iface: "eth0",
    address: "192.168.1.20/24",
    gateway: "10.0.0.1",
    ssid: "lab",
    ssh: true,
    rootKeys: "-----BEGIN OPENSSH PRIVATE KEY-----\nsecret",
    setupCode: "ABCD2345",
  }));
  assert.ok(built.errors.length >= 3);
  assert.equal(built.json, "");
  assert.ok(built.errors.some((e) => /private key/i.test(e)));
  assert.ok(built.errors.some((e) => /12 characters|password/i.test(e)));
  assert.ok(built.errors.some((e) => /wireless/i.test(e)));
});

test("refuses a setup code that uses ambiguous characters", () => {
  const built = run(base({ claim: "code", setupCode: "OOOO-1111" }));
  assert.equal(built.json, "");
  assert.ok(built.errors.some((e) => /setup code/i.test(e)));
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
});
