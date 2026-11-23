package profilelaunch

import (
	"os"
	"os/exec"

	"silverwing-go/internal/productprofile"
)

// ShowStartupError gives Finder launches a visible error before MyGO starts.
func ShowStartupError(err error) {
	if info, statErr := os.Stderr.Stat(); statErr == nil {
		if nullInfo, nullErr := os.Stat(os.DevNull); nullErr == nil && !os.SameFile(info, nullInfo) {
			return
		}
	}
	const script = `on run argv
		display alert (item 1 of argv) message (item 2 of argv) as critical
	end run`
	_ = exec.Command("/usr/bin/osascript", "-e", script, productprofile.Name+" 启动失败", err.Error()).Run()
}
