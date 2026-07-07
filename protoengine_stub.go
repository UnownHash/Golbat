//go:build !amd64 && !arm64

package main

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const hyperpbSupported = false

// protoEngineHandle carries only what the std fallback path needs on
// unsupported platforms: the std-engine prototype constructor. method is
// kept for parity/logging with the hyperpb build's handle, even though
// nothing here reads it.
type protoEngineHandle struct {
	method string
	newStd func() proto.Message
}

func newProtoEngine(method string, _ protoreflect.MessageDescriptor, newStd func() proto.Message) *protoEngineHandle {
	return &protoEngineHandle{method: method, newStd: newStd}
}

// startPgoWarmupClock is a no-op here: there is no hyperpb PGO warmup to
// bound on an unsupported platform.
func startPgoWarmupClock() {}

func decodeHyperpb[T any](eng *protoEngineHandle, payload []byte, wrap func(protoreflect.Message) T, process func(T) string) (string, error) {
	return decodeStd(eng, payload, wrap, process)
}
