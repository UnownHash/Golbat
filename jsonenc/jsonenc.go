//go:build !go_json

// Package jsonenc lets a test's Marshal call track whichever JSON codec the
// current build actually selects, instead of always hardcoding
// encoding/json regardless of build tags.
//
// -tags go_json does NOT gate huma_api.go: every huma-registered route —
// which includes everything the golden tests in decoder/api_*_test.go and
// pokemon_seentype_test.go pin — is marshaled through goccy/go-json
// unconditionally (huma_api.go's newHumaConfig imports goccy directly, no
// build constraint). What the tag actually gates is gin's own internal
// JSON codec (github.com/gin-gonic/gin/codec/json), used by the raw
// c.JSON() calls outside huma, e.g. routes.go's PokemonScan and GetHealth.
// Both the Dockerfile (`CGO_ENABLED=0 go build -tags go_json`) and the
// Makefile default to building with the tag, so in every real build huma's
// responses (always goccy) and gin's internal codec (goccy, because the
// tag selected it) agree — but the tag is not what causes huma's choice,
// and a comment or test that implies it is would be describing a causal
// link that doesn't exist.
//
// A test that pins an exact wire-format string by calling
// encoding/json.Marshal directly never exercises goccy at all, tag or no
// tag — a hardcoded `encoding/json` import doesn't change based on how the
// test binary was built. Golden-JSON tests call jsonenc.Marshal instead:
// built under -tags go_json (as CI now does, matching the Dockerfile),
// Marshal is goccy, the codec every real build ships; built without the
// tag — a configuration nothing ships, but a real `go build .` outcome —
// Marshal is stdlib. Both are genuine codecs this binary can end up
// using; routing through jsonenc makes the test track whichever one the
// build in front of it actually selected, rather than silently pinning
// stdlib regardless.
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
