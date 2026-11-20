"use strict";

const app = document.getElementById("app");
const nowPlaying = document.getElementById("now-playing");
const statusText = document.getElementById("status");
const source = document.getElementById("source");
const feedback = document.getElementById("feedback");
const urlInput = document.getElementById("url");
const playLabel = document.getElementById("play-label");
const siteChips = document.querySelectorAll("[data-site]");
const commandButtons = document.querySelectorAll("[data-command]");
let requests = Promise.resolve();
let pending = 0;
let toastTimer = 0;
let shownCommandError = "";

const sites = [
  {name: "YouTube", hosts: ["youtube.com", "youtu.be", "youtube-nocookie.com"]},
  {name: "arte.tv", hosts: ["arte.tv"]},
  {name: "france.tv", hosts: ["france.tv", "francetvinfo.fr"]},
];

function toast(text, error = false) {
  window.clearTimeout(toastTimer);
  feedback.textContent = text;
  feedback.classList.toggle("error", error);
  feedback.hidden = false;
  toastTimer = window.setTimeout(() => { feedback.hidden = true; }, error ? 6000 : 2500);
}

// Serialize mutations so rapid taps retain their order. A 202 means queued,
// not that the Gio event loop or mpv has executed the request yet.
function send(path, body, onQueued) {
  if (pending >= 32) {
    toast("Too many pending commands; wait a moment.", true);
    return;
  }
  pending++;
  requests = requests.then(async () => {
    const response = await fetch(path, {
      method: "POST", headers: {"Content-Type": "application/json"},
      body: JSON.stringify(body), credentials: "omit", signal: AbortSignal.timeout(6000)
    });
    const result = await response.json();
    if (!response.ok) throw new Error(result.error || "Command failed.");
    if (onQueued) onQueued();
  }).catch(error => {
    toast(error.message || "Remote unavailable.", true);
  }).finally(() => { pending--; });
}

// Share sheets often add a title around the link; keep the first URL and
// accept a bare "youtu.be/…" by assuming HTTPS.
function linkFrom(text) {
  text = text.trim();
  const url = text.match(/https?:\/\/[^\s<>"']+/i);
  if (url) return url[0];
  if (/^[\w-]+(\.[\w-]+)+([/?#]\S*)?$/.test(text)) return "https://" + text;
  return text;
}

function siteFor(link) {
  let host;
  try { host = new URL(link).hostname.toLowerCase(); } catch (_) { return null; }
  return sites.find(site => site.hosts.some(h => host === h || host.endsWith("." + h))) || null;
}

function updateSite() {
  const site = urlInput.value.trim() ? siteFor(linkFrom(urlInput.value)) : null;
  siteChips.forEach(chip => chip.classList.toggle("match", site !== null && chip.dataset.site === site.name));
  playLabel.textContent = site ? "Play on TV · " + site.name : "Play on TV";
}

function press() {
  if (navigator.vibrate) navigator.vibrate(8);
}

document.querySelectorAll("[data-action]").forEach(button => {
  button.addEventListener("click", () => { press(); send("/api/action", {action: button.dataset.action}); });
});
commandButtons.forEach(button => {
  button.addEventListener("click", () => { press(); send("/api/player", {command: button.dataset.command}); });
});
urlInput.addEventListener("input", updateSite);
document.getElementById("url-form").addEventListener("submit", event => {
  event.preventDefault();
  const url = linkFrom(urlInput.value);
  if (!url) {
    toast("Paste a link first.", true);
    urlInput.focus();
    return;
  }
  send("/api/play", {url}, () => {
    urlInput.value = "";
    urlInput.blur();
    updateSite();
    toast("Sent to TV");
  });
});

document.addEventListener("keydown", event => {
  // Preserve normal editor, button activation, and Tab behavior.
  if (event.target.matches("input, textarea, select, [contenteditable]") || event.ctrlKey || event.altKey || event.metaKey) return;
  const actions = {ArrowUp: "up", ArrowDown: "down", ArrowLeft: "left", ArrowRight: "right", Escape: "back", Home: "home"};
  if (actions[event.key]) {
    event.preventDefault();
    send("/api/action", {action: actions[event.key]});
  }
});

function setText(element, text) {
  if (element.textContent !== text) element.textContent = text;
  element.hidden = text === "";
}

// The now-playing card moves first while playing, but never while the link
// field is in use: reordering would move it under the user's finger.
function render(state, title, status, badge, controls) {
  app.dataset.state = state;
  const layout = state === "running" || state === "offline" ? "playing" : "idle";
  if (app.dataset.layout !== layout && document.activeElement !== urlInput && !urlInput.value) {
    app.dataset.layout = layout;
  }
  setText(nowPlaying, title);
  setText(statusText, status);
  setText(source, badge);
  commandButtons.forEach(button => { button.disabled = !controls(button.dataset.command); });
}

async function poll() {
  try {
    const response = await fetch("/api/status", {cache: "no-store", credentials: "omit", signal: AbortSignal.timeout(6000)});
    if (!response.ok) throw new Error("Disconnected");
    const status = await response.json();
    const running = status.state === "Running";
    const title = running ? (status.title || "Untitled video") : "Nothing playing";
    let text = running ? "" : (status.error || "");
    if (status.stopping) text = "Stopping…";
    if (status.queued_commands) text += (text ? " · " : "") + status.queued_commands + " queued";
    const state = running ? "running" : status.state === "Error" ? "error" : "idle";
    const badge = running ? (status.source === "web" ? "Link" : "Library") : "";
    render(state, title, text, badge, command => running && (!status.stopping || command === "stop"));
    document.title = running ? title + " · pépé" : "pépé";
    if (status.command_error && status.command_error !== shownCommandError) toast(status.command_error, true);
    shownCommandError = status.command_error || "";
  } catch (_) {
    render("offline", "Can't reach pépé", "Check that pépé is running on the TV.", "", () => false);
    document.title = "pépé";
  } finally {
    window.setTimeout(poll, 1000);
  }
}
poll();
