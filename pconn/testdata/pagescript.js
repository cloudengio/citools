// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

// pagescript runs the script of the page that 'pconn serve' serves against a
// stand-in for the browser, clicks its buttons in turn, and prints what the
// script did as JSON, so that a Go test can check how the script behaves and
// not only what it looks like: a script that renders correctly can still fail
// when it runs, and a button whose handler throws simply does nothing.
//
// usage: node pagescript.js <script> <clicks>
//
// clicks is a JSON list of {kind, target, members, open}, kind being start or
// stop, members the connections the button affects and open the urls of its
// pages. PCONN_PENDING names the targets, comma separated, that never become
// established, as a group that is failing to come up does not, and
// PCONN_FAIL_START those whose start is refused.
"use strict";

const fs = require("fs");
const vm = require("vm");

const [scriptPath, clicksJSON] = process.argv.slice(2);
const script = fs.readFileSync(scriptPath, "utf8");
const clicks = JSON.parse(clicksJSON);
const pending = new Set((process.env.PCONN_PENDING || "").split(",").filter(Boolean));
const failStart = new Set((process.env.PCONN_FAIL_START || "").split(",").filter(Boolean));

const report = {
  opened: [],
  blurred: 0,
  focused: 0,
  fetches: [],
  messages: {},
  errors: [],
};

// The reports the script makes, one line per target.
const lines = [];
const messages = {
  querySelector: (sel) => {
    const m = /data-target='(.*)'/.exec(sel);
    return (m && lines.find((l) => l.dataset.target === m[1])) || null;
  },
  appendChild: (line) => lines.push(line),
};

function fakeLine() {
  const line = { dataset: {}, bad: false, text: "" };
  // The report is of the line as it stands, whichever of its text and its
  // marking as an error the script sets first.
  const record = () => (report.messages[line.dataset.target] = { text: line.text, bad: line.bad });
  line.classList = {
    toggle: (_, on) => {
      line.bad = !!on;
      record();
    },
  };
  Object.defineProperty(line, "textContent", {
    set: (v) => {
      line.text = v;
      record();
    },
    get: () => line.text,
  });
  return line;
}

let clickHandler = null;
const document = {
  getElementById: (id) => (id === "messages" ? messages : null),
  querySelectorAll: () => [],
  createElement: () => fakeLine(),
  addEventListener: (type, fn) => {
    if (type === "click") {
      clickHandler = fn;
    }
  },
};

const window = {
  open: (url, name) => {
    report.opened.push({ url, name });
    return { blur: () => report.blurred++ };
  },
  focus: () => report.focused++,
};

function targetOf(url) {
  const m = /[?&]target=([^&]*)/.exec(url);
  return m ? decodeURIComponent(m[1]) : "";
}

async function fetch(url, opts = {}) {
  report.fetches.push({
    url,
    method: opts.method || "GET",
    fromPage: (opts.headers || {})["X-Pconn"] === "1",
  });
  const target = targetOf(url);
  if (url.startsWith("/api/start") && failStart.has(target)) {
    return {
      ok: false,
      statusText: "Conflict",
      json: async () => ({ error: "local port 8080 is already forwarded by \"other\"" }),
    };
  }
  if (url.startsWith("/api/established")) {
    const body = pending.has(target)
      ? { ready: false, waiting: target, error: "connecting: no route to host" }
      : { ready: true };
    return { ok: true, json: async () => body };
  }
  return { ok: true, statusText: "OK", json: async () => ({}), text: async () => "<html></html>" };
}

// An error that the script does not handle is what makes a button go dead,
// so it is what is being looked for.
process.on("unhandledRejection", (err) => {
  report.errors.push("unhandled: " + ((err && err.message) || err));
});

const context = vm.createContext({
  document,
  window,
  fetch,
  CSS: { escape: (s) => s },
  DOMParser: class {
    parseFromString() {
      return { getElementById: () => null };
    }
  },
  // The page polls on a timer, which is left out; a wait within an operation
  // is let run, without waiting, so that a target that never becomes
  // established keeps polling for as long as the test runs.
  setInterval: () => 0,
  setTimeout: (fn) => setImmediate(fn),
  console,
});

try {
  vm.runInContext(script, context);
} catch (err) {
  report.errors.push("the script did not run: " + err.message);
}

async function settle(ticks) {
  for (let i = 0; i < ticks; i++) {
    await new Promise((resolve) => setImmediate(resolve));
  }
}

async function main() {
  if (!clickHandler) {
    report.errors.push("the script registered no click handler");
    return;
  }
  for (const c of clicks) {
    const dataset = { members: JSON.stringify(c.members || [c.target]) };
    if (c.kind === "start") {
      dataset.start = c.target;
      dataset.open = c.open || "";
    } else {
      dataset.stop = c.target;
    }
    const button = { disabled: false, dataset };
    try {
      clickHandler({ target: { closest: (sel) => (sel === "button" ? button : null) } });
    } catch (err) {
      report.errors.push("the click handler threw: " + err.message);
    }
    // Long enough for an operation that can finish to have finished, and for
    // one that cannot to be in the middle of waiting, before the next click.
    await settle(40);
  }
  await settle(40);
}

main().then(() => {
  process.stdout.write(JSON.stringify(report));
  // A target that never becomes established would otherwise poll forever.
  process.exit(0);
});
