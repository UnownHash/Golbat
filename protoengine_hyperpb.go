//go:build amd64 || arm64

package main

import (
	"sync"
	"sync/atomic"
	"time"

	"buf.build/go/hyperpb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	log "github.com/sirupsen/logrus"
	"golbat/config"
)

const hyperpbSupported = true

const pgoWarmupSamples = 256

// pgoWarmupDeadline bounds how long any handle keeps recording PGO profile
// samples, in addition to the pgoWarmupSamples completion trigger: recordPGO
// no-ops once this much time has elapsed since initProtoEngines() started
// the clock, so a rarely-hit method doesn't pay the double-parse warmup cost
// forever if it never reaches pgoWarmupSamples. A method that never
// completes warmup simply keeps its baseline (non-PGO) compiled type.
const pgoWarmupDeadline = 10 * time.Minute

// pgoWarmupDeadlineAt is the UnixNano instant recordPGO stops doing any
// warmup work, set once by startPgoWarmupClock (called from
// initProtoEngines). Atomic: read from every decode goroutine's hot path
// with no other synchronization, alongside the equally atomic per-handle
// profile.done flag.
var pgoWarmupDeadlineAt atomic.Int64

func startPgoWarmupClock() {
	pgoWarmupDeadlineAt.Store(time.Now().Add(pgoWarmupDeadline).UnixNano())
}

func pgoWarmupExpired() bool {
	deadline := pgoWarmupDeadlineAt.Load()
	return deadline != 0 && time.Now().UnixNano() > deadline
}

// protoEngineHandle is one generator root's compiled-type machinery: the
// hyperpb MessageType (initially the baseline compile, later swapped for a
// PGO-recompiled one), a pool of reusable arenas, PGO warmup bookkeeping,
// and the std-engine prototype constructor (decodeStd must stay a fast
// generated-struct unmarshal, never a dynamicpb fallback, so newStd is
// supplied at construction rather than derived from the descriptor).
type protoEngineHandle struct {
	method string
	newStd func() proto.Message

	ty     atomic.Pointer[hyperpb.MessageType]
	arenas sync.Pool // *hyperpb.Shared

	profile struct {
		mu      sync.Mutex
		pending *hyperpb.Profile
		seen    int
		done    atomic.Bool
	}
}

func newProtoEngine(method string, md protoreflect.MessageDescriptor, newStd func() proto.Message) *protoEngineHandle {
	e := &protoEngineHandle{method: method, newStd: newStd}
	e.ty.Store(hyperpb.CompileMessageDescriptor(md))
	if config.Config.ProtoEngine.Pgo {
		e.profile.pending = e.ty.Load().NewProfile()
	}
	e.arenas.New = func() any { return new(hyperpb.Shared) }
	log.Infof("[PROTO_ENGINE] %s: hyperpb type compiled for %s (engine=%s)", method, md.FullName(), engineFor(method))
	return e
}

// recordPGO feeds warmup packets into the profile; after pgoWarmupSamples it
// recompiles once and swaps the optimized type in. No-ops once
// pgoWarmupDeadline has elapsed since startup, even if pgoWarmupSamples was
// never reached.
func (e *protoEngineHandle) recordPGO(payload []byte) {
	if pgoWarmupExpired() {
		return
	}
	e.profile.mu.Lock()
	defer e.profile.mu.Unlock()
	if e.profile.done.Load() || e.profile.pending == nil {
		return
	}
	ty := e.ty.Load()
	shared := new(hyperpb.Shared)
	msg := shared.NewMessage(ty)
	_ = msg.Unmarshal(payload, hyperpb.WithRecordProfile(e.profile.pending, 1.0))
	shared.Free()
	e.profile.seen++
	if e.profile.seen >= pgoWarmupSamples {
		e.ty.Store(ty.Recompile(e.profile.pending))
		e.profile.pending = nil
		e.profile.done.Store(true)
		log.Infof("[PROTO_ENGINE] %s: PGO recompile complete after %d samples", e.method, e.profile.seen)
	}
}

func decodeHyperpb[T any](e *protoEngineHandle, payload []byte, wrap func(protoreflect.Message) T, process func(T) string) (string, error) {
	if e == nil {
		return decodeStd(e, payload, wrap, process)
	}
	if !e.profile.done.Load() && config.Config.ProtoEngine.Pgo {
		e.recordPGO(payload)
	}
	shared := e.arenas.Get().(*hyperpb.Shared)
	defer func() {
		shared.Free()
		e.arenas.Put(shared)
	}()
	msg := shared.NewMessage(e.ty.Load())
	if err := msg.Unmarshal(payload); err != nil {
		return "", err
	}
	return process(wrap(msg.ProtoReflect())), nil
}
