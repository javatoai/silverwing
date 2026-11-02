package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

const configReadChunkSize = 32 * 1024

// A synchronous filesystem open/read may wait for the OS. Cancellation is
// checked again as soon as it returns; no detached read continues afterwards.
func openConfigurationContext(ctx context.Context, path string) (*os.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

func readConfigurationFileContext(ctx context.Context, path string) ([]byte, error) {
	file, err := openConfigurationContext(ctx, path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(configContextReader{ctx: ctx, reader: file})
}

// ConfigurationArchiveFingerprintContext hashes only the validated preview's
// source archive, without allocating a second whole-archive byte slice.
func ConfigurationArchiveFingerprintContext(ctx context.Context, path string) (string, error) {
	file, err := openConfigurationContext(ctx, path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, configContextReader{ctx: ctx, reader: file}); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// Parse and hash a private copy of the same byte stream. Reopening the source
// separately for validation and fingerprinting can bind preview A to archive B.
// A temporary file keeps ZIP random access without buffering the entire archive.
func readArchiveSnapshotContext(ctx context.Context, path string) (decodedConfig, string, error) {
	if err := ctx.Err(); err != nil {
		return decodedConfig{}, "", err
	}
	info, err := os.Stat(path)
	if cancelled := ctx.Err(); cancelled != nil {
		return decodedConfig{}, "", cancelled
	}
	if err != nil {
		return decodedConfig{}, "", fmt.Errorf("配置 ZIP 不存在或无法读取：%w", err)
	}
	if !info.Mode().IsRegular() {
		return decodedConfig{}, "", fmt.Errorf("配置 ZIP 不是普通文件：%s", path)
	}
	file, err := openConfigurationContext(ctx, path)
	if err != nil {
		return decodedConfig{}, "", err
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil {
		return decodedConfig{}, "", err
	}
	if !info.Mode().IsRegular() {
		return decodedConfig{}, "", fmt.Errorf("配置 ZIP 不是普通文件：%s", path)
	}
	temporary, err := os.CreateTemp("", "silverwing-config-snapshot-*.zip")
	if err != nil {
		return decodedConfig{}, "", err
	}
	defer os.Remove(temporary.Name())
	defer temporary.Close()
	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(temporary, hash), configContextReader{ctx: ctx, reader: file}); err != nil {
		return decodedConfig{}, "", err
	}
	if err := temporary.Close(); err != nil {
		return decodedConfig{}, "", err
	}
	decoded, err := readArchiveContext(ctx, temporary.Name())
	if err != nil {
		return decodedConfig{}, "", err
	}
	return decoded, hex.EncodeToString(hash.Sum(nil)), nil
}

type configContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader configContextReader) Read(p []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := reader.reader.Read(p[:min(len(p), configReadChunkSize)])
	if cancelled := reader.ctx.Err(); cancelled != nil {
		return n, cancelled
	}
	return n, err
}

type configContextReaderAt struct {
	ctx    context.Context
	reader io.ReaderAt
}

func (reader configContextReaderAt) ReadAt(p []byte, offset int64) (int, error) {
	n := 0
	for n < len(p) {
		if err := reader.ctx.Err(); err != nil {
			return n, err
		}
		end := min(n+configReadChunkSize, len(p))
		count, err := reader.reader.ReadAt(p[n:end], offset+int64(n))
		n += count
		if cancelled := reader.ctx.Err(); cancelled != nil {
			return n, cancelled
		}
		if err != nil {
			return n, err
		}
	}
	return n, nil
}
