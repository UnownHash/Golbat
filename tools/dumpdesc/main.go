// dumpdesc writes the FileDescriptorSet for golbat/pogo to stdout, derived from
// the COMPILED pogo package (pogo.File_vbase_proto) rather than the .proto
// source. This is the exact schema the binary uses, so the thinned descriptor
// produced from it is guaranteed a faithful subset — and the thinning pipeline
// needs no access to the licensed vbase.proto text.
//
// Build it under the default (full) build so it sees the full schema:
//
//	go run ./tools/dumpdesc > full.desc
package main

import (
	"os"

	"golbat/pogo"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

func main() {
	set := &descriptorpb.FileDescriptorSet{}
	seen := map[string]bool{}
	var add func(fd protoreflect.FileDescriptor)
	add = func(fd protoreflect.FileDescriptor) {
		if seen[fd.Path()] {
			return
		}
		seen[fd.Path()] = true
		// Dependencies must precede the files that import them in the set.
		imports := fd.Imports()
		for i := 0; i < imports.Len(); i++ {
			add(imports.Get(i).FileDescriptor)
		}
		set.File = append(set.File, protodesc.ToFileDescriptorProto(fd))
	}
	add(pogo.File_vbase_proto)

	b, err := proto.Marshal(set)
	if err != nil {
		panic(err)
	}
	if _, err := os.Stdout.Write(b); err != nil {
		panic(err)
	}
}
