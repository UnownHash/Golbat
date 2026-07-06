package main

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"golbat/config"
	"golbat/pogo"
)

// Engine method keys.
const (
	engMethodGmo           = "gmo"
	engMethodEncounter     = "encounter"
	engMethodDiskEncounter = "disk_encounter"
)

// stdPrototype returns a fresh pogo struct for the std engine per method.
func stdPrototype(method string) proto.Message {
	switch method {
	case engMethodGmo:
		return &pogo.GetMapObjectsOutProto{}
	case engMethodEncounter:
		return &pogo.EncounterOutProto{}
	case engMethodDiskEncounter:
		return &pogo.DiskEncounterOutProto{}
	}
	panic("unknown proto engine method " + method)
}

func engineFor(method string) string {
	if !hyperpbSupported {
		return "std"
	}
	var v string
	switch method {
	case engMethodGmo:
		v = config.Config.ProtoEngine.Gmo
	case engMethodEncounter:
		v = config.Config.ProtoEngine.Encounter
	case engMethodDiskEncounter:
		v = config.Config.ProtoEngine.DiskEncounter
	}
	if v == "hyperpb" {
		return "hyperpb"
	}
	return "std"
}

// decodeStd is the fallback path: protobuf-go unmarshal, wrapped into the
// same pogoshim surface the hyperpb path uses.
func decodeStd[T any](method string, payload []byte, wrap func(protoreflect.Message) T, process func(T) string) (string, error) {
	m := stdPrototype(method)
	if err := (proto.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(payload, m); err != nil {
		return "", err
	}
	return process(wrap(m.ProtoReflect())), nil
}

func decodeWithArena[T any](method string, payload []byte, wrap func(protoreflect.Message) T, process func(T) string) (string, error) {
	if engineFor(method) == "hyperpb" {
		return decodeHyperpb(method, payload, wrap, process)
	}
	return decodeStd(method, payload, wrap, process)
}
