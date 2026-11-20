"use strict";

const list = document.getElementById("entries");
const count = document.getElementById("log-count");
const copyButton = document.getElementById("copy");
const empty = document.getElementById("empty");
const truncatedNote = document.getElementById("truncated");
const feedback = document.getElementById("feedback");
const maxEntries = 500; // matches pépé's in-memory buffer
let entries = [];
let next = 0;
let toastTimer = 0;
let timer = 0;

function toast(text, error = false) {
  window.clearTimeout(toastTimer);
  feedback.textContent = text;
  feedback.classList.toggle("error", error);
  feedback.hidden = false;
  toastTimer = window.setTimeout(() => { feedback.hidden = true; }, error ? 6000 : 2500);
}

// pépé sends its local time; keep it rather than the phone's time zone.
function clock(entry) {
  return entry.time.slice(0, 10) + " " + entry.time.slice(11, 19);
}

function line(entry) {
  return clock(entry) + " " + entry.level + " " + entry.source + ": " + entry.message;
}

function item(entry) {
  const li = document.createElement("li");
  li.className = "log-entry level-" + entry.level.toLowerCase();
  const meta = document.createElement("p");
  meta.className = "log-meta";
  for (const [text, name] of [[entry.time.slice(11, 19), "log-time"], [entry.level, "log-level"], [entry.source, "log-source"]]) {
    const span = document.createElement("span");
    span.className = name;
    span.textContent = text;
    meta.append(span);
  }
  const message = document.createElement("p");
  message.className = "log-message";
  message.textContent = entry.message;
  li.append(meta, message);
  return li;
}

function render(added) {
  const following = window.innerHeight + window.scrollY >= document.documentElement.scrollHeight - 48;
  list.append(...added.map(item));
  while (list.children.length > entries.length) list.firstElementChild.remove();
  count.textContent = entries.length === 1 ? "1 entry" : entries.length + " entries";
  empty.hidden = entries.length > 0;
  copyButton.disabled = entries.length === 0;
  if (following && added.length) window.scrollTo(0, document.documentElement.scrollHeight);
}

async function poll() {
  try {
    const response = await fetch("/api/logs?after=" + next, {cache: "no-store", credentials: "omit", signal: AbortSignal.timeout(6000)});
    if (!response.ok) throw new Error("Disconnected");
    const result = await response.json();
    if (result.next < next) {
      // pépé restarted: start over on the next poll.
      next = 0;
      entries = [];
      list.replaceChildren();
      truncatedNote.hidden = true;
      return;
    }
    entries = entries.concat(result.entries).slice(-maxEntries);
    if (result.truncated) truncatedNote.hidden = false;
    next = result.next;
    render(result.entries);
  } catch (_) {
    count.textContent = "Can't reach pépé";
  } finally {
    schedule();
  }
}

function schedule() {
  window.clearTimeout(timer);
  if (document.visibilityState === "visible") timer = window.setTimeout(poll, 2000);
}

document.addEventListener("visibilitychange", () => {
  if (document.visibilityState === "visible") poll();
  else window.clearTimeout(timer);
});

// The remote is plain HTTP on the LAN, where browsers hide the clipboard
// API; fall back to copying a selected textarea, then to a manual selection.
copyButton.addEventListener("click", async () => {
  const text = entries.map(line).join("\n") + "\n";
  const copied = entries.length === 1 ? "Copied 1 entry" : "Copied " + entries.length + " entries";
  if (navigator.clipboard && window.isSecureContext) {
    try {
      await navigator.clipboard.writeText(text);
      toast(copied);
      return;
    } catch (_) { /* fall back below */ }
  }
  const area = document.createElement("textarea");
  area.className = "copy-buffer";
  area.value = text;
  area.readOnly = true;
  document.body.append(area);
  area.focus();
  area.select();
  area.setSelectionRange(0, text.length);
  let ok = false;
  try { ok = document.execCommand("copy"); } catch (_) { ok = false; }
  area.remove();
  copyButton.focus();
  if (ok) {
    toast(copied);
    return;
  }
  window.getSelection().selectAllChildren(list);
  toast("Copying is blocked here; the logs are selected, so copy them manually.", true);
});

poll();
