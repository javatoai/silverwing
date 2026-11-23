package profilelaunch

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// OpenDesktop launches the current executable on a different profile.
func OpenDesktop(currentHome, chosenHome string) error {
	home, err := CanonicalHome(chosenHome)
	if err != nil {
		return err
	}
	if sameHome(currentHome, home) {
		return fmt.Errorf("此窗口已使用所选应用数据目录：%s", home)
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("定位 Silverwing 程序：%w", err)
	}
	cmd := exec.Command(executable, "--"+ConfigDirFlag, home)
	cmd.Dir = filepath.Dir(executable)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("打开新 Silverwing 窗口：%w", err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
