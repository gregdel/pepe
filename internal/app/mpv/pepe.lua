-- pépé-owned playback UI. Keyboard mode yields to user input.conf bindings.
local mp = require "mp"
local input = require "mp.input"
-- Colours are ASS BGR hex. pépé passes its theme (theme.go); these defaults
-- mirror it only so the script stands alone.
local opts = {
    controls = "keyboard", help = true, panel = true, web = false, volume_keys = false,
    color_fg = "ECE8E8", color_subtle = "ABA0A0", color_accent = "FF7F9C",
    color_panel = "141010", color_track = "6A6060",
    color_green = "9BD781", color_yellow = "6BD8FF", color_blue = "FFB468",
}
require("mp.options").read_options(opts, "pepe")
local overlay = mp.create_osd_overlay("ass-events")
local loaded, started_at = false, nil
local generation = 0
local timer, render
-- menu_open tracks mpv's console (pepe's selector, select.lua menus, the
-- console itself); own_menu only marks pepe's pending selector request.
local help_requested, menu_open, own_menu, startup_until = false, false, false, 0

local function finite(value)
    return type(value) == "number" and value == value
        and value > -math.huge and value < math.huge
end

local function timestamp(seconds)
    if not finite(seconds) or seconds < 0 then return "Unknown" end
    seconds = math.floor(seconds)
    return string.format("%d:%02d:%02d", math.floor(seconds / 3600),
        math.floor(seconds / 60) % 60, seconds % 60)
end

local function clock(epoch, now)
    local day = os.date("%Y-%m-%d", epoch)
    return os.date("%Hh%M", epoch), day ~= os.date("%Y-%m-%d", now) and day or nil
end

-- Separate ASS events keep labels anchored independently of text length.
local function text(events, x, y, alignment, size, color, value, extra)
    events[#events + 1] = string.format(
        "{\\an%d\\pos(%d,%d)\\q2\\fnRoboto\\bord0\\shad0\\fs%d\\b0\\1c&H%s&\\1a&H00&%s}%s",
        alignment, x, y, size, color, extra or "", value)
end

local function rectangle(events, x1, y1, x2, y2, color, alpha)
    if x2 <= x1 then return end
    events[#events + 1] = string.format(
        "{\\an7\\pos(0,0)\\bord0\\shad0\\1c&H%s&\\1a&H%s&\\p1}"
        .. "m %d %d l %d %d %d %d %d %d{\\p0}",
        color, alpha or "00", x1, y1, x2, y1, x2, y2, x1, y2)
end

local function escape(text)
    -- Metadata is text, never ASS markup or additional overlay events.
    text = text:gsub("%s+", " ")
    return mp.command_native({"escape-ass", text})
end

local function clear()
    timer:kill()
    if overlay.data ~= "" then
        overlay:remove()
        overlay.data = ""
    end
end

-- Arrows seek 5s, the web remote 10s; both seek 1 min with Up/Down.
local seek_hint = "Left/Right Seek  ·  Up/Down Seek 1 min"

-- Position in a multi-entry playlist (queued episodes or a web playlist), e.g. "3 / 8".
local function playlist_position()
    local count = mp.get_property_number("playlist-count", 0)
    local pos = mp.get_property_number("playlist-pos-1", 0)
    if not finite(count) or not finite(pos) or count < 2 or pos < 1 then return nil end
    return string.format("%d / %d", pos, count)
end

-- A queued show's position leads the title, e.g. "3 / 8 · Title".
local function heading(title)
    local value = escape(title)
    local position_label = playlist_position()
    if position_label then
        value = string.format("{\\b0\\1c&H%s&}%s  ·  {\\b1\\1c&H%s&}",
            opts.color_subtle, position_label, opts.color_fg) .. value
    end
    return value
end

local function hints(events, left, center, right, top)
    if opts.controls == "hdmi-cec" then
        local line = "OK Pause/resume  ·  Back Return"
        -- Roboto has no media symbols; « » stand for Rewind/Fast forward.
        if playlist_position() then line = line .. "  ·  « » Previous/next" end
        text(events, center, top, 8, 22, opts.color_fg, line)
        -- One row in the same order as the remote, with bars above the labels
        -- so all actions also fit in narrow windows. Red (w) is inert here.
        local buttons = {
            {opts.color_green, "Help"}, {opts.color_yellow, "Subtitle tracks"},
            {opts.color_blue, "Cycle subtitles"},
        }
        local column = (right - left) / #buttons
        for i, button in ipairs(buttons) do
            local x = math.floor(left + (i - 0.5) * column)
            rectangle(events, x - 20, top + 32, x + 20, top + 40, button[1])
            text(events, x, top + 46, 8, 20, opts.color_fg, button[2])
        end
    else
        text(events, center, top, 8, 22, opts.color_fg, "Space Pause/resume  ·  q Return")
        local line = "j Cycle subtitles  ·  Tab Subtitle tracks"
        if playlist_position() then line = line .. "  ·  < > Previous/next" end
        text(events, center, top + 32, 8, 22, opts.color_subtle, line)
    end
end

render = function()
    local paused = mp.get_property_bool("pause", false)
    local transient = opts.help and mp.get_time() < startup_until
    if not loaded or (not opts.panel and not help_requested) or menu_open
        or not (paused or help_requested or transient) then
        clear()
        return
    end
    if not timer:is_enabled() then timer:resume() end

    local now = os.time()
    local position = mp.get_property_number("time-pos")
    local duration = mp.get_property_number("duration")
    local speed = mp.get_property_number("speed", 1)
    local remaining
    if finite(position) and position >= 0 and finite(duration) and duration > 0
        and finite(speed) and speed > 0 then
        remaining = math.max(0, duration - position) / speed
        if not finite(remaining) then remaining = nil end
    end
    -- media-title/filename may contain a signed URL while web extraction is
    -- pending. Only the explicitly resolved title is safe for that source.
    local title = mp.get_property(opts.web and "force-media-title" or "media-title", "")
    if title == "" then title = opts.web and "Web UI video" or "Video" end
    local dimensions = mp.get_property_native("osd-dimensions", {})
    local aspect = dimensions.aspect
    if not finite(aspect) or aspect <= 0 then aspect = 16 / 9 end
    -- Keep three columns legible on narrow windows too; use square ASS pixels.
    local height = math.floor(math.max(720, 720 / aspect) + 0.5)
    local width = math.floor(height * aspect + 0.5)
    local panel_width = math.min(1120, width - 64)
    local left = math.floor((width - panel_width) / 2) + 28
    local right, center = width - left, math.floor(width / 2)
    -- Keep the panel stable above mpv's native bottom controls. Mouse movement
    -- must neither hide pause metadata nor intercept OSC clicks and dragging.
    local bottom = height - 120
    local top = bottom - 282
    local fg, muted, accent = opts.color_fg, opts.color_subtle, opts.color_accent
    local panel = opts.color_panel
    local events = {}
    if not paused or not opts.panel then
        -- In a playlist, name the entry that just started or was skipped to.
        local panel_top = playlist_position() and top - 56 or top
        rectangle(events, left - 28, panel_top, right + 28, top + 160, panel, "00")
        if panel_top < top then
            text(events, center, top - 36, 8, 26, fg, heading(title),
                string.format("\\b1\\clip(%d,%d,%d,%d)", left, panel_top, right, top))
        end
        hints(events, left, center, right, top + 20)
        text(events, center, top + 100, 8, 22, muted, seek_hint)
        local data = table.concat(events, "\n")
        if data ~= overlay.data or width ~= overlay.res_x or height ~= overlay.res_y then
            overlay.res_x, overlay.res_y, overlay.data = width, height, data
            overlay:update()
        end
        return
    end
    if opts.help or help_requested then
        local hint_top = help_requested and top - 134 or top - 90
        rectangle(events, left - 28, hint_top, right + 28, top - 8, panel, "00")
        hints(events, left, center, right, hint_top + 12)
        if help_requested then
            text(events, center, top - 52, 8, 22, muted, seek_hint)
        end
    end
    rectangle(events, left - 28, top, right + 28, bottom, panel, "00")
    -- A long title is single-line and clipped before the pause badge. The
    -- playlist position shares its event, so the two never overlap.
    text(events, left, top + 22, 7, 30, fg, heading(title),
        string.format("\\b1\\clip(%d,%d,%d,%d)", left, top, right - 160, top + 64))
    rectangle(events, right - 128, top + 29, right - 123, top + 49, accent)
    rectangle(events, right - 118, top + 29, right - 113, top + 49, accent)
    text(events, right, top + 25, 9, 20, accent, "PAUSED")

    text(events, left, top + 78, 7, 16, muted, "ELAPSED")
    local remaining_label = "REMAINING"
    if finite(speed) and speed > 0 and speed ~= 1 then
        remaining_label = remaining_label .. string.format(" · %g×", speed)
    end
    text(events, center, top + 78, 8, 16, muted, remaining_label)
    text(events, right, top + 78, 9, 16, muted, "TOTAL")
    text(events, left, top + 100, 7, 28, fg, timestamp(position))
    text(events, center, top + 100, 8, 28, accent,
        remaining and string.format("%d min left", math.ceil(remaining / 60)) or "Unknown")
    text(events, right, top + 100, 9, 28, fg, timestamp(duration))

    local bar_y = top + 154
    rectangle(events, left, bar_y, right, bar_y + 10, opts.color_track)
    if finite(position) and position >= 0 and finite(duration) and duration > 0 then
        local progress = math.min(1, position / duration)
        local cursor = left + math.floor((right - left) * progress + 0.5)
        rectangle(events, left, bar_y, cursor, bar_y + 10, accent)
        rectangle(events, cursor - 2, bar_y - 5, cursor + 2, bar_y + 15, fg)
    else
        text(events, center, bar_y + 16, 8, 14, muted, "PROGRESS UNKNOWN")
    end

    local function clock_cell(x, alignment, label, epoch, fallback, color)
        text(events, x, top + 190, alignment, 16, muted, label)
        local value, date
        if epoch then value, date = clock(epoch, now) else value = fallback end
        text(events, x, top + 212, alignment, epoch and 28 or 22, color, value)
        if date then text(events, x, top + 246, alignment, 16, muted, date) end
    end
    clock_cell(left, 7, "STARTED", started_at, "Not started yet", fg)
    clock_cell(center, 8, "NOW", now, nil, fg)
    clock_cell(right, 9, "FINISH", remaining and now + math.ceil(remaining), "Unknown", accent)
    text(events, right, top + 266, 9, 14, muted, "if resumed now")

    local data = table.concat(events, "\n")
    if data ~= overlay.data or width ~= overlay.res_x or height ~= overlay.res_y then
        overlay.res_x, overlay.res_y, overlay.data = width, height, data
        overlay:update()
    end
end

timer = mp.add_periodic_timer(1, render, true)
overlay.data = ""

local function reset()
    generation = generation + 1
    loaded, started_at = false, nil
    help_requested, startup_until = false, 0
    if own_menu then input.terminate() end
    own_menu = false
    clear()
end

mp.register_event("start-file", reset)
mp.register_event("end-file", reset)
mp.register_event("file-loaded", function()
    loaded = true
    startup_until = mp.get_time() + 4
    if not mp.get_property_bool("pause", false) then started_at = os.time() end
    render()
end)
mp.observe_property("pause", "bool", function(_, paused)
    if loaded and not paused and not started_at then started_at = os.time() end
    if not paused then help_requested, startup_until = false, 0 end
    render()
end)
mp.observe_property("osd-dimensions", "native", render)
for _, property in ipairs({"time-pos", "duration", "speed", "media-title", "force-media-title"}) do
    mp.observe_property(property,
        (property == "media-title" or property == "force-media-title") and "string" or "number", render)
end
-- Unset without the console script, so a missing console never hides the
-- panel or swallows remote keys.
mp.observe_property("user-data/mpv/console/open", "bool", function(_, open)
    menu_open = open == true
    render()
end)
mp.register_event("shutdown", reset)

local function toggle_help()
    if not loaded or menu_open then return end
    help_requested = not help_requested
    startup_until = 0
    render()
end

local function select_subtitles()
    if not loaded or menu_open then return end
    local ids, items = {"no"}, {"Off"}
    local current = mp.get_property_native("sid")
    local default_item = 1
    for _, track in ipairs(mp.get_property_native("track-list", {})) do
        if track.type == "sub" then
            ids[#ids + 1] = track.id
            local label = track.title
            if not label or label == "" then label = "Subtitle " .. track.id end
            label = track.id .. " · " .. label:gsub("%s+", " ")
            if track.lang then label = label .. " · " .. track.lang end
            if track.external then label = label .. " · external" end
            if track.id == current then
                label = label .. " [selected]"
                default_item = #ids
            end
            items[#items + 1] = label
        end
    end
    local selected_generation = generation
    own_menu = true
    -- Reuse mpv's native selector renderer/input routing; the console-open
    -- observer hides pause/help panels. No separate menu or key dispatcher.
    input.select({
        prompt = #items == 1 and "Subtitles (no tracks available):"
            or "Subtitles — Up/Down Choose · OK Select · Back Close:",
        items = items, default_item = default_item,
        submit = function(index)
            if loaded and generation == selected_generation and ids[index] then
                mp.set_property_native("sid", ids[index])
            end
        end,
        closed = function()
            if generation == selected_generation then own_menu = false end
        end,
    })
end

mp.add_key_binding(nil, "toggle-help", toggle_help)
mp.add_key_binding(opts.controls == "keyboard" and "TAB" or nil, "subtitles", select_subtitles)

-- pépé forwards web remote actions here while playing. Any open console menu
-- gets the keys; otherwise OK/Back act like a TV remote and Up/Down keep
-- mpv's bindings.
local remote_keys = {up = "UP", down = "DOWN", left = "LEFT", right = "RIGHT", ok = "ENTER", back = "ESC"}
mp.register_script_message("remote", function(action)
    if menu_open or action == "up" or action == "down" then
        if remote_keys[action] then mp.commandv("keypress", remote_keys[action]) end
    elseif action == "left" then mp.command("seek -10")
    elseif action == "right" then mp.command("seek 10")
    elseif action == "ok" then mp.command("cycle pause")
    elseif action == "back" then mp.command("quit")
    elseif action == "subtitles" then select_subtitles()
    end
end)
if opts.controls == "hdmi-cec" then
    if opts.volume_keys then
        mp.set_property_native("volume-max", 100)
        if mp.get_property_number("volume", 100) > 100 then
            mp.set_property_native("volume", 100)
        end
    end
    -- Only this profile overrides native mpv shortcuts. Console/selector
    -- bindings activated later take priority until their menu is closed.
    local bindings = {
        {"s", "cycle sub"}, {"w", "ignore"}, {"HOME", "quit"},
        {"ENTER", "cycle pause"}, {"ESC", "quit"},
        -- Programme keys are inert unless this player explicitly opts in.
        {"F3", opts.volume_keys and "osd-msg-bar add volume 5" or "ignore", {repeatable = opts.volume_keys}},
        {"F4", opts.volume_keys and "osd-msg-bar add volume -5" or "ignore", {repeatable = opts.volume_keys}},
        {"F5", "set pause no"}, {"F6", "set pause yes"},
        {"F7", "cycle pause"}, {"F8", "quit"},
    }
    for _, binding in ipairs(bindings) do
        local command = binding[2]
        mp.add_forced_key_binding(binding[1], "cec-" .. binding[1],
            function() mp.command(command) end, binding[3])
    end
    -- Most TV remotes only send Rewind/Fast forward. In a playlist they and
    -- Previous/Next change entry, once per press so holding never skips
    -- several episodes; otherwise they seek (repeating) or change chapter.
    local media_keys = {
        {"F9", "seek -10", "playlist-prev", true}, {"F10", "seek 10", "playlist-next", true},
        {"F11", "add chapter -1", "playlist-prev", false}, {"F12", "add chapter 1", "playlist-next", false},
    }
    for _, key in ipairs(media_keys) do
        local single, playlist = key[2], key[3]
        mp.add_forced_key_binding(key[1], "cec-" .. key[1], function(event)
            if event.event == "up" then return end
            if not playlist_position() then
                mp.command(single)
            elseif event.event ~= "repeat" then
                mp.command(playlist)
            end
        end, {complex = true, repeatable = key[4]})
    end
    mp.add_forced_key_binding("r", "cec-help", toggle_help)
    mp.add_forced_key_binding("TAB", "cec-subtitles", select_subtitles)
end
