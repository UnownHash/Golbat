//go:build !go_json

package jsonenc

import (
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// TestMarshalResolvesToStdlib proves jsonenc.Marshal is literally
// encoding/json.Marshal — not a same-named lookalike that could silently
// hide either codec — when built without -tags go_json, the configuration
// a bare `go test ./...` uses. It inspects the resolved function's own
// qualified name rather than comparing output, so it fails even if some
// future codec produced byte-identical results.
func TestMarshalResolvesToStdlib(t *testing.T) {
	name := runtime.FuncForPC(reflect.ValueOf(Marshal).Pointer()).Name()
	t.Logf("jsonenc.Marshal resolves to %s", name)
	if !strings.HasPrefix(name, "encoding/json.") {
		t.Fatalf("jsonenc.Marshal resolves to %q, want encoding/json.Marshal — the !go_json build stopped selecting stdlib", name)
	}
}
