package main

import (
	"embed"

	localapp "agent-chat-local/internal/app"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	application := localapp.New()

	err := wails.Run(&options.App{
		Title:  "Agent Chat Local",
		Width:  1180,
		Height: 780,
		Mac: &mac.Options{
			About: &mac.AboutInfo{
				Title:   "Agent Chat Local",
				Message: "Chat local para Claude, Gemini e Codex.",
			},
		},
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 246, G: 247, B: 241, A: 1},
		OnStartup:        application.Startup,
		Bind: []interface{}{
			application,
		},
	})
	if err != nil {
		println("error:", err.Error())
	}
}
