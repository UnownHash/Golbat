package main

import (
	b64 "encoding/base64"
	"sync"
)

// payloadBufPool recycles the decoded-payload byte buffers on the raw ingest
// hot path. The raw handler base64-decodes every packet's payload into a fresh
// []byte that lives only for the duration of one decode() call; pooling those
// buffers removes a per-packet allocation the size of the whole payload
// (15–300 KB for a GMO).
//
// Safety: this is sound only because standard protobuf-go Unmarshal COPIES the
// bytes it retains (strings/bytes fields are copied into the message), so a
// payload buffer is no longer referenced once decode()'s unmarshal returns.
// (This is exactly why the same pooling is unsafe under a zero-copy arena
// parser, whose message strings would alias the pooled buffer.)
var payloadBufPool = sync.Pool{New: func() any { b := make([]byte, 0, 4096); return &b }}

// decodeBase64Pooled base64-decodes s into a buffer borrowed from
// payloadBufPool and returns the decoded slice plus a release func. The caller
// MUST call release exactly once, and only after every reader of the returned
// slice is done (i.e. after decode() returns). On empty input or a decode
// error it returns (nil, no-op release) — matching the previous
// DecodeString-with-ignored-error behavior on the raw path.
func decodeBase64Pooled(s string) ([]byte, func()) {
	if s == "" {
		return nil, func() {}
	}
	bp := payloadBufPool.Get().(*[]byte)
	need := b64.StdEncoding.DecodedLen(len(s))
	if cap(*bp) < need {
		*bp = make([]byte, need)
	}
	// []byte(s) here does not escape the Decode call, so the compiler elides
	// its allocation — verified 0 allocs/op. The only allocation is the pooled
	// output buffer above, amortized away across packets.
	n, err := b64.StdEncoding.Decode((*bp)[:need], []byte(s))
	if err != nil {
		payloadBufPool.Put(bp)
		return nil, func() {}
	}
	return (*bp)[:n], func() { payloadBufPool.Put(bp) }
}
