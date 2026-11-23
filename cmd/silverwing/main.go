package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"

	"silverwing-go/internal/application"
	"silverwing-go/internal/desktop"
	"silverwing-go/internal/productprofile"
	"silverwing-go/internal/profilelaunch"
	"silverwing-go/internal/storage"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Print(err)
		profilelaunch.ShowStartupError(err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet(productprofile.Name, flag.ContinueOnError)
	var directories profilelaunch.DirectoryFlags
	directories.Register(flags)
	expectedProfileID := flags.String(profilelaunch.ExpectedProfileIDFlag, "", "internal launcher profile identity")
	page := flags.String("page", "tasks", "initial page: tasks, archive, requirements, services, repositories, tools, skills, or settings")
	dark := flags.Bool("dark", false, "start with the dark theme")
	width := flags.Int("width", 0, "initial window width; zero restores the saved size")
	height := flags.Int("height", 0, "initial window height; zero restores the saved size")
	tab := flags.Int("tab", 0, "initial task tab: 0 task branches, 1 materials, 2 requirement, 4 Tag, 5 task files, 6 service info, 7 terminal, 8 project instructions")
	capture := flags.String("capture", "", "write a PNG capture of the native window")
	captureOnly := flags.Bool("capture-only", false, "exit after writing the requested capture")
	updateHealth := flags.String("update-health", "", "internal updater readiness receipt")
	updateToken := flags.String("update-token", "", "internal updater readiness token")
	updateResult := flags.String("update-result", "", "internal updater result receipt")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	explicitNavigation := false
	explicitTab := false
	expectedProfile := false
	flags.Visit(func(item *flag.Flag) {
		if item.Name == "tab" {
			explicitTab = true
		}
		if item.Name == "page" || item.Name == "tab" {
			explicitNavigation = true
		}
		expectedProfile = expectedProfile || item.Name == profilelaunch.ExpectedProfileIDFlag
	})

	if expectedProfile && (!directories.Explicit() || *expectedProfileID == "") {
		return errors.New("快捷方式已失效。请重新创建快捷方式")
	}
	home, err := directories.Resolve()
	if err != nil {
		if expectedProfile {
			return fmt.Errorf("快捷方式已失效。请重新创建快捷方式。%w", err)
		}
		return err
	}
	if expectedProfile {
		if err := profilelaunch.VerifyShortcut(home, *expectedProfileID); err != nil {
			return err
		}
	} else if _, err := storage.ReadProfileID(home); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := storage.CheckProfileDirectory(home); err != nil {
			return fmt.Errorf("无法使用此应用数据目录，原有配置已保留。请使用 --conf-dir 指定其他目录。\n%s\n%w", home, err)
		}
	}
	release, err := profilelaunch.LockDesktop(home)
	if err != nil {
		return err
	}
	defer release()
	if expectedProfile {
		if err := profilelaunch.VerifyShortcut(home, *expectedProfileID); err != nil {
			return err
		}
	}
	backend, err := application.New(home)
	if err != nil {
		var incompatible *storage.UnsupportedConfigVersionError
		if errors.As(err, &incompatible) {
			return fmt.Errorf("应用数据目录的配置格式不兼容，原有配置已保留。请使用 --conf-dir 指定其他目录。\n%s\n%w", home, err)
		}
		return err
	}
	return desktop.Run(backend, desktop.Options{
		Home: backend.Paths.Home, RestoreSession: *capture == "" && !explicitNavigation,
		Page: *page, Dark: *dark, Width: *width, Height: *height, Tab: *tab,
		TabExplicit: explicitTab,
		CapturePath: *capture, CaptureOnly: *captureOnly,
		UpdateHealthPath: *updateHealth, UpdateHealthToken: *updateToken,
		UpdateResultPath: *updateResult,
	})
}
