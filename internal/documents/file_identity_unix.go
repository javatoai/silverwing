//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package documents

import (
	"fmt"
	"os"
	"reflect"
)

func fileIdentityKey(path string) string {
	info, err := os.Stat(path)
	if err != nil || info.Sys() == nil {
		return ""
	}
	value := reflect.ValueOf(info.Sys())
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	dev, inode := structInteger(value, "Dev"), structInteger(value, "Ino")
	if dev == nil || inode == nil {
		return ""
	}
	return fmt.Sprintf("%x:%x", *dev, *inode)
}

func structInteger(value reflect.Value, name string) *uint64 {
	if value.Kind() != reflect.Struct {
		return nil
	}
	field := value.FieldByName(name)
	if !field.IsValid() {
		return nil
	}
	var number uint64
	switch field.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		number = field.Uint()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		number = uint64(field.Int())
	default:
		return nil
	}
	return &number
}
