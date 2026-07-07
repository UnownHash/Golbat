// pogoshimgen emits typed accessor shims over hyperpb's protoreflect API.
//
// For every message reachable from the configured roots it generates a
// value-type wrapper with protoc-gen-go-style getters backed by cached
// FieldDescriptors:
//
//	type PokemonFortProto struct{ m protoreflect.Message }
//	func (x PokemonFortProto) GetFortId() string { ... }
//	func (x PokemonFortProto) GetRaidInfo() RaidInfoProto { ... } // zero shim when absent
//	func (x PokemonFortProto) GetPokestopDisplays() PokestopIncidentDisplayProtoList
//
// Semantics deliberately mirror generated open-API getters so call sites
// translate mechanically: zero shims chain like nil message getters, float32
// fields return float32 (preserving float32 arithmetic), enums return the
// descriptor package's generated enum types. Repeated fields return List
// wrappers with Len/At/All instead of slices — the one call-site difference.
// Repeated STRING fields get StringList instead of the untyped ScalarList:
// its At clones (strings.Clone) out of the arena, same as the singular
// string getter, so retaining an element never pins the whole parsed
// payload. Plain ScalarList.At returns a raw protoreflect.Value — calling
// .String()/.Bytes() on it is NOT safe to retain.
//
// Root message descriptors are resolved from golbat/pogo's registered
// descriptors via protoregistry, keyed by full proto name
// (POGOProtos.Rpc.<Name>), rather than a hardcoded map — golbat/pogo is
// imported solely for its init()-time descriptor registration.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	_ "golbat/pogo"
)

// rootDescriptor resolves a root message name to its descriptor via the
// global registry populated by importing golbat/pogo. Roots live in the
// POGOProtos.Rpc proto package.
func rootDescriptor(name string) (protoreflect.MessageDescriptor, error) {
	d, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName("POGOProtos.Rpc." + name))
	if err != nil {
		return nil, err
	}
	md, ok := d.(protoreflect.MessageDescriptor)
	if !ok {
		return nil, fmt.Errorf("%s is not a message", name)
	}
	return md, nil
}

// goCamelCase mirrors protoc-gen-go's field-name conversion.
func goCamelCase(s string) string {
	var b strings.Builder
	up := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '_':
			up = true
		case up && 'a' <= c && c <= 'z':
			b.WriteByte(c - 'a' + 'A')
			up = false
		default:
			b.WriteByte(c)
			up = false
		}
	}
	return b.String()
}

// pogoRpcPackage is the proto package every message/enum in golbat/pogo
// lives under; goName strips it before computing a Go identifier, mirroring
// protogen.newGoIdent's use of FullName relative to the file's package.
const pogoRpcPackage = "POGOProtos.Rpc."

// goName computes the protoc-gen-go Go identifier for a (possibly nested)
// message or enum descriptor. This is NOT simply the path components joined
// with underscores: protoc-gen-go derives it by taking the descriptor's
// FullName relative to the proto package and running it through
// internal/strs.GoCamelCase (see google.golang.org/protobuf/compiler/
// protogen.newGoIdent), which drops a "." immediately followed by a
// lowercase letter -- merging that word into the previous one instead of
// underscore-joining it. Most nested types (conventionally PascalCase, e.g.
// "Foo.CharacterDisplay") happen to produce the same result either way, but
// a nested type whose declared name starts lowercase (e.g. this proto set's
// "ContestPokemonAlignmentFocusProto.alignment", or
// "ButterflyCollectorRewardEncounterProto.request") does not:
// GoCamelCase yields "ContestPokemonAlignmentFocusProtoAlignment" (no
// underscore), which is the actual generated identifier -- a naive
// underscore-join would silently reference a type that doesn't exist.
func goName(d protoreflect.Descriptor) string {
	return goCamelCasePath(strings.TrimPrefix(string(d.FullName()), pogoRpcPackage))
}

// goCamelCasePath is a direct port of protoc-gen-go's internal/strs.GoCamelCase
// (an internal package, hence copied rather than imported), applied to a
// FullName's package-relative, dot-separated path instead of a single
// underscore-separated field name. See goName's doc comment for why this
// must match exactly rather than approximate it.
func goCamelCasePath(s string) string {
	var b []byte
	isLower := func(c byte) bool { return 'a' <= c && c <= 'z' }
	isDigit := func(c byte) bool { return '0' <= c && c <= '9' }
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '.' && i+1 < len(s) && isLower(s[i+1]):
			// Skip over '.' in ".{{lowercase}}": the next word merges
			// directly into the previous one instead of getting a "_".
		case c == '.':
			b = append(b, '_') // convert '.' to '_'
		case c == '_' && (i == 0 || s[i-1] == '.'):
			// Convert initial '_' to ensure we start with a capital letter.
			b = append(b, 'X')
		case c == '_' && i+1 < len(s) && isLower(s[i+1]):
			// Skip over '_' in "_{{lowercase}}".
		case isDigit(c):
			b = append(b, c)
		default:
			if isLower(c) {
				c -= 'a' - 'A'
			}
			b = append(b, c)
			for ; i+1 < len(s) && isLower(s[i+1]); i++ {
				b = append(b, s[i+1])
			}
		}
	}
	return string(b)
}

type gen struct {
	out       strings.Builder
	descPkg   string // import path of the package providing descriptors + enums
	descName  string // package identifier
	msgs      map[string]protoreflect.MessageDescriptor
	listElem  map[string]bool // message Go names needing a List wrapper
	usesBytes bool            // true once a BytesKind getter is emitted (needs "bytes" import)
}

func (g *gen) collect(md protoreflect.MessageDescriptor) {
	name := goName(md)
	if _, seen := g.msgs[name]; seen {
		return
	}
	if md.IsMapEntry() {
		return // map fields unsupported in v1; accessed via raw protoreflect if needed
	}
	g.msgs[name] = md
	fields := md.Fields()
	for i := 0; i < fields.Len(); i++ {
		f := fields.Get(i)
		if f.Kind() == protoreflect.MessageKind || f.Kind() == protoreflect.GroupKind {
			if f.IsMap() {
				continue
			}
			g.collect(f.Message())
		}
	}
}

func (g *gen) scalarGetter(recv, getter string, f protoreflect.FieldDescriptor) {
	get := fmt.Sprintf("x.m.Get(%s)", fdVar(recv, f))
	var typ, expr, zero string
	switch f.Kind() {
	case protoreflect.BoolKind:
		typ, expr, zero = "bool", get+".Bool()", "false"
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		typ, expr, zero = "int32", "int32("+get+".Int())", "0"
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		typ, expr, zero = "int64", get+".Int()", "0"
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		typ, expr, zero = "uint32", "uint32("+get+".Uint())", "0"
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		typ, expr, zero = "uint64", get+".Uint()", "0"
	case protoreflect.FloatKind:
		typ, expr, zero = "float32", "float32("+get+".Float())", "0"
	case protoreflect.DoubleKind:
		typ, expr, zero = "float64", get+".Float()", "0"
	case protoreflect.StringKind:
		// hyperpb's String() is an unsafe.String view directly into the
		// arena's per-parse payload copy (Shared.Src): retaining it keeps
		// that whole payload copy reachable. strings.Clone copies out a
		// right-sized, independent string so callers can retain the result
		// (cache keys, entity fields, tracker IDs) without pinning the
		// arena. Free on "" and a single tiny alloc on small strings --
		// negligible against the cost of a parse.
		typ, expr, zero = "string", "strings.Clone("+get+".String())", `""`
	case protoreflect.BytesKind:
		// Same reasoning as StringKind: clone out of the arena-backed view.
		typ, expr, zero = "[]byte", "bytes.Clone("+get+".Bytes())", "nil"
		g.usesBytes = true
	case protoreflect.EnumKind:
		et := g.descName + "." + goName(f.Enum())
		typ, expr, zero = et, et+"("+get+".Enum())", et+"(0)"
	default:
		return
	}
	fmt.Fprintf(&g.out, "func (x %s) %s() %s {\n\tif x.m == nil {\n\t\treturn %s\n\t}\n\treturn %s\n}\n\n",
		recv, getter, typ, zero, expr)
}

func fdVar(msgGoName string, f protoreflect.FieldDescriptor) string {
	return "fd_" + msgGoName + "_" + string(f.Name())
}

func (g *gen) message(name string, md protoreflect.MessageDescriptor) {
	fmt.Fprintf(&g.out, "// %s wraps a hyperpb/protoreflect %s message.\n", name, md.FullName())
	fmt.Fprintf(&g.out, "type %s struct{ m protoreflect.Message }\n\n", name)
	fmt.Fprintf(&g.out, "// As%s wraps a parsed message (e.g. from hyperpb). A nil or invalid\n", name)
	fmt.Fprintf(&g.out, "// message (protoreflect.Message.IsValid() == false, which is what a\n")
	fmt.Fprintf(&g.out, "// nil *pogo.%s .ProtoReflect() produces) yields the zero shim, so\n", name)
	fmt.Fprintf(&g.out, "// wrapping a typed-nil pointer is indistinguishable from wrapping nothing\n")
	fmt.Fprintf(&g.out, "// at all: IsZero() is true and every getter chains to its zero value.\n")
	fmt.Fprintf(&g.out, "func As%s(m protoreflect.Message) %s {\n\tif m == nil || !m.IsValid() {\n\t\treturn %s{}\n\t}\n\treturn %s{m}\n}\n\n",
		name, name, name, name)
	fmt.Fprintf(&g.out, "func (x %s) IsZero() bool { return x.m == nil }\n\n", name)

	fields := md.Fields()
	for i := 0; i < fields.Len(); i++ {
		f := fields.Get(i)
		if f.IsMap() {
			continue
		}
		getter := "Get" + goCamelCase(string(f.Name()))
		switch {
		case f.IsList():
			g.listGetter(name, getter, f)
		case f.Kind() == protoreflect.MessageKind || f.Kind() == protoreflect.GroupKind:
			sub := goName(f.Message())
			fmt.Fprintf(&g.out, "func (x %s) Has%s() bool {\n\treturn x.m != nil && x.m.Has(%s)\n}\n\n",
				name, goCamelCase(string(f.Name())), fdVar(name, f))
			// Single protoreflect call: Get on an absent message field
			// returns an invalid empty view without allocating (verified in
			// TestHyperGetAbsentMessageSemantics), so no separate Has.
			fmt.Fprintf(&g.out, "func (x %s) %s() %s {\n\tif x.m == nil {\n\t\treturn %s{}\n\t}\n\tif v := x.m.Get(%s).Message(); v.IsValid() {\n\t\treturn %s{v}\n\t}\n\treturn %s{}\n}\n\n",
				name, getter, sub, sub, fdVar(name, f), sub, sub)
		default:
			g.scalarGetter(name, getter, f)
		}
	}
}

func (g *gen) listGetter(recv, getter string, f protoreflect.FieldDescriptor) {
	if f.Kind() == protoreflect.MessageKind || f.Kind() == protoreflect.GroupKind {
		elem := goName(f.Message())
		g.listElem[elem] = true
		fmt.Fprintf(&g.out, "func (x %s) %s() %sList {\n\tif x.m == nil {\n\t\treturn %sList{}\n\t}\n\treturn %sList{x.m.Get(%s).List()}\n}\n\n",
			recv, getter, elem, elem, elem, fdVar(recv, f))
		return
	}
	if f.Kind() == protoreflect.StringKind {
		// Repeated string fields get their own clone-on-access wrapper
		// (StringList) instead of the untyped ScalarList: ScalarList.At
		// returns a raw protoreflect.Value, and calling .String() on it is
		// an unsafe arena view -- the same hazard scalarGetter's StringKind
		// case clones out of for singular fields. Two real call sites
		// (Gym/Pokestop image URLs, quest template IDs) once stored that
		// view straight into long-lived entities, pinning the whole
		// parse's payload copy for the entity's cache lifetime.
		//
		// NOTE: repeated bytes fields would have the identical hazard via
		// ScalarList.At(i).Bytes(), but none exist in the current root set
		// (verified by walking every reachable field) so there is no
		// BytesList counterpart yet -- add one the same way if a future
		// proto introduces a repeated bytes field.
		fmt.Fprintf(&g.out, "func (x %s) %s() StringList {\n\tif x.m == nil {\n\t\treturn StringList{}\n\t}\n\treturn StringList{x.m.Get(%s).List()}\n}\n\n",
			recv, getter, fdVar(recv, f))
		return
	}
	// Remaining scalar/enum lists share the untyped ScalarList support type.
	fmt.Fprintf(&g.out, "func (x %s) %s() ScalarList {\n\tif x.m == nil {\n\t\treturn ScalarList{}\n\t}\n\treturn ScalarList{x.m.Get(%s).List()}\n}\n\n",
		recv, getter, fdVar(recv, f))
}

func (g *gen) fdVars() {
	names := sortedKeys(g.msgs)
	fmt.Fprintf(&g.out, "var (\n")
	for _, name := range names {
		md := g.msgs[name]
		fields := md.Fields()
		for i := 0; i < fields.Len(); i++ {
			f := fields.Get(i)
			if f.IsMap() {
				continue
			}
			fmt.Fprintf(&g.out, "\t%s = mustFD((*%s.%s)(nil).ProtoReflect().Descriptor(), %q)\n",
				fdVar(name, f), g.descName, name, string(f.Name()))
		}
	}
	fmt.Fprintf(&g.out, ")\n\n")
}

func (g *gen) listTypes() {
	for _, elem := range sortedBoolKeys(g.listElem) {
		fmt.Fprintf(&g.out, "type %sList struct{ l protoreflect.List }\n\n", elem)
		fmt.Fprintf(&g.out, "func (l %sList) Len() int {\n\tif l.l == nil {\n\t\treturn 0\n\t}\n\treturn l.l.Len()\n}\n\n", elem)
		fmt.Fprintf(&g.out, "func (l %sList) At(i int) %s { return %s{l.l.Get(i).Message()} }\n\n", elem, elem, elem)
		fmt.Fprintf(&g.out, "func (l %sList) All() iter.Seq[%s] {\n\treturn func(yield func(%s) bool) {\n\t\tif l.l == nil {\n\t\t\treturn\n\t\t}\n\t\tfor i, n := 0, l.l.Len(); i < n; i++ {\n\t\t\tif !yield(%s{l.l.Get(i).Message()}) {\n\t\t\t\treturn\n\t\t\t}\n\t\t}\n\t}\n}\n\n", elem, elem, elem, elem)
	}
}

func sortedKeys(m map[string]protoreflect.MessageDescriptor) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedBoolKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// defaultRoots is the Wave 1-3 generator root set: the original GMO/encounter
// trio plus every Wave 3 method's data and (where present) request proto --
// see docs/superpowers/plans/2026-07-07-hyperpb-wave3.md's "New generator
// roots" list. Every message transitively reachable from these gets a shim.
const defaultRoots = "GetMapObjectsOutProto,EncounterOutProto,DiskEncounterOutProto," +
	"FortDetailsOutProto,GymGetInfoOutProto,FortSearchOutProto,GetMapFortsOutProto," +
	"GetRoutesOutProto,StartIncidentOutProto,OpenInvasionCombatSessionProto," +
	"OpenInvasionCombatSessionOutProto,BattleStateOutProto,GetContestDataProto," +
	"GetContestDataOutProto,GetPokemonSizeLeaderboardEntryProto," +
	"GetPokemonSizeLeaderboardEntryOutProto,GetStationedPokemonDetailsProto," +
	"GetStationedPokemonDetailsOutProto,ProcessTappableProto,ProcessTappableOutProto," +
	"GetEventRsvpsProto,GetEventRsvpsOutProto,GetEventRsvpCountOutProto," +
	"ProxyRequestProto,ProxyResponseProto,InternalGetFriendDetailsOutProto," +
	"InternalSearchPlayerOutProto,InternalSearchPlayerProto"

func main() {
	outPath := flag.String("out", "pogoshim/pogoshim.gen.go", "output file")
	pkg := flag.String("pkg", "pogoshim", "generated package name")
	descPkg := flag.String("descpkg", "golbat/pogo", "package providing descriptors and enum types")
	roots := flag.String("roots", defaultRoots, "root messages (CSV)")
	flag.Parse()

	g := &gen{
		descPkg:  *descPkg,
		msgs:     map[string]protoreflect.MessageDescriptor{},
		listElem: map[string]bool{},
	}
	parts := strings.Split(*descPkg, "/")
	g.descName = parts[len(parts)-1]

	for _, r := range strings.Split(*roots, ",") {
		r = strings.TrimSpace(r)
		md, err := rootDescriptor(r)
		if err != nil {
			fmt.Fprintf(os.Stderr, "resolve root %q: %v\n", r, err)
			os.Exit(1)
		}
		g.collect(md)
	}

	for _, name := range sortedKeys(g.msgs) {
		g.message(name, g.msgs[name])
	}
	g.listTypes()
	g.fdVars()

	// Header is built after the body so g.usesBytes (set while emitting
	// BytesKind getters) is known before we decide which stdlib imports the
	// generated file needs. "strings" is always required: StringList (like
	// ScalarList) is emitted unconditionally below and its At method always
	// calls strings.Clone.
	var header strings.Builder
	fmt.Fprintf(&header, "// Code generated by pogoshimgen. DO NOT EDIT.\n\npackage %s\n\n", *pkg)
	fmt.Fprintf(&header, "import (\n")
	stdImports := []string{"iter", "strings"}
	if g.usesBytes {
		stdImports = append(stdImports, "bytes")
	}
	sort.Strings(stdImports)
	for _, imp := range stdImports {
		fmt.Fprintf(&header, "\t%q\n", imp)
	}
	fmt.Fprintf(&header, "\n\t\"google.golang.org/protobuf/reflect/protoreflect\"\n\n\t%q\n)\n\n", g.descPkg)
	fmt.Fprintf(&header, "func mustFD(md protoreflect.MessageDescriptor, name string) protoreflect.FieldDescriptor {\n\tf := md.Fields().ByName(protoreflect.Name(name))\n\tif f == nil {\n\t\tpanic(\"pogoshim: missing field \" + name + \" on \" + string(md.FullName()))\n\t}\n\treturn f\n}\n\n")
	fmt.Fprintf(&header, "// ScalarList wraps a repeated scalar/enum field.\ntype ScalarList struct{ l protoreflect.List }\n\nfunc (l ScalarList) Len() int {\n\tif l.l == nil {\n\t\treturn 0\n\t}\n\treturn l.l.Len()\n}\n\nfunc (l ScalarList) At(i int) protoreflect.Value { return l.l.Get(i) }\n\n")
	fmt.Fprintf(&header, "// StringList wraps a repeated string field. Unlike ScalarList.At (which\n// returns a raw protoreflect.Value whose .String() is an unsafe view into\n// the parse's arena-backed payload copy), At clones its result so callers\n// can retain individual elements without pinning the whole payload.\ntype StringList struct{ l protoreflect.List }\n\nfunc (l StringList) Len() int {\n\tif l.l == nil {\n\t\treturn 0\n\t}\n\treturn l.l.Len()\n}\n\nfunc (l StringList) At(i int) string { return strings.Clone(l.l.Get(i).String()) }\n\n")

	if err := os.WriteFile(*outPath, []byte(header.String()+g.out.String()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("pogoshim: %d messages, %d list types -> %s\n", len(g.msgs), len(g.listElem), *outPath)
}
