package main

import (
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

// Embeds frontend/web-build, not the Vite default frontend/dist — see
// vite.config.ts for why (repo root .gitignore blanket-ignores any `dist/`).
//
//go:embed all:frontend/web-build
var assets embed.FS

func main() {
	app := NewApp()

	err := wails.Run(&options.App{
		Title:  "onlinemenu.tr POS",
		Width:  1280,
		Height: 800,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		Bind: []interface{}{
			app,
		},
		// Debug.OpenInspectorOnStartup is gated by the `dev` build tag
		// (devtools_dev.go / devtools_release.go). Release builds
		// (`task pos:build`, no -tags dev) always compile in `false` here —
		// the inspector cannot be enabled in a shipped POS station binary.
		Debug: options.Debug{
			OpenInspectorOnStartup: openInspectorOnStartup,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
