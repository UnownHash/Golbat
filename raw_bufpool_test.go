package main

import (
	"bytes"
	b64 "encoding/base64"
	"strings"
	"sync"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"golbat/pogo"
)

func TestDecodeBase64Pooled(t *testing.T) {
	// Empty input -> nil, no-op release.
	if b, rel := decodeBase64Pooled(""); b != nil || rel == nil {
		t.Fatalf("empty: got %v", b)
	}

	// Round-trip: various sizes, including bigger-than-initial-pool-cap.
	for _, raw := range []string{"x", "hello world", strings.Repeat("abc", 5000)} {
		enc := b64.StdEncoding.EncodeToString([]byte(raw))
		got, release := decodeBase64Pooled(enc)
		if string(got) != raw {
			t.Fatalf("round-trip mismatch: got %q want %q", got, raw)
		}
		release()
	}

	// Reuse: a small decode after a big one must not read stale tail bytes.
	big := b64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xAB}, 8000))
	_, relBig := decodeBase64Pooled(big)
	relBig() // return the (grown) buffer to the pool
	smallRaw := []byte("tiny")
	small := b64.StdEncoding.EncodeToString(smallRaw)
	got, rel := decodeBase64Pooled(small)
	if !bytes.Equal(got, smallRaw) {
		t.Fatalf("reuse tail-bleed: got %q want %q", got, smallRaw)
	}
	rel()
}

func TestDecodeBase64PooledConcurrent(t *testing.T) {
	// Race-detector target: many goroutines borrowing/returning concurrently,
	// each verifying its own payload round-trips (no cross-contamination).
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			raw := strings.Repeat(string(rune('a'+g)), 100+g*37)
			enc := b64.StdEncoding.EncodeToString([]byte(raw))
			for i := 0; i < 200; i++ {
				got, release := decodeBase64Pooled(enc)
				if string(got) != raw {
					t.Errorf("g%d: mismatch", g)
					release()
					return
				}
				release()
			}
		}(g)
	}
	wg.Wait()
}

func TestUnmarshalClientProtoDiscardsUnknownKeepsKnown(t *testing.T) {
	// A real fort proto with a known field.
	src := &pogo.FortDetailsOutProto{Id: "FORT_1"}
	raw, err := proto.Marshal(src)
	if err != nil {
		t.Fatal(err)
	}
	// Append an unknown field (field 99999, varint) on the wire.
	withUnknown := protowire.AppendTag(append([]byte{}, raw...), 99999, protowire.VarintType)
	withUnknown = protowire.AppendVarint(withUnknown, 42)

	var got pogo.FortDetailsOutProto
	if err := unmarshalClientProto(withUnknown, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Known field survives.
	if got.GetId() != "FORT_1" {
		t.Fatalf("known field lost: %q", got.GetId())
	}
	// Unknown field discarded (not retained in the message).
	if n := len(got.ProtoReflect().GetUnknown()); n != 0 {
		t.Fatalf("expected unknown fields discarded, got %d bytes retained", n)
	}
}
