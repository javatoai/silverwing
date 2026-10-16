package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
)

// DecodeStrict applies Kotlin-serialization-like strict object decoding. It rejects
// unknown keys, explicit nulls for non-nullable fields, missing required keys, and
// trailing JSON values. Defaults are supplied by the target's UnmarshalJSON method.
func DecodeStrict(data []byte, target any, required ...string) error {
	t := reflect.TypeOf(target)
	if t == nil || t.Kind() != reflect.Pointer {
		return fmt.Errorf("strict JSON target must be a pointer")
	}
	data, err := normalizeCommandProxyTargetsJSON(data, t.Elem())
	if err != nil {
		return err
	}
	if err := validateJSONShape(data, t.Elem()); err != nil {
		return err
	}
	if len(required) != 0 {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			return err
		}
		for _, name := range required {
			if _, ok := fields[name]; !ok {
				return fmt.Errorf("missing required field %q", name)
			}
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

// normalizeCommandProxyTargetsJSON mirrors Kotlin's Set serializer for the one
// persisted set-valued config field. It leaves malformed values for the strict
// shape/enum decoder to reject and preserves first occurrence order.
func normalizeCommandProxyTargetsJSON(data []byte, targetType reflect.Type) ([]byte, error) {
	if targetType.Kind() != reflect.Struct {
		return data, nil
	}
	fieldType := reflect.TypeOf([]CommandProxyTarget{})
	for i := 0; i < targetType.NumField(); i++ {
		field := targetType.Field(i)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name != "commandProxyTargets" || field.Type != fieldType {
			continue
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			return data, nil
		}
		raw, exists := fields[name]
		if !exists || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return data, nil
		}
		var values []CommandProxyTarget
		if err := json.Unmarshal(raw, &values); err != nil {
			return data, err
		}
		unique := NormalizeCommandProxyTargets(values)
		if len(unique) == len(values) {
			return data, nil
		}
		encoded, err := json.Marshal(unique)
		if err != nil {
			return data, err
		}
		fields[name] = encoded
		encodedObject, err := json.Marshal(fields)
		if err != nil {
			return data, err
		}
		return encodedObject, nil
	}
	return data, nil
}

func validateJSONShape(data []byte, t reflect.Type) error {
	nullable := false
	for t.Kind() == reflect.Pointer {
		nullable = true
		t = t.Elem()
	}
	trimmed := bytes.TrimSpace(data)
	if bytes.Equal(trimmed, []byte("null")) {
		if nullable {
			return nil
		}
		return fmt.Errorf("null is not valid for %s", t)
	}
	switch t.Kind() {
	case reflect.Struct:
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(trimmed, &fields); err != nil {
			return err
		}
		if fields == nil {
			return fmt.Errorf("expected JSON object for %s", t)
		}
		known := make(map[string]reflect.Type, t.NumField())
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if field.PkgPath != "" {
				continue
			}
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name == "-" {
				continue
			}
			if name == "" {
				name = field.Name
			}
			known[name] = field.Type
		}
		for name, raw := range fields {
			fieldType, ok := known[name]
			if !ok {
				return fmt.Errorf("unknown field %q in %s", name, t)
			}
			if err := validateJSONShape(raw, fieldType); err != nil {
				return fmt.Errorf("field %s: %w", name, err)
			}
		}
	case reflect.Slice, reflect.Array:
		var items []json.RawMessage
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return err
		}
		if items == nil {
			return fmt.Errorf("expected JSON array for %s", t)
		}
		for i, item := range items {
			if err := validateJSONShape(item, t.Elem()); err != nil {
				return fmt.Errorf("item %d: %w", i, err)
			}
		}
	case reflect.Map:
		if !bytes.HasPrefix(trimmed, []byte("{")) {
			return fmt.Errorf("expected JSON object for %s", t)
		}
	case reflect.String:
		var value string
		if err := json.Unmarshal(trimmed, &value); err != nil {
			return err
		}
	case reflect.Bool:
		var value bool
		if err := json.Unmarshal(trimmed, &value); err != nil {
			return err
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		var value json.Number
		decoder := json.NewDecoder(bytes.NewReader(trimmed))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			return err
		}
	default:
		// The typed decoder below handles the remaining scalar types.
	}
	return nil
}

func decodeEnum(data []byte, allowed ...string) (string, error) {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return "", err
	}
	for _, candidate := range allowed {
		if value == candidate {
			return value, nil
		}
	}
	return "", fmt.Errorf("unknown enum value %q", value)
}
