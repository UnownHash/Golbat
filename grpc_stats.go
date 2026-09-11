package main

import (
	"context"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc/stats"
)

// grpcRPCLogger is a stats.Handler that logs one DEBUG line per RPC when the
// RPC completes, carrying the timings the scan logs cannot see:
//
//   - total: the RPC as the server observed it, from request headers in to
//     the status trailer written to the socket;
//   - handler+marshal: the point at which the marshalled response was handed
//     to the transport (everything Golbat computes, plus proto.Marshal and
//     any compression);
//   - request/response sizes on the wire, and the compression negotiated,
//     so a gzip'd reply is visible when comparing against HTTP.
//
// The gap between total here and the caller's own stopwatch is transport,
// client-side decode, and connection setup — none of which the server can
// measure.
//
// Only GolbatApi methods are logged: raw ingest runs at hundreds of RPCs a
// second and is not what the line is for. The handler is installed only
// when the logger is at debug level (grpcRPCLoggingEnabled), so at the
// default level the server carries no per-RPC bookkeeping at all.
type grpcRPCLogger struct{}

// grpcRPCLoggingEnabled reports whether the per-RPC timing handler should
// be installed: only at debug level, decided once when the server is built.
func grpcRPCLoggingEnabled() bool {
	return log.IsLevelEnabled(log.DebugLevel)
}

type rpcTimingKey struct{}

type rpcTiming struct {
	method      string
	compression string
	reqWire     int
	respBytes   int // uncompressed payload
	respWire    int // compressed payload plus gRPC framing
	sentAt      time.Time
}

func (grpcRPCLogger) TagRPC(ctx context.Context, info *stats.RPCTagInfo) context.Context {
	if !strings.HasPrefix(info.FullMethodName, grpcApiServicePrefix) {
		return ctx // no timing attached: HandleRPC ignores this RPC
	}
	return context.WithValue(ctx, rpcTimingKey{}, &rpcTiming{method: info.FullMethodName})
}

func (grpcRPCLogger) HandleRPC(ctx context.Context, s stats.RPCStats) {
	t, _ := ctx.Value(rpcTimingKey{}).(*rpcTiming)
	if t == nil {
		return
	}
	switch e := s.(type) {
	case *stats.InHeader:
		t.compression = e.Compression
	case *stats.InPayload:
		t.reqWire += e.WireLength
	case *stats.OutPayload:
		t.respBytes += e.Length
		t.respWire += e.WireLength
		t.sentAt = e.SentTime
	case *stats.End:
		total := e.EndTime.Sub(e.BeginTime)
		var toSend time.Duration
		if !t.sentAt.IsZero() {
			toSend = t.sentAt.Sub(e.BeginTime)
		}
		log.Debugf("[GRPC_RPC] %s total=%s handler+marshal=%s req_wire=%d resp_bytes=%d resp_wire=%d compression=%q err=%v",
			t.method, total, toSend, t.reqWire, t.respBytes, t.respWire, t.compression, e.Error)
	}
}

func (grpcRPCLogger) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return ctx
}

func (grpcRPCLogger) HandleConn(context.Context, stats.ConnStats) {}
