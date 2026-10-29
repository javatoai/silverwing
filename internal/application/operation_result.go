package application

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
)

// OperationResult is the typed in-process result. ActionResult remains the JSON
// compatibility envelope at the edge of the application.
type OperationResult[T any] struct {
	Message  string
	Text     string
	Path     string
	Data     T
	omitData bool
}

func (result OperationResult[T]) ToActionResult() ActionResult {
	var data any = result.Data
	if _, unit := data.(struct{}); unit {
		data = nil
	} else if value := reflect.ValueOf(data); value.IsValid() && value.Kind() == reflect.Pointer {
		if value.IsNil() {
			data = nil
		} else {
			data = value.Elem().Interface()
		}
	}
	if result.Message == "" && result.Text == "" && result.Path == "" && reflect.ValueOf(result.Data).IsValid() && reflect.ValueOf(result.Data).IsZero() {
		data = nil
	}
	if result.omitData {
		data = nil
	}
	return ActionResult{Message: result.Message, Text: result.Text, Path: result.Path, Data: data}
}

// ResultData uses native Go values first. The JSON fallback is restricted to
// this compatibility boundary so UI consumers do not each implement a codec.
// A missing or malformed result is an error, never a successful zero value.
func ResultData[T any](result ActionResult) (T, error) {
	if data, ok := result.Data.(T); ok {
		if value := reflect.ValueOf(data); value.IsValid() && value.Kind() == reflect.Pointer && value.IsNil() {
			var zero T
			return zero, errors.New("操作结果缺少数据")
		}
		return data, nil
	}
	if data, ok := result.Data.(*T); ok && data != nil {
		return *data, nil
	}
	var data T
	if result.Data == nil {
		return data, errors.New("操作结果缺少数据")
	}
	encoded, err := json.Marshal(result.Data)
	if err != nil {
		return data, fmt.Errorf("编码操作结果失败：%w", err)
	}
	if string(encoded) == "null" {
		return data, errors.New("操作结果缺少数据")
	}
	if err := json.Unmarshal(encoded, &data); err != nil {
		return data, fmt.Errorf("读取操作结果格式失败：%w", err)
	}
	return data, nil
}

func operationResultFromAction[T any](result ActionResult) (OperationResult[T], error) {
	value, err := ResultData[T](result)
	return OperationResult[T]{Message: result.Message, Text: result.Text, Path: result.Path, Data: value, omitData: result.Data == nil}, err
}
