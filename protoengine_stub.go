//go:build !amd64 && !arm64

package main

import "google.golang.org/protobuf/reflect/protoreflect"

const hyperpbSupported = false

func initProtoEngines() {}

func decodeHyperpb[T any](method string, payload []byte, wrap func(protoreflect.Message) T, process func(T) string) (string, error) {
	return decodeStd(method, payload, wrap, process)
}
