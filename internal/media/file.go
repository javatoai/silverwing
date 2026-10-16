package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
)

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %v", ErrCancelled, err)
	}
	return nil
}

func readRegularFile(ctx context.Context, path string, limit int64, kind string) ([]byte, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if limit < 1 {
		return nil, fmt.Errorf("%s 预览大小上限无效", kind)
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("无法读取%s：%w", kind, err)
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s 必须是普通文件", kind)
	}
	if before.Size() > limit {
		return nil, fmt.Errorf("%s 超过预览大小限制", kind)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("无法打开%s：%w", kind, err)
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("无法读取%s信息：%w", kind, err)
	}
	if !after.Mode().IsRegular() || !os.SameFile(before, after) {
		return nil, fmt.Errorf("%s 必须是普通文件，且不能在打开期间被替换", kind)
	}

	// Keep the initial allocation modest even when a caller configures a very
	// large cap. The reader grows only as bytes are actually present on disk.
	initialCapacity := int(max64(min64(min64(after.Size(), limit), 1<<20), 0))
	buffer := make([]byte, 0, initialCapacity)
	chunk := make([]byte, 64*1024)
	for {
		if err := contextError(ctx); err != nil {
			return nil, err
		}
		remaining := limit - int64(len(buffer))
		if remaining < 0 {
			return nil, fmt.Errorf("%s 超过预览大小限制", kind)
		}
		readBuffer := chunk
		// Read one byte beyond the limit to distinguish an exactly-sized file
		// from an oversized file. This avoids limit+1 overflow for MaxInt64.
		if remaining < int64(len(readBuffer)) {
			readBuffer = readBuffer[:int(remaining)+1]
		}
		count, readErr := file.Read(readBuffer)
		buffer = append(buffer, readBuffer[:count]...)
		if int64(len(buffer)) > limit {
			return nil, fmt.Errorf("%s 超过预览大小限制", kind)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("读取%s失败：%w", kind, readErr)
		}
		if count == 0 {
			return nil, errors.New("读取文件时没有取得进展")
		}
	}
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	return buffer, nil
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
