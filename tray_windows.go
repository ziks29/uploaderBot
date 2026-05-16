//go:build windows

package main

import (
	"bytes"
	_ "embed"
	"encoding/binary"
	"os/exec"

	"github.com/getlantern/systray"
)

//go:embed logo_v1.png
var logoPNG []byte

// pngToIco wraps raw PNG bytes in a minimal ICO container.
// getlantern/systray on Windows requires ICO format, not plain PNG.
func pngToIco(png []byte) []byte {
	// Read dimensions from PNG IHDR (bytes 16–23 after the 8-byte signature + 8-byte chunk header)
	w := int(binary.BigEndian.Uint32(png[16:20]))
	h := int(binary.BigEndian.Uint32(png[20:24]))
	bw, bh := byte(w), byte(h)
	if w >= 256 {
		bw = 0
	}
	if h >= 256 {
		bh = 0
	}

	buf := new(bytes.Buffer)
	// ICONDIR
	binary.Write(buf, binary.LittleEndian, uint16(0)) // reserved
	binary.Write(buf, binary.LittleEndian, uint16(1)) // type: icon
	binary.Write(buf, binary.LittleEndian, uint16(1)) // image count
	// ICONDIRENTRY
	buf.WriteByte(bw)
	buf.WriteByte(bh)
	buf.WriteByte(0)  // color count
	buf.WriteByte(0)  // reserved
	binary.Write(buf, binary.LittleEndian, uint16(1))           // planes
	binary.Write(buf, binary.LittleEndian, uint16(32))          // bit count
	binary.Write(buf, binary.LittleEndian, uint32(len(png)))    // image data size
	binary.Write(buf, binary.LittleEndian, uint32(6+16))        // image data offset
	buf.Write(png)
	return buf.Bytes()
}

func startApp() {
	systray.Run(func() {
		systray.SetIcon(pngToIco(logoPNG))
		systray.SetTitle("Fivemanage Uploader")
		systray.SetTooltip("Fivemanage Uploader Bot — running")

		mStatus := systray.AddMenuItem("● Bot Running", "")
		mStatus.Disable()
		systray.AddSeparator()
		mDashboard := systray.AddMenuItem("Open Dashboard", "Open app.fivemanage.com")
		systray.AddSeparator()
		mQuit := systray.AddMenuItem("Quit", "Stop the bot and exit")

		go func() {
			for {
				select {
				case <-mDashboard.ClickedCh:
					exec.Command("rundll32", "url.dll,FileProtocolHandler", "https://app.fivemanage.com").Start()
				case <-mQuit.ClickedCh:
					systray.Quit()
					return
				}
			}
		}()
	}, func() {})
}
