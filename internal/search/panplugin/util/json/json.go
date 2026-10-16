// SPDX-License-Identifier: MIT
package json

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// Marshal encodes v as JSON.
func Marshal(v interface{}) ([]byte, error) { return json.Marshal(v) }

// Unmarshal decodes JSON numbers without converting them to float64.
func Unmarshal(data []byte, v interface{}) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(v); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}

// MarshalString 序列化对象到JSON字符串
func MarshalString(v interface{}) (string, error) {
	bytes, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

// UnmarshalString 反序列化JSON字符串到对象
func UnmarshalString(str string, v interface{}) error {
	return API.Unmarshal([]byte(str), v)
}

// MarshalIndent 序列化对象到格式化的JSON
func MarshalIndent(v interface{}, prefix, indent string) ([]byte, error) {
	// 使用sonic的格式化功能
	return json.MarshalIndent(v, prefix, indent)
}
