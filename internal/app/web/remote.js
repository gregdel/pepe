"use strict";

const app = document.getElementById("app");
const nowPlaying = document.getElementById("now-playing");
const statusText = document.getElementById("status");
const feedback = document.getElementById("feedback");
const urlInput = document.getElementById("url");
const playLabel = document.getElementById("play-label");
const siteChips = document.querySelectorAll("[data-site]");
const commandButtons = document.querySelectorAll("[data-command]");
const watchedButton = document.querySelector('[data-action="watched"]');
const progress = document.getElementById("progress");
const seek = document.getElementById("seek");
const elapsed = document.getElementById("elapsed");
const total = document.getElementById("total");
let requests = Promise.resolve();
let pending = 0;
let toastTimer = 0;
let shownCommandError = "";
// The last polled position, advanced locally between polls while playing.
let clock = {position: 0, duration: 0, paused: true, at: 0, entry: 0};
let dragging = false;
let seekHold = null; // {target, until}: ignore polls until mpv reaches a seek
let pollTimer = 0, polling = false;

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
  playLabel.textContent = site ? "Play · " + site.name : "Play";
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
  const actions = {ArrowUp: "up", ArrowDown: "down", ArrowLeft: "left", ArrowRight: "right", Escape: "back"};
  if (actions[event.key]) {
    event.preventDefault();
    send("/api/action", {action: actions[event.key]});
  }
});

function setText(element, text) {
  if (element.textContent !== text) element.textContent = text;
  element.hidden = text === "";
}

function timestamp(seconds) {
  seconds = Math.max(0, Math.floor(seconds));
  const h = Math.floor(seconds / 3600), m = Math.floor(seconds / 60) % 60, s = String(seconds % 60).padStart(2, "0");
  return h ? h + ":" + String(m).padStart(2, "0") + ":" + s : m + ":" + s;
}

function clockPosition() {
  const advanced = clock.paused ? 0 : (performance.now() - clock.at) / 1000;
  return Math.min(clock.position + advanced, clock.duration);
}

function drawProgress(position) {
  seek.value = position;
  seek.style.setProperty("--progress", (clock.duration > 0 ? position / clock.duration * 100 : 0) + "%");
  setText(elapsed, timestamp(position));
  seek.setAttribute("aria-valuetext", timestamp(position) + " of " + timestamp(clock.duration));
}

// Seek once on release, not while dragging: mpv's control queue is short.
seek.addEventListener("pointerdown", () => { dragging = true; });
seek.addEventListener("input", () => {
  dragging = true;
  drawProgress(Number(seek.value));
});
seek.addEventListener("change", () => {
  dragging = false;
  const target = Number(seek.value);
  clock.position = target;
  clock.at = performance.now();
  seekHold = {target, until: clock.at + 2000};
  press();
  send("/api/player", {command: "seek", position: target, playlist_pos: clock.entry});
});
// A release without a change (a tap on the thumb) fires no change event.
for (const type of ["pointerup", "pointercancel"]) {
  seek.addEventListener(type, () => { window.setTimeout(() => { dragging = false; }, 0); });
}
window.setInterval(() => {
  if (!progress.hidden && !dragging && !document.hidden) drawProgress(clockPosition());
}, 250);

// The now-playing card grows while playing, but never while the link field is
// in use: the resize would move the field under the user's finger.
function render(state, title, status, controls, pos = 0, count = 0) {
  app.dataset.state = state;
  const layout = state === "running" || state === "offline" ? "playing" : "idle";
  if (app.dataset.layout !== layout && document.activeElement !== urlInput && !urlInput.value) {
    app.dataset.layout = layout;
  }
  setText(nowPlaying, title);
  setText(statusText, status);
  progress.hidden = state !== "running" || clock.duration <= 0;
  seek.max = clock.duration;
  seek.disabled = !controls("seek");
  setText(total, timestamp(clock.duration));
  if (!progress.hidden && !dragging) drawProgress(clockPosition());
  // Previous/Next exist for any multi-entry mpv playlist: a show's queued
  // episodes or a web playlist.
  commandButtons.forEach(button => {
    const command = button.dataset.command;
    if (command.startsWith("playlist-")) button.hidden = state !== "running" || count < 2;
    button.disabled = !controls(command);
  });
  watchedButton.disabled = state === "running";
}

function updateClock(status) {
  const position = Number(status.position) || 0, now = performance.now();
  const entry = status.playlist_pos || 0;
  if (seekHold && entry === clock.entry && now < seekHold.until && Math.abs(position - seekHold.target) > 2) {
    clock.paused = !!status.paused; // keep the seek target until mpv reaches it
  } else {
    seekHold = null;
    clock = {position, paused: !!status.paused, at: now, entry};
  }
  clock.duration = Number(status.duration) || 0;
}

// Poll only while the page is visible, and at once when it comes back.
async function poll() {
  polling = true;
  pollTimer = 0;
  try {
    const response = await fetch("/api/status", {cache: "no-store", credentials: "omit", signal: AbortSignal.timeout(6000)});
    if (!response.ok) throw new Error("Disconnected");
    const status = await response.json();
    const running = status.state === "Running";
    const title = running ? (status.title || "Untitled video") : "Nothing playing";
    const pos = status.playlist_pos || 0, count = status.playlist_count || 0;
    const parts = [];
    if (running && pos > 0 && count > 1) parts.push((status.source === "web" ? "Video " : "Episode ") + pos + " of " + count);
    if (!running && status.error) parts.push(status.error);
    if (status.stopping) parts.push("Stopping…");
    if (status.queued_commands) parts.push(status.queued_commands + " queued");
    const state = running ? "running" : status.state === "Error" ? "error" : "idle";
    if (running) updateClock(status);
    else clock.duration = 0;
    render(state, title, parts.join(" · "), command => {
      if (!running || (status.stopping && command !== "stop")) return false;
      if (command === "playlist-prev") return pos > 1;
      if (command === "playlist-next") return pos > 0 && pos < count;
      return true;
    }, pos, count);
    document.title = running ? title + " · pépé" : "pépé";
    if (status.command_error && status.command_error !== shownCommandError) toast(status.command_error, true);
    shownCommandError = status.command_error || "";
  } catch (_) {
    clock.duration = 0;
    render("offline", "Can't reach pépé", "Check that pépé is running on the TV.", () => false);
    document.title = "pépé";
  } finally {
    polling = false;
    if (!document.hidden) pollTimer = window.setTimeout(poll, 1000);
  }
}
document.addEventListener("visibilitychange", () => {
  if (document.hidden) {
    window.clearTimeout(pollTimer);
    pollTimer = 0;
  } else if (!polling && !pollTimer) {
    poll();
  }
});
poll();
