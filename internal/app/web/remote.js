"use strict";

const statusText = document.getElementById("status");
const nowPlaying = document.getElementById("now-playing");
const feedback = document.getElementById("feedback");
const urlInput = document.getElementById("url");
let requests = Promise.resolve();
let pending = 0;

// Serialize mutations so rapid taps retain their order. A 202 means queued,
// not that the Gio event loop or mpv has executed the request yet.
function send(path, body) {
  if (pending >= 32) {
    feedback.textContent = "Too many pending commands; wait a moment.";
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
    feedback.textContent = "Command queued.";
  }).catch(error => {
    feedback.textContent = error.message || "Remote unavailable.";
  }).finally(() => { pending--; });
}

function navigate(action) {
  send("/api/action", {action});
}

document.querySelectorAll("[data-action]").forEach(button => {
  button.addEventListener("click", () => navigate(button.dataset.action));
});
document.querySelectorAll("[data-command]").forEach(button => {
  button.addEventListener("click", () => send("/api/player", {command: button.dataset.command}));
});
document.getElementById("url-form").addEventListener("submit", event => {
  event.preventDefault();
  send("/api/play", {url: urlInput.value.trim()});
});

document.addEventListener("keydown", event => {
  // Preserve normal editor, button activation, and Tab behavior.
  if (event.target.matches("input, textarea, select, [contenteditable]") || event.ctrlKey || event.altKey || event.metaKey) return;
  const actions = {ArrowUp: "up", ArrowDown: "down", ArrowLeft: "left", ArrowRight: "right", Escape: "back", Home: "home"};
  if (actions[event.key]) {
    event.preventDefault();
    navigate(actions[event.key]);
  }
});

async function poll() {
  try {
    const response = await fetch("/api/status", {cache: "no-store", credentials: "omit", signal: AbortSignal.timeout(6000)});
    if (!response.ok) throw new Error("Disconnected");
    const status = await response.json();
    const running = status.state === "Running";
    const title = running ? (status.title || "Untitled video") : "Nothing playing";
    if (nowPlaying.textContent !== title) nowPlaying.textContent = title;
    document.title = running ? title + " · Pépé remote" : "Pépé remote";
    let text = status.state === "Idle" ? "Ready to play" : status.state;
    if (status.stopping) text = "Stopping playback";
    else if (running) text = status.source === "web" ? "Playing from Web UI" : "Now playing";
    if (status.queued_commands) text += " — " + status.queued_commands + " queued";
    if (statusText.textContent !== text) statusText.textContent = text;
    if (status.error || status.command_error) feedback.textContent = status.command_error || status.error;
    else if (!pending && !status.queued_commands && feedback.textContent === "Command queued.") feedback.textContent = "";
    document.querySelectorAll("[data-command]").forEach(button => {
      button.disabled = status.state !== "Running" || (status.stopping && button.dataset.command !== "stop");
    });
  } catch (_) {
    nowPlaying.textContent = "Pépé remote";
    document.title = "Pépé remote";
    statusText.textContent = "Disconnected — check that Pépé is running.";
    document.querySelectorAll("[data-command]").forEach(button => { button.disabled = true; });
  } finally {
    window.setTimeout(poll, 1000);
  }
}
poll();
