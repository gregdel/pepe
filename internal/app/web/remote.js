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
  // Search results are focusable: arrows must scroll them, not move the TV.
  if (searchOpen) {
    if (event.key === "Escape" && event.target !== searchInput) closeSearch();
    return;
  }
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
    playerRunning = running;
    catalogChanged(status.catalog_version);
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

// ── Search ───────────────────────────────────────────────────────────────────
// The phone matches titles itself, so typing never waits for the network. The
// catalog is refetched when the status poll reports a new library version.

const searchForm = document.getElementById("search-form");
const searchInput = document.getElementById("search");
const searchClear = document.getElementById("search-clear");
const searchClose = document.getElementById("search-close");
const searchView = document.getElementById("search-view");
const resultsHeading = document.getElementById("results-heading");
const resultsList = document.getElementById("results");
const resultsMore = document.getElementById("results-more");
const resultsLive = document.getElementById("results-live");
const searchMessage = document.getElementById("search-message");
const searchMessageText = searchMessage.querySelector("p");
const searchMessageAction = searchMessage.querySelector("button");
const resultTemplate = document.getElementById("result-template");
const kindInputs = document.querySelectorAll('input[name="kind"]');
const kindCounts = document.querySelectorAll("[data-count]");
const resultLimit = 60;
const kindNames = {movie: "Movie", show: "Show"};
const kindPlurals = {"": "titles", movie: "movies", show: "shows"};
// Leading articles a title may start with that a search may leave out.
const articles = new Set(["the", "a", "an", "le", "la", "les", "l", "un", "une"]);

let searchOpen = false;
let playerRunning = false;
let catalog = null; // {version, entries}
let catalogState = "idle"; // idle | loading | error
let rows = new Map(); // result rows by kind:id, kept so loaded posters stay
let liveTimer = 0;
let mainScroll = 0, searchScroll = 0;

function storedKind() {
  try { return localStorage.getItem("pepe.searchKind") || ""; } catch (_) { return ""; }
}
let kind = storedKind();
kindInputs.forEach(input => { input.checked = input.value === kind; });

// Accents, case and punctuation never matter: "amelie" finds "Amélie".
function fold(text) {
  return text.normalize("NFD").replace(/\p{M}/gu, "").toLowerCase().replace(/[^\p{L}\p{N}]+/gu, " ").trim();
}

// Optimal string alignment distance, capped: 2 means "more than one edit".
function oneEdit(a, b) {
  if (Math.abs(a.length - b.length) > 1) return false;
  let previous2 = null, previous = Array.from({length: b.length + 1}, (_, j) => j);
  for (let i = 1; i <= a.length; i++) {
    const current = [i];
    for (let j = 1; j <= b.length; j++) {
      current[j] = Math.min(previous[j] + 1, current[j - 1] + 1, previous[j - 1] + (a[i - 1] === b[j - 1] ? 0 : 1));
      if (previous2 && i > 1 && j > 1 && a[i - 1] === b[j - 2] && a[i - 2] === b[j - 1]) {
        current[j] = Math.min(current[j], previous2[j - 2] + 1);
      }
    }
    previous2 = previous;
    previous = current;
  }
  return previous[b.length] <= 1;
}

// In-order characters, as fzf matches: rewards runs and word starts, and
// rejects matches spread too thin to be meant.
function subsequence(token, text) {
  let score = 0, run = 0, at = -1, first = -1;
  for (const char of token) {
    const next = text.indexOf(char, at + 1);
    if (next < 0) return 0;
    run = next === at + 1 ? run + 1 : 0;
    score += 1 + run * 2 + (next === 0 || text[next - 1] === " " ? 3 : 0);
    if (first < 0) first = next;
    at = next;
  }
  return at - first + 1 > token.length * 3 ? 0 : Math.min(45, 10 + score);
}

// Best score of one query token in a title; 0 means no match.
function tokenScore(token, entry) {
  let best = 0;
  entry.words.forEach((word, i) => {
    const lead = i === 0 || (i === 1 && articles.has(entry.words[0]));
    if (word.startsWith(token)) {
      best = Math.max(best, (lead ? 100 : 90) + (word.length === token.length ? 10 : 0));
    } else if (token.length >= 4 && (oneEdit(token, word.slice(0, token.length)) || oneEdit(token, word))) {
      best = Math.max(best, lead ? 40 : 35); // a typo
    }
  });
  if (best >= 90) return best;
  if (/^\d{4}$/.test(token) && Number(token) === entry.item.year) return 80;
  if (entry.text.includes(token)) return Math.max(best, 60);
  return Math.max(best, token.length >= 3 ? subsequence(token, entry.text) : 0);
}

function search(query) {
  const tokens = fold(query).split(" ").filter(Boolean);
  if (!tokens.length) {
    return catalog.entries.slice().sort((a, b) => (b.item.added || 0) - (a.item.added || 0) || a.text.localeCompare(b.text));
  }
  const matches = [];
  for (const entry of catalog.entries) {
    let score = 0;
    for (const token of tokens) {
      const s = tokenScore(token, entry);
      if (!s) { score = 0; break; }
      score += s;
    }
    if (score) matches.push({entry, score});
  }
  matches.sort((a, b) => b.score - a.score || a.entry.text.length - b.entry.text.length || a.entry.text.localeCompare(b.entry.text));
  return matches.map(match => match.entry);
}

async function loadCatalog() {
  if (catalogState === "loading") return;
  catalogState = "loading";
  renderSearch();
  try {
    const response = await fetch("/api/catalog", {cache: "no-store", credentials: "omit", signal: AbortSignal.timeout(10000)});
    if (!response.ok) throw new Error("Unavailable");
    const data = await response.json();
    const entries = data.titles.map(item => {
      const text = fold(item.title);
      return {item, text, words: text.split(" ")};
    });
    catalog = {version: data.version, entries};
    rows = new Map();
    catalogState = "idle";
  } catch (_) {
    catalogState = "error";
  }
  renderSearch();
}

// Polled: a refresh that lands during a load is caught by the next poll. After
// a failed reload the stale catalog stays, with a Retry, until search is
// reopened, rather than retrying on every poll.
function catalogChanged(version) {
  if (version === undefined || !catalog || catalog.version === version) return;
  if (!searchOpen) catalog = null;
  else if (catalogState !== "error") loadCatalog();
}

function posterURL(item) {
  return "/api/artwork/" + item.kind + "/" + encodeURIComponent(item.id) + "?v=" + catalog.version;
}

function resultRow(item) {
  const key = item.kind + ":" + item.id;
  let row = rows.get(key);
  if (row) return row;
  row = resultTemplate.content.firstElementChild.cloneNode(true);
  row.dataset.kind = item.kind;
  row.querySelector(".result-title").textContent = item.title;
  row.querySelector(".result-kind").textContent = kindNames[item.kind];
  row.querySelector(".result-year").textContent = item.year ? String(item.year) : "";
  const image = new Image(240, 360);
  image.alt = "";
  image.decoding = "async";
  image.loading = "lazy";
  image.addEventListener("load", () => image.classList.add("loaded"), {once: true});
  image.addEventListener("error", () => image.remove(), {once: true});
  image.src = posterURL(item);
  row.querySelector(".poster").append(image);
  row.querySelector(".result-open").addEventListener("click", () => openTitle(item, false));
  const play = row.querySelector(".result-play");
  if (item.kind === "movie") {
    play.setAttribute("aria-label", "Play " + item.title + " on TV");
    play.title = "Play on TV";
    play.addEventListener("click", () => openTitle(item, true));
  } else {
    play.remove();
  }
  rows.set(key, row);
  return row;
}

function showMessage(text, action = "", onAction = null) {
  searchMessage.hidden = false;
  searchMessageText.textContent = text;
  searchMessageAction.textContent = action;
  searchMessageAction.hidden = !action;
  searchMessageAction.onclick = onAction;
}

function skeletons() {
  return Array.from({length: 3}, () => {
    const row = document.createElement("li");
    row.className = "result skeleton";
    row.setAttribute("aria-hidden", "true");
    row.innerHTML = '<span class="poster"></span><span class="result-text"><span class="bar"></span><span class="bar short"></span></span>';
    return row;
  });
}

function renderSearch() {
  const query = searchInput.value;
  searchClear.hidden = query === "";
  if (!searchOpen) return;
  searchMessage.hidden = true;
  resultsMore.hidden = true;
  if (!catalog) {
    kindCounts.forEach(count => { count.textContent = ""; });
    resultsHeading.hidden = true;
    if (catalogState === "error") {
      resultsList.replaceChildren();
      showMessage("Can't load the library. Check that pépé is running on the TV.", "Retry", loadCatalog);
    } else {
      resultsList.replaceChildren(...skeletons());
    }
    return;
  }
  const matches = search(query);
  const counts = {"": matches.length, movie: 0, show: 0};
  matches.forEach(entry => { counts[entry.item.kind]++; });
  kindCounts.forEach(count => { count.textContent = catalog.entries.length ? String(counts[count.dataset.count]) : ""; });
  const shown = kind ? matches.filter(entry => entry.item.kind === kind) : matches;
  resultsHeading.textContent = query.trim() ? "Best matches" : "Recently added";
  resultsHeading.hidden = shown.length === 0;
  resultsList.replaceChildren(...shown.slice(0, resultLimit).map(entry => resultRow(entry.item)));
  if (shown.length > resultLimit) {
    resultsMore.textContent = "Showing " + resultLimit + " of " + shown.length + ". Keep typing to narrow down.";
    resultsMore.hidden = false;
  }
  if (catalogState === "error") {
    showMessage("Can't refresh the library; these results may be out of date.", "Retry", loadCatalog);
  } else if (!catalog.entries.length) {
    showMessage("No titles yet. The library is empty or still loading on the TV.");
  } else if (!shown.length && kind && matches.length) {
    showMessage("No " + kindPlurals[kind] + " match “" + query.trim() + "”.", "Search all", () => setKind(""));
  } else if (!shown.length) {
    showMessage("Nothing matches “" + query.trim() + "”.");
  }
  // Announce the count once typing pauses, not on every keystroke.
  window.clearTimeout(liveTimer);
  liveTimer = window.setTimeout(() => {
    resultsLive.textContent = shown.length === 1 ? "1 result" : shown.length + " results";
  }, 600);
}

function setKind(value) {
  kind = value;
  kindInputs.forEach(input => { input.checked = input.value === kind; });
  try { localStorage.setItem("pepe.searchKind", kind); } catch (_) { /* per-viewer convenience only */ }
  window.scrollTo(0, 0);
  renderSearch();
}

function openSearch() {
  if (searchOpen) return;
  searchOpen = true;
  mainScroll = window.scrollY;
  app.dataset.view = "search";
  searchView.hidden = false;
  searchClose.hidden = false;
  history.pushState({search: true}, "", "#search");
  if (!catalog) loadCatalog();
  renderSearch();
  window.scrollTo(0, searchScroll);
}

// Hide the view; history is already where it should be.
function leaveSearch() {
  if (!searchOpen) return;
  searchOpen = false;
  searchScroll = window.scrollY;
  delete app.dataset.view;
  searchView.hidden = true;
  searchClose.hidden = true;
  searchInput.blur();
  window.scrollTo(0, mainScroll);
}

// Close through history, so the phone's Back and the close button agree.
function closeSearch() {
  if (history.state && history.state.search) history.back();
  else leaveSearch();
}

function openTitle(item, play) {
  press();
  send("/api/open", {kind: item.kind, id: item.id, play}, () => {
    toast(play ? "Playing on TV" : playerRunning ? "Opens on TV when playback stops" : "Opened on TV");
  });
  closeSearch();
}

window.addEventListener("popstate", () => {
  if (searchOpen && !(history.state && history.state.search)) leaveSearch();
});
// A reload on #search starts on the remote; there is no history entry to pop.
if (location.hash === "#search") history.replaceState(null, "", location.pathname);

// A tap opens the view even when the field already has focus.
searchInput.addEventListener("focus", openSearch);
searchInput.addEventListener("click", openSearch);
searchInput.addEventListener("input", () => {
  openSearch();
  window.scrollTo(0, 0);
  renderSearch();
});
searchInput.addEventListener("keydown", event => {
  if (event.key !== "Escape") return;
  event.preventDefault();
  if (searchInput.value) {
    searchInput.value = "";
    renderSearch();
  } else {
    closeSearch();
  }
});
// Enter only hides the keyboard, revealing the results.
searchForm.addEventListener("submit", event => {
  event.preventDefault();
  searchInput.blur();
});
searchClear.addEventListener("click", () => {
  searchInput.value = "";
  searchInput.focus();
  renderSearch();
});
searchClose.addEventListener("click", closeSearch);
kindInputs.forEach(input => input.addEventListener("change", () => setKind(input.value)));
poll();
