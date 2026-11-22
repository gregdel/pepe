-- Offline script tests; run with the same Lua runtime as the target mpv.
local script = assert(arg[1], "usage: luajit tests/mpv-osd.lua internal/app/mpv/pepe.lua")
local real_time, real_date = os.time, os.date
local now = real_time({year = 2026, month = 10, day = 6, hour = 22, min = 29, sec = 0})
local props = {
    pause = false, ["time-pos"] = 38 * 60, duration = 80 * 60, speed = 1,
    ["media-title"] = "Movie name (2024)", filename = "fallback.mkv",
    ["osd-dimensions"] = {aspect = 16 / 9}, ["volume-max"] = 130,
}
local observers, events, updates, removes = {}, {}, 0, 0
local escaped = {}
local options = {help = false}
local bindings, forced, commands, repeatable, messages = {}, {}, {}, {}, {}
local console_open = "user-data/mpv/console/open"
local selection
package.loaded["mp.options"] = {read_options = function(opts)
    for k, v in pairs(options) do opts[k] = v end
end}
package.loaded["mp.input"] = {
    select = function(opts) selection = opts end,
    terminate = function() if selection then local s = selection; selection = nil; s.closed() end end,
}
local overlay = {
    update = function() updates = updates + 1 end,
    remove = function() removes = removes + 1 end,
}
local timer = {enabled = false}
function timer:kill() self.enabled = false end
function timer:resume() self.enabled = true end
function timer:is_enabled() return self.enabled end
local tick
local function get(name, default)
    if props[name] == nil then return default end
    return props[name]
end
package.loaded.mp = {
    create_osd_overlay = function(format) assert(format == "ass-events"); return overlay end,
    get_property = get, get_property_number = get, get_property_bool = get,
    get_property_native = get,
    get_time = function() return now end,
    set_property_native = function(name, value) props[name] = value end,
    add_key_binding = function(key, name, fn) bindings[name] = {key = key, fn = fn} end,
    add_forced_key_binding = function(key, name, fn, flags)
        forced[key] = fn
        repeatable[key] = flags and flags.repeatable or false
    end,
    command = function(command) commands[#commands + 1] = command end,
    commandv = function(...) commands[#commands + 1] = table.concat({...}, " ") end,
    register_script_message = function(name, fn) messages[name] = fn end,
    osd_message = function(message) commands[#commands + 1] = message end,
    observe_property = function(name, kind, fn)
        assert(not observers[name])
        assert(kind == "bool" or kind == "number" or kind == "string" or kind == "native")
        observers[name] = fn
    end,
    register_event = function(name, fn) events[name] = fn end,
    add_periodic_timer = function(seconds, fn, disabled)
        assert(seconds == 1 and disabled == true)
        tick = fn
        return timer
    end,
    command_native = function(command)
        assert(command[1] == "escape-ass")
        escaped[#escaped + 1] = command[2]
        -- Return a marker so assertions detect any unescaped metadata.
        return "ESCAPED_TITLE"
    end,
}
os.time = function() return now end
dofile(script)
local function set(name, value)
    props[name] = value
    if observers[name] then observers[name](name, value) end
end
local function contains(text)
    assert(overlay.data:find(text, 1, true), "missing " .. text .. " in " .. overlay.data)
end
local function absent(text)
    assert(not overlay.data:find(text, 1, true), "unexpected " .. text)
end
local function remote_bars()
    local count = 0
    local previous_x
    local colours = {"9BD781", "6BD8FF", "FFB468"}
    for line in overlay.data:gmatch("[^\n]+") do
        if line:find("\\p1}", 1, true) then
            local x1, y1, x2, y2 = line:match("m (%-?%d+) (%-?%d+) l (%-?%d+) %d+ %d+ (%-?%d+)")
            x1, y1, x2, y2 = tonumber(x1), tonumber(y1), tonumber(x2), tonumber(y2)
            if x2 - x1 == 40 and y2 - y1 == 8 then
                count = count + 1
                assert(line:find(colours[count], 1, true), "remote bars out of colour order")
                assert(not previous_x or x1 > previous_x)
                previous_x = x1
            end
        end
    end
    -- Red (w) is inert during playback, so it gets no bar.
    assert(count == 3, "missing grouped remote bars")
    for _, name in ipairs({"Red", "Green", "Yellow", "Blue"}) do absent(name) end
    for _, label in ipairs({"Help", "Subtitle tracks", "Cycle subtitles"}) do contains(label) end
    contains("Back Return")
end
local function at(x, y, alignment, value)
    local anchor = string.format("\\an%d\\pos(%d,%d)", alignment, x, y)
    for line in overlay.data:gmatch("[^\n]+") do
        if line:find(anchor, 1, true) and line:sub(-#value) == value then return end
    end
    error("missing anchored text: " .. anchor .. " " .. value)
end
local function remaining(value) at(640, 418, 8, value) end
local function started(value) at(108, 530, 7, value) end
local function current(value) at(640, 530, 8, value) end
local function finish(value) at(1172, 530, 9, value) end
local function bar(fill_end, cursor)
    local track, fill, marker = false, false, false
    for line in overlay.data:gmatch("[^\n]+") do
        if line:find("\\p1}", 1, true) then
            local x1, y1, x2, y2 = line:match("m (%-?%d+) (%-?%d+) l (%-?%d+) %d+ %d+ (%-?%d+)")
            x1, y1, x2, y2 = tonumber(x1), tonumber(y1), tonumber(x2), tonumber(y2)
            if y1 == 472 and y2 == 482 then
                assert(x1 == 108 and x2 <= 1172 and x2 > x1)
                if line:find("6B6259", 1, true) then
                    assert(x2 == 1172); track = true
                else
                    assert(line:find("D8C568", 1, true) and x2 == fill_end)
                    fill = true
                end
            elseif y1 == 467 and y2 == 487 then
                assert(cursor and x1 == cursor - 2 and x2 == cursor + 2)
                marker = true
            end
        end
    end
    assert(track and fill == (fill_end ~= nil) and marker == (cursor ~= nil))
end

-- Initial observations and startup pause cannot show stale/unloaded media.
for name, fn in pairs(observers) do fn(name, props[name]) end
assert(overlay.data == "" and not timer.enabled)
set("pause", true)
assert(overlay.data == "" and not timer.enabled)
set("pause", false)
events["start-file"]()
events["file-loaded"]()
assert(overlay.data == "" and not timer.enabled)

now = now + 38 * 60 -- 23h07
set("pause", true)
assert(timer.enabled and escaped[#escaped] == "Movie name (2024)")
contains("ESCAPED_TITLE")
at(108, 396, 7, "ELAPSED")
at(640, 396, 8, "REMAINING")
at(1172, 396, 9, "TOTAL")
at(108, 418, 7, "0:38:00")
at(1172, 418, 9, "1:20:00")
remaining("42 min left")
started("22h29")
current("23h07")
finish("23h49")
at(1172, 584, 9, "if resumed now")
contains("\\clip(108,318,1012,382)")
bar(613, 613) -- 47.5% of the media, not elapsed wall-clock time.
assert(overlay.res_x == 1280 and overlay.res_y == 720)
-- The panel leaves a stable strip for native mouse controls.
contains("m 80 318 l 1200 318 1200 600 80 600")
local keyboard_data = overlay.data
set("mouse-pos", {x = 10, y = 200, hover = true})
set("mouse-pos", {x = 10, y = 700, hover = true})
assert(overlay.data == keyboard_data and timer.enabled)

local previous = updates
tick()
assert(updates == previous, "unchanged overlay need not be uploaded")
now = now + 60
tick()
remaining("42 min left")
current("23h08")
finish("23h50")
started("22h29")
bar(613, 613)

-- Speed changes and seeking affect the estimate, not the recorded start.
set("speed", 2)
remaining("21 min left")
finish("23h29")
at(640, 396, 8, "REMAINING · 2×")
bar(613, 613) -- Speed changes the estimate, not the media progress.
set("time-pos", 40 * 60)
remaining("20 min left")
finish("23h28")
bar(640, 640)
set("speed", 0.5)
remaining("80 min left")
finish("00h28")
at(1172, 564, 9, "2026-10-07")

-- No guessed finish for live streams, missing position or invalid values.
for _, name in ipairs({"duration", "time-pos", "speed"}) do
    local saved = props[name]
    for _, value in ipairs({false, -1, math.huge, 0 / 0}) do
        set(name, value)
        remaining("Unknown")
        finish("Unknown")
        if name ~= "speed" then
            bar(nil, nil)
            contains("PROGRESS UNKNOWN")
        else
            bar(640, 640)
        end
    end
    set(name, nil)
    remaining(name == "speed" and "40 min left" or "Unknown")
    set(name, saved)
end
set("duration", 0)
finish("Unknown")
bar(nil, nil)
set("duration", 80 * 60)
set("speed", 0)
finish("Unknown")
set("speed", 1)
set("time-pos", 80 * 60 + 1)
remaining("0 min left")
finish("23h08")
bar(1172, 1172)
set("time-pos", 80 * 60 - 1)
remaining("1 min left")
set("time-pos", 0)
at(108, 418, 7, "0:00:00")
bar(nil, 108)
absent("PROGRESS UNKNOWN")

-- Titles are escaped, whitespace normalized, and fallback is available.
set("media-title", "Show name S02E05 - Episode name")
assert(escaped[#escaped] == "Show name S02E05 - Episode name")
set("media-title", "Literal {\\b1} \\N title\nnext\tline")
assert(escaped[#escaped] == "Literal {\\b1} \\N title next line")
absent("Literal")
set("media-title", "")
assert(escaped[#escaped] == "Video")
set("media-title", nil)
assert(escaped[#escaped] == "Video")
set("media-title", string.rep("Long title 名称 ", 80))
assert(escaped[#escaped] == props["media-title"])
contains("\\clip(108,318,1012,382)")
set("osd-dimensions", {aspect = 4 / 3})
assert(overlay.res_x == 960 and overlay.res_y == 720)
at(60, 418, 7, "0:00:00")
at(900, 418, 9, "1:20:00")
set("osd-dimensions", {aspect = 9 / 16})
assert(overlay.res_x == 720 and overlay.res_y == 1280)
at(60, 978, 7, "0:00:00")
at(660, 978, 9, "1:20:00")
for _, aspect in ipairs({false, -1, 0, math.huge, 0 / 0}) do
    set("osd-dimensions", {aspect = aspect})
    assert(overlay.res_x == 1280 and overlay.res_y == 720)
end
set("osd-dimensions", {})
assert(overlay.res_x == 1280)

-- Resuming stops all periodic work and removes the overlay immediately.
previous = removes
set("pause", false)
assert(not timer.enabled and overlay.data == "" and removes == previous + 1)
now = now + 5 * 60
set("pause", true)
started("22h29")
current("23h13")

-- End/new file resets the recorded start, including a file loaded paused.
events["end-file"]()
assert(not timer.enabled and overlay.data == "")
events["start-file"]()
set("media-title", "Second movie (2025)")
assert(overlay.data == "")
events["file-loaded"]()
contains("Not started yet")
now = now + 60
set("pause", false)
now = now + 60
set("pause", true)
started("23h14")
current("23h15")
assert(escaped[#escaped] == "Second movie (2025)")
now = now + 24 * 60 * 60
tick()
started("23h14")
at(108, 564, 7, "2026-10-06")
current("23h15")
finish("00h35")
at(1172, 564, 9, "2026-10-08")
events.shutdown()
assert(not timer.enabled and overlay.data == "")

-- Keyboard mode adds a non-forced Tab shortcut, preserving native/user keys.
assert(next(forced) == nil)
assert(bindings["toggle-help"].key == nil and bindings.subtitles.key == "TAB")

-- Native selector data and close lifecycle, including explicit Off and sparse IDs.
-- The console reports itself open/closed; that alone hides/restores the panel.
local function close_selection()
    set(console_open, false)
    selection.closed()
end
events["file-loaded"]()
props["track-list"] = {
    {id = 9, type = "audio"}, {id = 3, type = "sub", lang = "en", title = "English"},
    {id = 7, type = "sub", lang = "fr", external = true, title = "French\nexternal"},
}
props.sid = 7
bindings.subtitles.fn()
set(console_open, true)
assert(selection and selection.default_item == 3 and #selection.items == 3)
assert(selection.items[1] == "Off" and selection.items[3]:find("[selected]", 1, true))
assert(overlay.data == "" and not timer.enabled)
selection.submit(2)
assert(props.sid == 3)
close_selection()
assert(overlay.data ~= "")
bindings.subtitles.fn()
set(console_open, true)
selection.submit(1)
assert(props.sid == "no")
close_selection()
props["track-list"] = {}
bindings.subtitles.fn()
set(console_open, true)
assert(#selection.items == 1 and selection.prompt:find("no tracks", 1, true))
local stale_selection = selection
events["end-file"]()
assert(selection == nil, "end-file did not close pepe's selector")
set(console_open, false)
assert(overlay.data == "" and not timer.enabled)
events["file-loaded"]()
props.sid = 7
stale_selection.submit(1)
assert(props.sid == 7, "old menu changed a new file's subtitle track")

-- Web remote: without a menu, OK/Back/Left/Right drive playback and Up/Down
-- keep mpv's bindings.
local remote = messages.remote
set("pause", true)
local panel = overlay.data
assert(panel ~= "")
for _, case in ipairs({
    {"ok", "cycle pause"}, {"back", "quit"}, {"left", "seek -10"}, {"right", "seek 10"},
    {"up", "keypress UP"}, {"down", "keypress DOWN"},
}) do
    remote(case[1])
    assert(commands[#commands] == case[2], "remote " .. case[1] .. " sent " .. commands[#commands])
end
remote("subtitles")
assert(selection, "remote subtitles did not open the selector")
close_selection()
-- Any console menu, including mpv's own (g-s, console), gets every key and
-- hides the panel; the selector cannot be stacked on top of it.
selection = nil
set(console_open, true)
assert(overlay.data == "" and not timer.enabled)
for action, key in pairs({up = "UP", down = "DOWN", left = "LEFT", right = "RIGHT", ok = "ENTER", back = "ESC"}) do
    remote(action)
    assert(commands[#commands] == "keypress " .. key, "menu did not receive remote " .. action)
end
local sent = #commands
remote("subtitles")
bindings.subtitles.fn()
assert(selection == nil and #commands == sent)
set(console_open, false)
assert(overlay.data == panel and timer.enabled)
-- Without the console script the property never turns true: the panel stays
-- and the remote still pauses and quits.
remote("subtitles")
assert(selection and overlay.data == panel)
remote("ok")
assert(commands[#commands] == "cycle pause")
remote("back")
assert(commands[#commands] == "quit")
events["end-file"]()

-- Reload with CEC controls, hidden automatic hints, and safe web-title handling.
options = {controls = "hdmi-cec", help = false, web = true}
observers, events, bindings, forced, messages = {}, {}, {}, {}, {}
props.pause = false
props["media-title"] = "https://private.example/video?secret=token"
props["force-media-title"] = ""
dofile(script)
assert(forced.s and forced.r and forced.HOME and forced.TAB and forced.ENTER and forced.ESC)
for key in pairs(forced) do
    assert(repeatable[key] == (key == "F9" or key == "F10"),
        "only continuous seek bindings should repeat: " .. key)
end
forced.F9()
assert(commands[#commands] == "seek -10")
forced.F10()
assert(commands[#commands] == "seek 10")
forced.s()
assert(commands[#commands] == "cycle sub")
forced.ENTER()
assert(commands[#commands] == "cycle pause")
assert(props["volume-max"] == 130, "default CEC profile changed the volume limit")
for _, key in ipairs({"F3", "F4"}) do
    forced[key]()
    assert(commands[#commands] == "ignore" and not repeatable[key],
        "programme keys must be inert without the volume opt-in")
end
events["file-loaded"]()
assert(overlay.data == "" and not timer.enabled)
set("pause", true)
assert(escaped[#escaped] == "Web UI video")
absent("secret")
absent("Cycle subtitles") -- Hidden hints do not disable controls or pause metadata.
set("force-media-title", "Resolved web title")
assert(escaped[#escaped] == "Resolved web title")
forced.r()
remote_bars()
forced.r()
absent("Cycle subtitles")
set("pause", false)
forced.r()
contains("Left/Right Seek  ·  Up/Down Seek 1 min")
forced.r()
assert(overlay.data == "")

-- Mouse movement/hover never hides pause metadata or interrupts its clock.
-- Leave native OSC options and click/drag routing unchanged.
set("pause", true)
local paused_data, paused_removes = overlay.data, removes
for _, mouse in ipairs({
    {x = 1, y = 1, hover = true},
    {x = 10, y = 600, hover = true},
    {x = 10, y = 200, hover = true},
    {x = 10, y = 200, hover = false},
}) do
    set("mouse-pos", mouse)
    assert(overlay.data == paused_data and timer.enabled)
    assert(removes == paused_removes)
end
now = now + 10
tick()
contains("PAUSED")
assert(timer.enabled)

-- Fullscreen/resize also preserves the panel, including mouse movement
-- immediately before and after a same-aspect-ratio viewport change.
for _, change in ipairs({
    {"fullscreen", true},
    {"osd-dimensions", {w = 1920, h = 1080, aspect = 16 / 9}},
    {"fullscreen", false},
    {"osd-dimensions", {w = 1280, h = 720, aspect = 16 / 9}},
}) do
    set("mouse-pos", {x = 10, y = 600, hover = true})
    set("mouse-pos", {x = 20, y = 600, hover = true})
    contains("PAUSED")
    assert(timer.enabled)
    set(change[1], change[2])
    contains("PAUSED")
    assert(timer.enabled)
    set("mouse-pos", {x = 640, y = 600, hover = true})
    contains("PAUSED")
    now = now + 10
    tick()
    contains("PAUSED")
end
set("mouse-pos", {x = 650, y = 600, hover = true})
contains("PAUSED")
assert(timer.enabled)
events.shutdown()
assert(overlay.data == "" and not timer.enabled)

-- Automatic startup guidance expires and does no periodic work afterwards.
options = {controls = "hdmi-cec", help = true}
observers, events, bindings, forced, messages = {}, {}, {}, {}, {}
props.pause = false
dofile(script)
events["file-loaded"]()
remote_bars()
assert(timer.enabled)
now = now + 5
tick()
assert(overlay.data == "" and not timer.enabled)
events.shutdown()

-- Opting out of the pause panel leaves CEC controls available independently.
options = {controls = "hdmi-cec", help = true, panel = false}
observers, events, bindings, forced, messages = {}, {}, {}, {}, {}
dofile(script)
events["file-loaded"]()
set("pause", true)
assert(overlay.data == "" and not timer.enabled)
forced.r()
remote_bars()
absent("PAUSED") -- Explicit help still works without enabling the pause panel.
forced.r()
assert(overlay.data == "" and not timer.enabled)
forced.r()
remote_bars()
forced.TAB()
set(console_open, true)
assert(selection and overlay.data == "")
set(console_open, false)
events.shutdown()

-- Programme volume is opt-in, repeatable, capped, and independent of the panel.
options = {controls = "hdmi-cec", volume_keys = true, help = false, panel = false}
observers, events, bindings, forced, messages = {}, {}, {}, {}, {}
props["volume-max"], props.volume = 130, 120
dofile(script)
assert(props["volume-max"] == 100 and props.volume == 100)
for key in pairs(forced) do
    assert(repeatable[key] == (key == "F3" or key == "F4" or key == "F9" or key == "F10"),
        "only continuous volume/seek bindings should repeat: " .. key)
end
forced.F3()
assert(commands[#commands] == "osd-msg-bar add volume 5")
forced.F4()
assert(commands[#commands] == "osd-msg-bar add volume -5")
events.shutdown()

-- Even an explicit volume opt-in must not override keyboard-mode shortcuts.
options = {controls = "keyboard", volume_keys = true}
observers, events, bindings, forced, messages = {}, {}, {}, {}, {}
props["volume-max"], props.volume = 130, 120
dofile(script)
assert(next(forced) == nil and props["volume-max"] == 130 and props.volume == 120)
events.shutdown()

os.time, os.date = real_time, real_date
print("Playback OSD tests passed")
