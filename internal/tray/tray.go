package tray

import (
	"context"
	"log"
	"os/exec"
	"runtime"

	"gatekeep/internal/buildinfo"
	"gatekeep/internal/icon"
	"gatekeep/internal/notify"
	"gatekeep/internal/startup"
	"gatekeep/internal/update"
	"github.com/getlantern/systray"
)

type Options struct {
	URL             string
	OnReady         func()
	OnQuit          func()
	OnNotifications func(enabled bool)
}

func Run(opts Options) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	systray.Run(func() { onReady(ctx, opts) }, func() {
		cancel()
		if opts.OnQuit != nil {
			opts.OnQuit()
		}
	})
}

func Quit() {
	systray.Quit()
}

func onReady(ctx context.Context, opts Options) {
	png, err := icon.PNG(64)
	if err != nil {
		log.Fatalf("icon: %v", err)
	}
	systray.SetIcon(png)
	systray.SetTooltip("GateKeep " + buildinfo.Version)
	versionItem := systray.AddMenuItem("GateKeep "+buildinfo.Version, "Commit "+buildinfo.Commit)
	versionItem.Disable()
	updateItem := systray.AddMenuItem("Get update", "Download the latest GateKeep release")
	updateItem.Hide()
	systray.AddSeparator()

	openItem := systray.AddMenuItem("Open UI", "Open GateKeep in the browser")
	loginItem := systray.AddMenuItemCheckbox("Start at Login", "Open GateKeep when you log in", startup.Enabled())
	noteItem := systray.AddMenuItemCheckbox("Notifications", "Notify when a request is waiting", notify.Enabled())
	systray.AddSeparator()
	quitItem := systray.AddMenuItem("Quit", "Quit GateKeep")

	if opts.OnReady != nil {
		opts.OnReady()
	}

	releases := make(chan *update.Release, 1)
	go func() {
		release, err := update.Check(ctx, buildinfo.Version)
		if err != nil && ctx.Err() == nil {
			log.Printf("update check: %v", err)
		}
		releases <- release
	}()

	go func() {
		var releaseURL string
		for {
			select {
			case <-ctx.Done():
				return
			case release := <-releases:
				if release != nil {
					releaseURL = release.URL
					updateItem.SetTitle("Get " + release.Version)
					updateItem.Show()
				}
			case <-updateItem.ClickedCh:
				if releaseURL != "" {
					if err := openURL(releaseURL); err != nil {
						log.Printf("open release: %v", err)
					}
				}
			case <-openItem.ClickedCh:
				if err := openURL(opts.URL); err != nil {
					log.Printf("open ui: %v", err)
				}
			case <-loginItem.ClickedCh:
				enable := !loginItem.Checked()
				if err := startup.Set(enable); err != nil {
					log.Printf("startup: %v", err)
				}
				if startup.Enabled() {
					loginItem.Check()
				} else {
					loginItem.Uncheck()
				}
				if enable && startup.NeedsApproval() {
					if err := openURL("x-apple.systempreferences:com.apple.LoginItems-Settings.extension"); err != nil {
						log.Printf("startup approval: %v", err)
					}
				}
			case <-noteItem.ClickedCh:
				enable := !noteItem.Checked()
				if opts.OnNotifications != nil {
					opts.OnNotifications(enable)
				}
				if notify.Enabled() {
					noteItem.Check()
				} else {
					noteItem.Uncheck()
				}
			case <-quitItem.ClickedCh:
				systray.Quit()
				return
			}
		}
	}()
}

func openURL(raw string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", raw)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", raw)
	default:
		cmd = exec.Command("xdg-open", raw)
	}
	return cmd.Start()
}
