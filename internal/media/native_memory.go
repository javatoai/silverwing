package media

import "unsafe"

type nativeInput struct {
	bytes []byte
	ptr   unsafe.Pointer
	free  func() error
}

func (b *nativeInput) close() error {
	if b == nil || b.free == nil {
		return nil
	}
	err := b.free()
	b.free = nil
	b.ptr = nil
	b.bytes = nil
	return err
}
