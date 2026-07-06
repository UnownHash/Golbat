package main

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	log "github.com/sirupsen/logrus"

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

// invalidProtoEngineValues returns the [proto_engine] method keys (from
// engMethodGmo/Encounter/DiskEncounter) whose configured value is neither
// "std" nor "hyperpb", mapped to the offending value. engineFor() silently
// treats anything unrecognized as "std", so a typo (e.g. "hyperbp") would
// otherwise run on the std engine with no indication anything was
// misconfigured -- callers should warn once at startup for every entry here.
func invalidProtoEngineValues() map[string]string {
	configured := map[string]string{
		engMethodGmo:           config.Config.ProtoEngine.Gmo,
		engMethodEncounter:     config.Config.ProtoEngine.Encounter,
		engMethodDiskEncounter: config.Config.ProtoEngine.DiskEncounter,
	}
	bad := map[string]string{}
	for method, v := range configured {
		if v != "std" && v != "hyperpb" {
			bad[method] = v
		}
	}
	return bad
}

// warnInvalidProtoEngineValues logs a one-time warning for every
// [proto_engine] value that isn't "std" or "hyperpb". Call once at startup
// after config load, regardless of platform (hyperpb support is
// architecture-gated, but a config typo is worth flagging everywhere).
func warnInvalidProtoEngineValues() {
	for method, v := range invalidProtoEngineValues() {
		log.Warnf("[PROTO_ENGINE] proto_engine.%s = %q is neither \"std\" nor \"hyperpb\" -- falling back to std", method, v)
	}
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
