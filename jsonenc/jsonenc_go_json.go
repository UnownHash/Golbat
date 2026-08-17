//go:build go_json

package jsonenc

import gojson "github.com/goccy/go-json"

// Marshal *is* goccy/go-json's Marshal — the same package huma_api.go uses
// to serve every API response — assigned directly (not wrapped) so
// reflection on the func value (see jsonenc_go_json_test.go) reports it as
// github.com/goccy/go-json.Marshal. Selected when the binary is built with
// -tags go_json, matching the Dockerfile's production build.
var Marshal = gojson.Marshal
