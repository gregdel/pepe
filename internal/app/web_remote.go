package app

import (
	"fmt"
	"image"
	"image/color"
	"net"
	"slices"
	"strconv"

	"gioui.org/font"
	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	qrcode "github.com/skip2/go-qrcode"
)

type webRemotePage struct {
	urls    []string
	cursor  int
	bitmap  [][]bool
	message string
}

// StartWebUI starts the web remote on addr. A failure (address in use,
// privileged port) is logged and shown on the Web remote page instead of
// stopping pépé, so the address can be fixed in Settings.
func (a *App) StartWebUI(addr string) {
	if err := a.startWebUI(addr); err != nil {
		a.logger().Error("web remote disabled", "address", addr, "error", err)
		a.webUIStartError = err.Error()
	}
}

// openWebRemote shows the QR page without changing the catalog listing.
func (a *App) openWebRemote() {
	page := &webRemotePage{}
	a.browse.closePage()
	a.browse.remote = page
	if a.webUI == nil {
		page.message = "Enable the web remote with web_ui_addr: \":8081\" or -web-ui-addr=:8081."
		if a.webUIStartError != "" {
			page.message = "The web remote could not start (" + a.webUIStartError + "). Change its address in Settings."
		}
		return
	}
	bound, ok := a.webUI.listener.Addr().(*net.TCPAddr)
	if !ok {
		page.message = "Cannot determine the web remote address."
		return
	}
	// Re-read local addresses each time the page is opened (e.g. after Wi-Fi changes).
	var ips []net.IP
	interfaces, err := net.Interfaces()
	if err != nil {
		page.message = "Cannot determine a LAN address. Check your network connection."
		return
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			if ip, ok := address.(*net.IPNet); ok {
				ips = append(ips, ip.IP)
			}
		}
	}
	page.urls = webRemoteURLs(bound, ips)
	if len(page.urls) == 0 {
		page.message = "No phone-accessible LAN address. Connect to your LAN and listen on :8081 instead of localhost."
		return
	}
	page.move(0)
}

// Use the actual bound port, including listeners configured with port zero.
// Never advertise a wildcard, loopback or link-local address to a phone.
func webRemoteURLs(bound *net.TCPAddr, ips []net.IP) []string {
	if bound == nil || bound.Port == 0 {
		return nil
	}
	wildcard := bound.IP.IsUnspecified() || len(bound.IP) == 0
	if !wildcard {
		ips = []net.IP{bound.IP}
	}
	var candidates []net.IP
	for _, ip := range ips {
		if !ip.IsGlobalUnicast() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		if wildcard && bound.IP.To4() != nil && ip.To4() == nil {
			continue // IPv4-only wildcard listener
		}
		candidates = append(candidates, ip)
	}
	// Prefer private IPv4 LAN addresses, but offer every eligible interface:
	// there is no reliable way to infer which network the phone is using.
	slices.SortFunc(candidates, func(a, b net.IP) int {
		rank := func(ip net.IP) int {
			r := 0
			if !ip.IsPrivate() {
				r += 2
			}
			if ip.To4() == nil {
				r++
			}
			return r
		}
		if diff := rank(a) - rank(b); diff != 0 {
			return diff
		}
		return slices.Compare([]byte(a.To16()), []byte(b.To16()))
	})
	var urls []string
	for _, ip := range candidates {
		url := "http://" + net.JoinHostPort(ip.String(), strconv.Itoa(bound.Port)) + "/"
		if !slices.Contains(urls, url) {
			urls = append(urls, url)
		}
	}
	return urls
}

func (page *webRemotePage) move(delta int) {
	if len(page.urls) == 0 {
		return
	}
	page.cursor = (page.cursor + delta + len(page.urls)) % len(page.urls)
	code, err := qrcode.New(page.urls[page.cursor], qrcode.Medium)
	if err != nil {
		page.bitmap = nil
		page.message = "Cannot generate the QR code."
		return
	}
	page.bitmap = code.Bitmap() // includes the four-module white quiet zone
	page.message = ""
}

func (a *App) layoutWebRemote(gtx C) D {
	page := a.browse.remote
	return layout.UniformInset(dp24).Layout(gtx, func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx C) D {
				gtx.Constraints.Min = gtx.Constraints.Max
				return layout.Center.Layout(gtx, func(gtx C) D {
					if page.bitmap == nil {
						gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(unit.Dp(900)))
						return a.body(gtx, sp, page.message, colorFg, 0)
					}
					return layoutRemoteQR(gtx, page.bitmap)
				})
			}),
			layout.Rigid(layout.Spacer{Height: dp12}.Layout),
			layout.Rigid(func(gtx C) D {
				if len(page.urls) == 0 {
					return D{}
				}
				return a.label(gtx, sp, page.urls[page.cursor], colorFg, font.Medium)
			}),
			layout.Rigid(func(gtx C) D {
				if len(page.urls) < 2 {
					return D{}
				}
				return layout.Inset{Top: dp8}.Layout(gtx, func(gtx C) D {
					return a.label(gtx, spMeta, fmt.Sprintf("Address %d of %d · Right, then Up/Down to change", page.cursor+1, len(page.urls)), colorSubtle, 0)
				})
			}),
		)
	})
}

// Draw modules directly at integer pixel sizes: no texture filtering, blurred
// edges or uneven scaling. The bitmap's quiet zone remains solid white.
func layoutRemoteQR(gtx C, bitmap [][]bool) D {
	modules := len(bitmap)
	available := min(gtx.Constraints.Max.X, gtx.Constraints.Max.Y, gtx.Dp(unit.Dp(640)))
	scale := available / modules
	if scale < 1 {
		return D{}
	}
	size := image.Pt(modules*scale, modules*scale)
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	semantic.LabelOp("Web remote QR code").Add(gtx.Ops)
	paint.FillShape(gtx.Ops, color.NRGBA{R: 255, G: 255, B: 255, A: 255}, clip.Rect{Max: size}.Op())
	for y, row := range bitmap {
		for x := 0; x < len(row); {
			if !row[x] {
				x++
				continue
			}
			start := x
			for x < len(row) && row[x] {
				x++
			}
			paint.FillShape(gtx.Ops, color.NRGBA{A: 255}, clip.Rect(image.Rect(start*scale, y*scale, x*scale, (y+1)*scale)).Op())
		}
	}
	return D{Size: size}
}
