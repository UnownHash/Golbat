// protofields: type-precise analysis of which golbat/pogo message fields the
// Golbat codebase actually accesses, plus reflective escape hatches that would
// bypass static thinning. Drives schema thinning (keep only accessed fields).
//
// Usage: go run . <golbat-module-dir>   (default: ../..)
//
//	INCLUDE_TESTS=1  also count _test.go accesses
//	JSON=path        write the used-field set as JSON
package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/types"
	"os"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

const pogoPath = "golbat/pogo"

// generated packages whose OWN field access is not "Golbat logic reading a field".
var skipPkgs = map[string]bool{
	"golbat/pogo": true, "golbat/pogovt": true, "golbat/pogoshim": true, "golbat/pogothin": true,
}

// namedPogo returns the pogo MESSAGE named type of t (deref'd), or nil. Enums
// (underlying basic int) are excluded — they have no fields to thin and their
// .String() is a cheap name lookup, not message reflection.
func namedPogo(t types.Type) *types.Named {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	n, ok := t.(*types.Named)
	if !ok || n.Obj().Pkg() == nil || n.Obj().Pkg().Path() != pogoPath {
		return nil
	}
	if _, ok := n.Underlying().(*types.Struct); !ok {
		return nil // enum / non-message
	}
	return n
}

// fieldCount returns the number of protobuf field/getter entries on a pogo
// message struct (excludes the internal state/sizeCache/unknownFields).
func fieldCount(n *types.Named) int {
	st, ok := n.Underlying().(*types.Struct)
	if !ok {
		return 0
	}
	c := 0
	for i := 0; i < st.NumFields(); i++ {
		if strings.Contains(st.Tag(i), `protobuf:`) {
			c++
		}
	}
	return c
}

func main() {
	dir := "../.."
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	includeTests := os.Getenv("INCLUDE_TESTS") != ""

	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo |
			packages.NeedImports | packages.NeedDeps,
		Dir:   dir,
		Tests: includeTests,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		fmt.Fprintln(os.Stderr, "load:", err)
		os.Exit(1)
	}
	if packages.PrintErrors(pkgs) > 0 {
		fmt.Fprintln(os.Stderr, "package errors (results may be partial)")
	}

	used := map[string]map[string]bool{} // pogo type -> set of accessed Go member names (Get-stripped)
	fieldTotals := map[string]int{}      // pogo type -> total protobuf fields on the struct
	type escape struct{ Pos, Kind, Type string }
	var escapes []escape
	seenPkg := map[string]bool{}

	record := func(typ, member string) {
		if used[typ] == nil {
			used[typ] = map[string]bool{}
		}
		used[typ][member] = true
	}

	for _, pkg := range pkgs {
		if !strings.HasPrefix(pkg.PkgPath, "golbat") || skipPkgs[strings.TrimSuffix(pkg.PkgPath, ".test")] {
			continue
		}
		if seenPkg[pkg.PkgPath] {
			continue
		}
		seenPkg[pkg.PkgPath] = true
		for i, file := range pkg.Syntax {
			fname := ""
			if i < len(pkg.CompiledGoFiles) {
				fname = pkg.CompiledGoFiles[i]
			}
			if !includeTests && strings.HasSuffix(fname, "_test.go") {
				continue
			}
			ast.Inspect(file, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				tv, ok := pkg.TypesInfo.Types[sel.X]
				if !ok {
					return true
				}
				named := namedPogo(tv.Type)
				if named == nil {
					return true
				}
				member := sel.Sel.Name
				pos := pkg.Fset.Position(sel.Sel.Pos()).String()
				switch member {
				case "ProtoReflect":
					escapes = append(escapes, escape{pos, "ProtoReflect (full reflection)", named.Obj().Name()})
					return true
				case "String":
					// .String() on a MESSAGE reflects over all fields; flag it.
					escapes = append(escapes, escape{pos, "String (reflective text marshal)", named.Obj().Name()})
					return true
				case "Reset", "Descriptor":
					return true
				}
				record(named.Obj().Name(), strings.TrimPrefix(member, "Get"))
				fieldTotals[named.Obj().Name()] = fieldCount(named)
				return true
			})
			// proto.Marshal/Clone/Merge/Equal on a pogo value.
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				fsel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if x, ok := fsel.X.(*ast.Ident); !ok || x.Name != "proto" {
					return true
				}
				if fsel.Sel.Name != "Marshal" && fsel.Sel.Name != "Clone" && fsel.Sel.Name != "Merge" && fsel.Sel.Name != "Equal" {
					return true
				}
				for _, a := range call.Args {
					if tv, ok := pkg.TypesInfo.Types[a]; ok {
						if n := namedPogo(tv.Type); n != nil {
							pos := pkg.Fset.Position(call.Pos()).String()
							escapes = append(escapes, escape{pos, "proto." + fsel.Sel.Name, n.Obj().Name()})
						}
					}
				}
				return true
			})
		}
	}

	// --- report ---
	types_ := make([]string, 0, len(used))
	total := 0
	for t, m := range used {
		types_ = append(types_, t)
		total += len(m)
	}
	sort.Slice(types_, func(i, j int) bool { return len(used[types_[j]]) < len(used[types_[i]]) })

	accessedFields, totalFields := 0, 0
	for t := range used {
		accessedFields += len(used[t])
		totalFields += fieldTotals[t]
	}
	fmt.Printf("Golbat accesses %d distinct pogo message types, %d fields.\n", len(types_), total)
	fmt.Printf("Across those %d messages: %d of %d fields accessed — %d thinnable (%.0f%%).\n\n",
		len(types_), accessedFields, totalFields, totalFields-accessedFields,
		100*float64(totalFields-accessedFields)/float64(max(1, totalFields)))
	fmt.Println("Top accessed message types (accessed / total fields):")
	for i, t := range types_ {
		if i >= 15 {
			break
		}
		fmt.Printf("  %-38s %2d / %-2d fields\n", t, len(used[t]), fieldTotals[t])
	}

	fmt.Printf("\nReflective escape hatches (%d) — these bypass static field detection:\n", len(escapes))
	if len(escapes) == 0 {
		fmt.Println("  none — safe for static thinning")
	} else {
		// de-dup identical (kind,type) and show a few positions each
		byKind := map[string][]string{}
		for _, e := range escapes {
			k := e.Kind + " on " + e.Type
			byKind[k] = append(byKind[k], e.Pos)
		}
		keys := make([]string, 0, len(byKind))
		for k := range byKind {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			ps := byKind[k]
			ex := ps[0]
			if len(ps) > 1 {
				ex = fmt.Sprintf("%s (+%d more)", ps[0], len(ps)-1)
			}
			fmt.Printf("  %-52s %s\n", k, ex)
		}
	}

	if out := os.Getenv("JSON"); out != "" {
		m := map[string][]string{}
		for t, set := range used {
			for f := range set {
				m[t] = append(m[t], f)
			}
			sort.Strings(m[t])
		}
		b, _ := json.MarshalIndent(m, "", "  ")
		if err := os.WriteFile(out, b, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "json:", err)
		} else {
			fmt.Printf("\nused-field set written to %s\n", out)
		}
	}
}
