package bench

import (
	"os"
	"sort"
	"testing"

	"google.golang.org/protobuf/proto"

	"protobench/corpus"
	"protobench/readers"
)

func unmarshalOpts() proto.UnmarshalOptions {
	if os.Getenv("PROTOBENCH_NOLAZY") != "" {
		return proto.UnmarshalOptions{NoLazyDecoding: true}
	}
	return proto.UnmarshalOptions{}
}

func corpusDir() string {
	if d := os.Getenv("PROTOBENCH_CORPUS"); d != "" {
		return d
	}
	return "../../capture"
}

func BenchmarkDecode(b *testing.B) {
	byMethod, err := corpus.Load(corpusDir())
	if err != nil {
		b.Skipf("no corpus at %s: %v (set PROTOBENCH_CORPUS)", corpusDir(), err)
	}
	o := unmarshalOpts()
	methods := make([]string, 0, len(byMethod))
	for m := range byMethod {
		if _, ok := readers.Registry[m]; ok {
			methods = append(methods, m)
		}
	}
	sort.Strings(methods)
	if len(methods) == 0 {
		b.Skip("corpus has no methods with readers yet")
	}
	for _, method := range methods {
		payloads := byMethod[method]
		reader := readers.Registry[method]
		var totalBytes int64
		for _, p := range payloads {
			totalBytes += int64(len(p.Data))
		}
		b.Run(method, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(totalBytes / int64(len(payloads)))
			for i := 0; i < b.N; i++ {
				p := payloads[i%len(payloads)]
				if err := reader(p.Data, o); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
	b.Log("sink:", readers.Sink.Load())
}
