package main

import (
	"flag"
	"log"
	"os"

	"silverwing-go/internal/application"
	"silverwing-go/internal/desktop"
	"silverwing-go/internal/productprofile"
)

func main() {
	userHome, err := os.UserHomeDir()
	if err != nil {
		log.Fatal(err)
	}
	defaultHome := productprofile.DefaultHome(userHome)
	home := flag.String("home", defaultHome, productprofile.CLIName+" data directory")
	page := flag.String("page", "tasks", "initial page: tasks, archive, requirements, services, repositories, tools, skills, or settings")
	dark := flag.Bool("dark", false, "start with the dark theme")
	width := flag.Int("width", 0, "initial window width; zero restores the saved size")
	height := flag.Int("height", 0, "initial window height; zero restores the saved size")
	tab := flag.Int("tab", 0, "initial task tab: 0 task branches, 1 materials, 2 requirement, 3 task instructions, 4 Tag, 5 task files, 6 service info, 7 terminal")
	capture := flag.String("capture", "", "write a PNG capture of the native window")
	captureOnly := flag.Bool("capture-only", false, "exit after writing the requested capture")
	updateHealth := flag.String("update-health", "", "internal updater readiness receipt")
	updateToken := flag.String("update-token", "", "internal updater readiness token")
	updateResult := flag.String("update-result", "", "internal updater result receipt")
	flag.Parse()
	explicitNavigation := false
	explicitTab := false
	flag.Visit(func(item *flag.Flag) {
		if item.Name == "tab" {
			explicitTab = true
		}
		if item.Name == "page" || item.Name == "tab" {
			explicitNavigation = true
		}
	})

	backend, err := application.New(*home)
	if err != nil {
		log.Fatal(err)
	}
	if err := desktop.Run(backend, desktop.Options{
		Home: backend.Paths.Home, RestoreSession: *capture == "" && !explicitNavigation,
		Page: *page, Dark: *dark, Width: *width, Height: *height, Tab: *tab,
		TabExplicit: explicitTab,
		CapturePath: *capture, CaptureOnly: *captureOnly,
		UpdateHealthPath: *updateHealth, UpdateHealthToken: *updateToken,
		UpdateResultPath: *updateResult,
	}); err != nil {
		log.Fatal(err)
	}
}
