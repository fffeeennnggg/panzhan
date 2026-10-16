// SPDX-License-Identifier: MIT
package json

import (
	stdjson "encoding/json"
	"io"
)

type Decoder = stdjson.Decoder
type Encoder = stdjson.Encoder
type RawMessage = stdjson.RawMessage
type Number = stdjson.Number

func NewDecoder(r io.Reader) *Decoder {
	decoder := stdjson.NewDecoder(r)
	decoder.UseNumber()
	return decoder
}
func NewEncoder(w io.Writer) *Encoder { return stdjson.NewEncoder(w) }

type standardAPI struct{}

var API standardAPI

func (standardAPI) NewDecoder(r io.Reader) *Decoder { return NewDecoder(r) }
func (standardAPI) NewEncoder(w io.Writer) *Encoder { return NewEncoder(w) }
func (standardAPI) Marshal(v interface{}) ([]byte, error) {
	return stdjson.Marshal(v)
}
func (standardAPI) Unmarshal(data []byte, v interface{}) error { return Unmarshal(data, v) }
