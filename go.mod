module github.com/gregdel/pepe

go 1.27.1

toolchain go1.27.4

require (
	gioui.org v0.10.2
	github.com/dustin/go-humanize v1.0.1
	github.com/goccy/go-yaml v1.19.2
	github.com/odwrtw/polochon v0.0.0-20260823133424-bbaf79e37c68
	github.com/skip2/go-qrcode v0.0.0-20200617195104-da1b6568686e
	golang.org/x/exp/shiny v0.0.0-20250408133849-7e4ce0ab07d0
	golang.org/x/image v0.26.0
)

require (
	gioui.org/shader v1.0.9 // indirect
	github.com/go-text/typesetting v0.3.5 // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
)

replace gioui.org => github.com/gregdel/gio v0.0.0-20261123113452-9967fc97be47
