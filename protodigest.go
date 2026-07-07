package main

import (
	"hash"
	"sort"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// digestMessageGeneric is the shadow-verification fallback for any client-
// proto method without a hand-written digest function (protoshadow.go keeps
// hand-written digests for gmo/encounter/disk_encounter, the three
// highest-volume methods, where the extra control over exactly which fields
// matter is worth the maintenance cost). It walks m's DESCRIPTOR fields in
// field-number order -- Message.Range's iteration order is unspecified by
// the protoreflect API, so it must never be used for a digest that has to
// agree between two independent engines -- and folds every field:
//
//   - Has-bit first (protoshadow.go's foldBool), then the value, so a field
//     explicitly set to its zero value digests differently from the same
//     field left unset wherever the proto/kind combination makes that
//     distinguishable (singular message fields, proto2 scalars, proto3
//     `optional` scalars); for plain proto3 scalars Has already reduces to
//     "non-zero", so this is a harmless no-op fold rather than a special
//     case.
//   - Singular message fields recurse (an absent field's Message() view is
//     invalid and immediately returns, having already folded its Has bit).
//   - List fields fold their length, then each element in order.
//   - Map fields fold their length, then each entry in ascending
//     string-key order (Range's map iteration order is random, same
//     concern as Message.Range above).
//   - Scalars/enums reuse protoshadow.go's existing fold primitives
//     (float32 via Float32bits, exactly as the hand-written digests do).
func digestMessageGeneric(h hash.Hash64, m protoreflect.Message) {
	if m == nil || !m.IsValid() {
		return
	}
	fields := m.Descriptor().Fields()
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		tag := int(fd.Number())
		foldBool(h, tag, m.Has(fd))
		v := m.Get(fd)
		switch {
		case fd.IsMap():
			digestMapFieldGeneric(h, tag, v.Map(), fd.MapValue())
		case fd.IsList():
			digestListFieldGeneric(h, tag, v.List(), fd)
		case fd.Kind() == protoreflect.MessageKind || fd.Kind() == protoreflect.GroupKind:
			digestMessageGeneric(h, v.Message())
		default:
			digestScalarValueGeneric(h, tag, fd, v)
		}
	}
}

// digestListFieldGeneric folds a repeated field's length, then each element
// in order (index order -- never re-sorted, since list order is itself
// wire-observable data both engines must agree on).
func digestListFieldGeneric(h hash.Hash64, tag int, list protoreflect.List, fd protoreflect.FieldDescriptor) {
	foldLen(h, tag, list.Len())
	isMsg := fd.Kind() == protoreflect.MessageKind || fd.Kind() == protoreflect.GroupKind
	for i := 0; i < list.Len(); i++ {
		ev := list.Get(i)
		if isMsg {
			digestMessageGeneric(h, ev.Message())
		} else {
			digestScalarValueGeneric(h, tag, fd, ev)
		}
	}
}

// digestMapFieldGeneric folds a map field's length, then each entry in
// ascending string-key order (protoreflect.Map.Range order is unspecified,
// same concern as Message.Range) -- MapKey.String() gives a canonical,
// kind-independent string form for any of protobuf's map key kinds
// (integer, bool, or string), which is all that's needed for a stable sort.
func digestMapFieldGeneric(h hash.Hash64, tag int, m protoreflect.Map, valueFd protoreflect.FieldDescriptor) {
	foldLen(h, tag, m.Len())
	if m.Len() == 0 {
		return
	}
	keys := make([]protoreflect.MapKey, 0, m.Len())
	m.Range(func(k protoreflect.MapKey, _ protoreflect.Value) bool {
		keys = append(keys, k)
		return true
	})
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })

	isMsg := valueFd.Kind() == protoreflect.MessageKind || valueFd.Kind() == protoreflect.GroupKind
	for _, k := range keys {
		foldStr(h, tag, k.String())
		v := m.Get(k)
		if isMsg {
			digestMessageGeneric(h, v.Message())
		} else {
			digestScalarValueGeneric(h, tag, valueFd, v)
		}
	}
}

// digestScalarValueGeneric folds a single scalar/enum protoreflect.Value
// using protoshadow.go's existing fold primitives, keyed by the
// descriptor's Kind (float32 fields still fold via Float32bits, not
// Float64bits, even though protoreflect.Value stores them widened to
// float64 -- narrowing back to float32 first, exactly like the hand-written
// digests and the generator's own scalarGetter).
func digestScalarValueGeneric(h hash.Hash64, tag int, fd protoreflect.FieldDescriptor, v protoreflect.Value) {
	switch fd.Kind() {
	case protoreflect.BoolKind:
		foldBool(h, tag, v.Bool())
	case protoreflect.EnumKind:
		// protoreflect.Value for an enum field wraps a protoreflect.EnumNumber,
		// not a plain int32/int64 -- Value.Int() panics on it (verified: it
		// type-switches on the concrete Go type backing the Value, and
		// EnumNumber isn't one of the cases Int() accepts). Value.Enum() is
		// the accessor built for this kind specifically.
		foldI64(h, tag, int64(v.Enum()))
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind,
		protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		foldI64(h, tag, v.Int())
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind,
		protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		foldU64(h, tag, v.Uint())
	case protoreflect.FloatKind:
		foldF32(h, tag, float32(v.Float()))
	case protoreflect.DoubleKind:
		foldF64(h, tag, v.Float())
	case protoreflect.StringKind:
		foldStr(h, tag, v.String())
	case protoreflect.BytesKind:
		foldBytes(h, tag, v.Bytes())
	}
}
