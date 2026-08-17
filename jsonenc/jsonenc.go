//go:build !go_json

// Package jsonenc indirects JSON marshaling behind the same build tag that
// picks gin's own internal JSON codec (see gin's codec/json package) and
// that the production Dockerfile builds with (`go build -tags go_json`).
//
// huma_api.go serves every API response through goccy/go-json directly, so
// goccy is what actually ships regardless of this tag. But a test that pins
// an exact wire-format string by calling encoding/json.Marshal itself never
// exercises goccy — building the test binary with -tags go_json does not
// change which package a hardcoded `encoding/json` import resolves to.
// Golden-JSON tests call jsonenc.Marshal instead, so building and running
// them under -tags go_json (as CI now does) actually round-trips through
// goccy, and running them without the tag covers stdlib — both are real
// codecs the binary can end up using (goccy for API responses always;
// stdlib for anything, like webhooks/webhook.go, that imports
// encoding/json directly and isn't touched by this tag at all).
//
// Without the tag (this file), Marshal *is* stdlib's encoding/json.Marshal
// — assigned directly, not wrapped, so reflection on the func value (see
// jsonenc_test.go) reports it as encoding/json.Marshal rather than as a
// same-named jsonenc function that could be hiding either codec.
package jsonenc

import "encoding/json"

// Marshal is encoding/json.Marshal, selected when the binary is built
// without -tags go_json.
var Marshal = json.Marshal
