package main

import (
	"flag"
	"log"
	"os"
	"path/filepath"

	"silverwing-go/internal/application"
	"silverwing-go/internal/desktop"
)

func main() {
	userHome, err := os.UserHomeDir()
	if err != nil {
		log.Fatal(err)
	}
	defaultHome := filepath.Join(userHome, "silverwing-qa")
	home := flag.String("home", defaultHome, "silverwing-qa data directory")
	page := flag.String("page", "tasks", "initial page: tasks, archive, requirements, tools, skills, or settings")
	dark := flag.Bool("dark", false, "start with the dark theme")
	width := flag.Int("width", 0, "initial window width; zero restores the saved size")
	height := flag.Int("height", 0, "initial window height; zero restores the saved size")
	tab := flag.Int("tab", 1, "initial task tab: 1 materials, 2 requirement, 3 notes")
	capture := flag.String("capture", "", "write a PNG capture of the native window")
	captureOnly := flag.Bool("capture-only", false, "exit after writing the requested capture")
	flag.Parse()
	explicitNavigation := false
	flag.Visit(func(item *flag.Flag) {
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
		CapturePath: *capture, CaptureOnly: *captureOnly,
	}); err != nil {
		log.Fatal(err)
	}
}
