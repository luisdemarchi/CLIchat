package main

import (
	"embed"

	localapp "github.com/luisdemarchi/CLIchat/internal/app"
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
		Title:  "CLIchat",
		Width:  1180,
		Height: 780,
		Mac: &mac.Options{
			About: &mac.AboutInfo{
				Title:   "CLIchat",
				Message: "WhatsApp-style desktop chat for Claude Code and Codex CLIs.\n\nProjeto open source feito por Luís De Marchi (@luisdemarchi) — Brasil.",
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
