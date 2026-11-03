package install

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"silverwing-go/internal/appupdate"
)

type Mac struct{ Runner Runner }

func (m Mac) Apply(ctx context.Context, p appupdate.Plan) error {
	if filepath.Ext(p.Installation.Root) != ".app" || p.New.Metadata.Layout == nil {
		return errors.New("不是完整的 macOS 应用")
	}
	source, err := payload(p.New)
	if err != nil {
		return err
	}
	if err := appupdate.VerifyLayout(source, *p.New.Metadata.Layout); err != nil {
		return err
	}
	code, err := m.Runner.Run(ctx, "/usr/bin/codesign", []string{"--verify", "--deep", "--strict", source})
	if err != nil || code != 0 {
		return errors.New("macOS 应用签名检查失败")
	}
	work := macWork(p)
	if err := os.Mkdir(work, 0o700); err != nil {
		return err
	}
	staged := filepath.Join(work, "new.app")
	for _, f := range p.New.Metadata.Layout.Files {
		src, e := appupdate.Resolve(source, f.Path)
		if e != nil {
			return e
		}
		dst, e := appupdate.Resolve(staged, f.Path)
		if e != nil {
			return e
		}
		if err := appupdate.CopyFile(src, dst, os.FileMode(f.Mode)); err != nil {
			return err
		}
	}
	if code, err := m.Runner.Run(ctx, "/usr/bin/codesign", []string{"--verify", "--deep", "--strict", staged}); err != nil || code != 0 {
		return errors.New("暂存应用签名检查失败")
	}
	if err := os.Rename(p.Installation.Root, filepath.Join(work, "old.app")); err != nil {
		return err
	}
	if err := os.Rename(staged, p.Installation.Root); err != nil {
		_ = os.Rename(filepath.Join(work, "old.app"), p.Installation.Root)
		return err
	}
	return nil
}

func (m Mac) Rollback(_ context.Context, p appupdate.Plan) error {
	work := macWork(p)
	old := filepath.Join(work, "old.app")
	if _, err := os.Stat(old); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if _, err := os.Lstat(p.Installation.Root); err == nil {
		if err := os.Rename(p.Installation.Root, filepath.Join(work, "failed.app")); err != nil {
			return err
		}
	}
	return os.Rename(old, p.Installation.Root)
}

func macWork(p appupdate.Plan) string {
	return filepath.Join(filepath.Dir(p.Installation.Root), ".silverwing-update-"+p.ID)
}
