//go:build !windows

package native

import "silverwing-go/internal/appupdate"

func detectWindows(i appupdate.Installation) (appupdate.Installation, error) { return i, nil }
