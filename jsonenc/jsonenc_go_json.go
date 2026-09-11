//go:build go_json

package jsonenc

import gojson "github.com/goccy/go-json"

// Marshal *is* goccy/go-json's Marshal, assigned directly (not wrapped) so
// reflection on the func value (see jsonenc_go_json_test.go) reports it as
// github.com/goccy/go-json.Marshal. Selected when the binary is built with
// -tags go_json, the same tag the Dockerfile's production build and gin's
// own internal codec both key off — see jsonenc.go's package doc for what
// that tag does and doesn't gate (it doesn't gate huma_api.go, which uses
// goccy unconditionally regardless).
var Marshal = gojson.Marshal
