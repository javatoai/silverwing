//go:build !js

package mygo

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/egoist/mygo/internal/update"
)

// UpdateManifest is the signed-archive descriptor used by MyGO's updater.
type UpdateManifest = update.Manifest

// PreparedUpdate contains a verified archive and its unpacked contents. Preparing
// never changes the installed application. The caller owns the destination.
type PreparedUpdate struct {
	Manifest UpdateManifest
	Archive  string
	Contents string
}

// PrepareUpdate reuses the updater's bounded download, signature verification
// and archive reader without replacing files or starting an application.
func PrepareUpdate(ctx context.Context, source UpdateSource, publicKey, destination string, progress func(int64, int64)) (PreparedUpdate, error) {
	var prepared PreparedUpdate
	if err := ctx.Err(); err != nil {
		return prepared, err
	}
	pub, err := update.ParsePublicKey(publicKey)
	if err != nil {
		return prepared, err
	}
	body, err := source.Manifest(ctx, false)
	if err != nil {
		return prepared, err
	}
	data, err := io.ReadAll(io.LimitReader(body, update.MaxManifestSize+1))
	body.Close()
	if err != nil {
		return prepared, err
	}
	if len(data) > update.MaxManifestSize {
		return prepared, errors.New("update manifest is too large")
	}
	if err := json.Unmarshal(data, &prepared.Manifest); err != nil {
		return prepared, err
	}
	m := prepared.Manifest
	if m.Version == "" || m.URL == "" || m.Signature == "" || m.Size <= 0 || m.Size > update.MaxArchiveSize {
		return prepared, errors.New("incomplete update manifest")
	}
	if err := os.Mkdir(destination, 0o700); err != nil {
		return prepared, err
	}
	prepared.Archive = filepath.Join(destination, "update.tar.gz")
	prepared.Contents = filepath.Join(destination, "contents")
	if err := download(ctx, source.Open, m.URL, m.Size, m.Signature, pub, prepared.Archive, progress); err != nil {
		return prepared, err
	}
	file, err := os.Open(prepared.Archive)
	if err != nil {
		return prepared, err
	}
	defer file.Close()
	if err := update.ExtractArchive(file, prepared.Contents); err != nil {
		return prepared, err
	}
	return prepared, ctx.Err()
}

// VerifyUpdateArchive lets an out-of-process installer verify a prepared archive
// again at the handoff boundary, with the same verifier as Install.
func VerifyUpdateArchive(path, publicKey string, manifest UpdateManifest) error {
	pub, err := update.ParsePublicKey(publicKey)
	if err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != manifest.Size || manifest.Size <= 0 || manifest.Size > update.MaxArchiveSize {
		return errors.New("update archive size or type changed")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	return update.Verify(pub, hash.Sum(nil), manifest.Signature)
}

// ExtractVerifiedUpdateArchive is the handoff counterpart of PrepareUpdate. It
// re-extracts authenticated bytes rather than trusting a mutable staging tree.
func ExtractVerifiedUpdateArchive(path, publicKey string, manifest UpdateManifest, destination string) error {
	if err := VerifyUpdateArchive(path, publicKey, manifest); err != nil {
		return err
	}
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		return errors.New("verified update destination already exists")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return update.ExtractArchive(file, destination)
}

// WriteUpdateArchive is the packaging counterpart of PrepareUpdate. Signing keys
// are provided by the build environment and are never written into the archive.
func WriteUpdateArchive(destination, directory string, entries []string, privateKey string) (size int64, signature, publicKey string, err error) {
	key, err := update.ParsePrivateKey(privateKey)
	if err != nil {
		return 0, "", "", err
	}
	file, err := os.Create(destination)
	if err != nil {
		return 0, "", "", err
	}
	hash := sha256.New()
	err = update.WriteArchive(io.MultiWriter(file, hash), directory, entries)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return 0, "", "", err
	}
	info, err := os.Stat(destination)
	if err != nil {
		return 0, "", "", err
	}
	if info.Size() > update.MaxArchiveSize {
		return 0, "", "", fmt.Errorf("update archive exceeds %d bytes", update.MaxArchiveSize)
	}
	return info.Size(), update.Sign(key, hash.Sum(nil)), base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey)), nil
}
