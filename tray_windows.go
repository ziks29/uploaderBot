//go:build windows

package main

import (
	_ "embed"
	"os/exec"

	"github.com/getlantern/systray"
)

//go:embed logo_v1.png
var trayIcon []byte

func startApp() {
	systray.Run(func() {
		systray.SetIcon(trayIcon)
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
