package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"silverwing-go/internal/appupdate"
)

// Portable replaces only recorded application files, leaving additional files
// alone. Backups and the journal are durable before the first replacement.
type Portable struct{}

type journal struct {
	Files []appupdate.File `json:"files"`
	Added []string         `json:"added"`
}

func payload(bundle appupdate.Bundle) (string, error) {
	return appupdate.Resolve(bundle.Directory, bundle.Metadata.Payload)
}

func (Portable) Apply(ctx context.Context, p appupdate.Plan) error {
	root, err := payload(p.New)
	if err != nil {
		return err
	}
	layout := p.New.Metadata.Layout
	if layout == nil {
		return errors.New("更新包缺少文件清单")
	}
	if err := appupdate.VerifyLayout(root, *layout); err != nil {
		return err
	}
	old := map[string]bool{}
	files := append([]appupdate.File(nil), p.Installation.Layout.Files...)
	marker, err := filepath.Rel(p.Installation.Root, p.Installation.LayoutPath)
	if err != nil {
		return err
	}
	marker = filepath.ToSlash(marker)
	info, err := os.Stat(p.Installation.LayoutPath)
	if err != nil {
		return err
	}
	hash, err := appupdate.Digest(p.Installation.LayoutPath)
	if err != nil {
		return err
	}
	files = append(files, appupdate.File{Path: marker, Size: info.Size(), Mode: uint32(info.Mode().Perm()), SHA256: hash})
	j := journal{Files: files}
	for _, f := range files {
		old[strings.ToLower(f.Path)] = true
	}
	for _, f := range layout.Files {
		dst, e := appupdate.Resolve(p.Installation.Root, f.Path)
		if e != nil {
			return e
		}
		if !old[strings.ToLower(f.Path)] {
			if _, e := os.Lstat(dst); !errors.Is(e, os.ErrNotExist) {
				return fmt.Errorf("新程序文件与现有文件冲突：%s", f.Path)
			}
			j.Added = append(j.Added, f.Path)
		}
	}
	if err := os.Mkdir(p.Backup, 0o700); err != nil {
		return err
	}
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		src, e := appupdate.Resolve(p.Installation.Root, f.Path)
		if e != nil {
			return e
		}
		dst, e := appupdate.Resolve(p.Backup, f.Path)
		if e != nil {
			return e
		}
		if err := appupdate.CopyFile(src, dst, os.FileMode(f.Mode)); err != nil {
			return err
		}
	}
	if err := appupdate.WriteJSON(filepath.Join(p.Backup, "journal.json"), j); err != nil {
		return err
	}
	for _, f := range layout.Files {
		src, e := appupdate.Resolve(root, f.Path)
		if e != nil {
			return e
		}
		dst, e := appupdate.Resolve(p.Installation.Root, f.Path)
		if e != nil {
			return e
		}
		if err := replace(src, dst, os.FileMode(f.Mode)); err != nil {
			return err
		}
	}
	next := map[string]bool{}
	for _, f := range layout.Files {
		next[strings.ToLower(f.Path)] = true
	}
	for _, f := range files {
		if next[strings.ToLower(f.Path)] {
			continue
		}
		dst, e := appupdate.Resolve(p.Installation.Root, f.Path)
		if e != nil {
			return e
		}
		if err := os.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return appupdate.VerifyLayout(p.Installation.Root, *layout)
}

func (Portable) Rollback(_ context.Context, p appupdate.Plan) error {
	var j journal
	if err := appupdate.ReadJSON(filepath.Join(p.Backup, "journal.json"), &j); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, f := range j.Files {
		src, err := appupdate.Resolve(p.Backup, f.Path)
		if err != nil {
			return err
		}
		hash, err := appupdate.Digest(src)
		if err != nil || hash != f.SHA256 {
			return errors.New("恢复副本校验失败")
		}
		dst, err := appupdate.Resolve(p.Installation.Root, f.Path)
		if err != nil {
			return err
		}
		// An occupied file may be the one Apply never replaced. Do not try to
		// replace an intact original while restoring the files changed earlier.
		if current, err := appupdate.Digest(dst); err == nil && current == f.SHA256 {
			continue
		}
		if err := replace(src, dst, os.FileMode(f.Mode)); err != nil {
			return err
		}
	}
	for _, rel := range j.Added {
		dst, err := appupdate.Resolve(p.Installation.Root, rel)
		if err != nil {
			return err
		}
		if err := os.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func replace(source, destination string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(destination), ".silverwing-update-")
	if err != nil {
		return err
	}
	path := file.Name()
	file.Close()
	defer os.Remove(path)
	if err := os.Remove(path); err != nil {
		return err
	}
	if err := appupdate.CopyFile(source, path, mode); err != nil {
		return err
	}
	return os.Rename(path, destination)
}
