// prototrim thins a protobuf descriptor set to only the fields Golbat accesses
// (per a protofields JSON), preserving field numbers so removed fields become
// skipped-unknown on the wire. Operates on the structured FileDescriptorSet
// (not .proto text), so nesting and oneofs are handled robustly.
//
// Usage: prototrim <full.desc> <used_fields.json> <thin.desc>
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

// GoCamelCase is protoc-gen-go's exact field-name→Go-name conversion
// (google.golang.org/protobuf/internal/strs), so our used-set match is precise.
func GoCamelCase(s string) string {
	isLower := func(c byte) bool { return 'a' <= c && c <= 'z' }
	isDigit := func(c byte) bool { return '0' <= c && c <= '9' }
	var b []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '.' && i+1 < len(s) && isLower(s[i+1]):
		case c == '.':
			b = append(b, '_')
		case c == '_' && (i == 0 || s[i-1] == '.'):
			b = append(b, 'X')
		case c == '_' && i+1 < len(s) && isLower(s[i+1]):
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

var stats struct{ keptFields, removedFields, emptiedMsgs int }

// trimMessage keeps only fields whose Go name is in used[goName]; a message with
// no used entry is thinned to empty. Recurses into nested messages. Field
// numbers and oneof decls are preserved.
func trimMessage(m *descriptorpb.DescriptorProto, goName string, used map[string]map[string]bool) {
	keep := used[goName] // nil => keep nothing

	// A real oneof accessed via its wrapper field (type switch on `x.Type`) or
	// as a whole keeps ALL its members — the code references the per-member
	// wrapper types (*Msg_Member), which protoc-gen-go only emits if the member
	// field survives. Detect by the oneof's Go field name being in the used
	// set. (proto3 `optional` synthetic oneofs are named "_field" -> "XField",
	// never in the set, so they don't trigger this; their field is kept by its
	// own name.)
	keptOneof := map[int32]bool{}
	for i, od := range m.OneofDecl {
		if keep[GoCamelCase(od.GetName())] {
			keptOneof[int32(i)] = true
		}
	}
	var fields []*descriptorpb.FieldDescriptorProto
	for _, f := range m.Field {
		if keep[GoCamelCase(f.GetName())] || (f.OneofIndex != nil && keptOneof[f.GetOneofIndex()]) {
			fields = append(fields, f)
			stats.keptFields++
		} else {
			stats.removedFields++
		}
	}
	if len(fields) == 0 && len(m.Field) > 0 {
		stats.emptiedMsgs++
	}

	// Drop oneof decls that lost all their fields (protoc rejects empty
	// oneofs), and remap the surviving fields' oneof_index. This also covers
	// proto3 `optional` fields, which are modeled as synthetic single-field
	// oneofs — a kept optional field keeps its synthetic oneof automatically.
	if len(m.OneofDecl) > 0 {
		usedOneof := map[int32]bool{}
		for _, f := range fields {
			if f.OneofIndex != nil {
				usedOneof[f.GetOneofIndex()] = true
			}
		}
		var newDecls []*descriptorpb.OneofDescriptorProto
		remap := map[int32]int32{}
		for i, od := range m.OneofDecl {
			if usedOneof[int32(i)] {
				remap[int32(i)] = int32(len(newDecls))
				newDecls = append(newDecls, od)
			}
		}
		for _, f := range fields {
			if f.OneofIndex != nil {
				ni := remap[f.GetOneofIndex()]
				f.OneofIndex = &ni
			}
		}
		m.OneofDecl = newDecls
	}

	m.Field = fields
	for _, n := range m.NestedType {
		if n.GetOptions().GetMapEntry() {
			continue // synthetic map-entry messages: leave intact
		}
		trimMessage(n, goName+"_"+n.GetName(), used)
	}
}

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: prototrim <full.desc> <used.json> <thin.desc>")
		os.Exit(2)
	}
	descBytes, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var fds descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(descBytes, &fds); err != nil {
		fmt.Fprintln(os.Stderr, "descriptor:", err)
		os.Exit(1)
	}
	raw, err := os.ReadFile(os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var usedList map[string][]string
	if err := json.Unmarshal(raw, &usedList); err != nil {
		fmt.Fprintln(os.Stderr, "json:", err)
		os.Exit(1)
	}
	used := map[string]map[string]bool{}
	for t, fs := range usedList {
		used[t] = map[string]bool{}
		for _, f := range fs {
			used[t][f] = true
		}
	}

	for _, f := range fds.File {
		for _, m := range f.MessageType {
			trimMessage(m, m.GetName(), used)
		}
	}

	out, err := proto.Marshal(&fds)
	if err != nil {
		fmt.Fprintln(os.Stderr, "marshal:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(os.Args[3], out, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("thinned descriptor: kept %d fields, removed %d (%.0f%%), emptied %d messages -> %s\n",
		stats.keptFields, stats.removedFields,
		100*float64(stats.removedFields)/float64(stats.keptFields+stats.removedFields), stats.emptiedMsgs, os.Args[3])
}
