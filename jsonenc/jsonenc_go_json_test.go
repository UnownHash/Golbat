//go:build go_json

package jsonenc

import (
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// TestMarshalResolvesToGoccy proves jsonenc.Marshal is literally
// github.com/goccy/go-json.Marshal — not stdlib silently answering both tag
// configurations — when built with -tags go_json, the tag CI's tagged test
// run and the production Dockerfile both use. It inspects the resolved
// function's own qualified name rather than comparing output, which is the
// check that would have caught an indirection that "resolves to stdlib in
// both cases": that failure mode would still produce byte-identical golden
// output (stdlib compared against itself) while claiming to exercise goccy.
func TestMarshalResolvesToGoccy(t *testing.T) {
	name := runtime.FuncForPC(reflect.ValueOf(Marshal).Pointer()).Name()
	t.Logf("jsonenc.Marshal resolves to %s", name)
	if !strings.Contains(name, "goccy/go-json.") {
		t.Fatalf("jsonenc.Marshal resolves to %q, want github.com/goccy/go-json.Marshal — the go_json build tag stopped selecting goccy", name)
	}
}
