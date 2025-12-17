package codec

import (
	"io"

	"encoding/json/jsontext"
	"encoding/json/v2"

	gojson "github.com/goccy/go-json"
)

// UseGoJSON controls whether to use go-json for marshal/unmarshal operations.
// Default is false (use jsonv2).
var UseGoJSON bool

// JSONMarshal encodes v into JSON.
// Uses go-json if UseGoJSON is true, otherwise uses jsonv2.
func JSONMarshal(v any) ([]byte, error) {
	if UseGoJSON {
		return gojson.Marshal(v)
	}
	return json.Marshal(v)
}

// JSONMarshalIndent encodes v into indented JSON.
// Uses go-json if UseGoJSON is true, otherwise uses jsonv2.
func JSONMarshalIndent(v any, prefix, indent string) ([]byte, error) {
	if UseGoJSON {
		return gojson.MarshalIndent(v, prefix, indent)
	}
	return json.Marshal(v, jsontext.WithIndent(indent))
}

// JSONUnmarshal decodes JSON data into v.
// Uses go-json if UseGoJSON is true, otherwise uses jsonv2.
func JSONUnmarshal(data []byte, v any) error {
	if UseGoJSON {
		return gojson.Unmarshal(data, v)
	}
	return json.Unmarshal(data, v)
}

// JSONUnmarshalRead decodes JSON from reader into v.
// Uses go-json if UseGoJSON is true, otherwise uses jsonv2.
func JSONUnmarshalRead(r io.Reader, v any) error {
	if UseGoJSON {
		return gojson.NewDecoder(r).Decode(v)
	}
	return json.UnmarshalRead(r, v)
}

// JSONMarshalWrite encodes v as JSON directly to an io.Writer.
// This is more efficient than JSONMarshal when writing to a stream
// as it avoids intermediate []byte allocation.
// Uses go-json if UseGoJSON is true, otherwise uses jsonv2.
func JSONMarshalWrite(w io.Writer, v any) error {
	if UseGoJSON {
		return gojson.NewEncoder(w).Encode(v)
	}
	return json.MarshalWrite(w, v)
}
