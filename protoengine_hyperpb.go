//go:build amd64 || arm64

package main

import (
	"sync"
	"sync/atomic"

	"buf.build/go/hyperpb"
	"google.golang.org/protobuf/reflect/protoreflect"

	log "github.com/sirupsen/logrus"
	"golbat/config"
	"golbat/pogo"
)

const hyperpbSupported = true

const pgoWarmupSamples = 256

type hyperEngine struct {
	ty      atomic.Pointer[hyperpb.MessageType]
	arenas  sync.Pool // *hyperpb.Shared
	profile struct {
		mu      sync.Mutex
		pending *hyperpb.Profile
		seen    int
		done    atomic.Bool
	}
}

var hyperEngines = map[string]*hyperEngine{}

func initProtoEngines() {
	mds := map[string]protoreflect.MessageDescriptor{
		engMethodGmo:           (*pogo.GetMapObjectsOutProto)(nil).ProtoReflect().Descriptor(),
		engMethodEncounter:     (*pogo.EncounterOutProto)(nil).ProtoReflect().Descriptor(),
		engMethodDiskEncounter: (*pogo.DiskEncounterOutProto)(nil).ProtoReflect().Descriptor(),
	}
	for method, md := range mds {
		e := &hyperEngine{}
		e.ty.Store(hyperpb.CompileMessageDescriptor(md))
		if config.Config.ProtoEngine.Pgo {
			e.profile.pending = e.ty.Load().NewProfile()
		}
		e.arenas.New = func() any { return new(hyperpb.Shared) }
		hyperEngines[method] = e
		log.Infof("[PROTO_ENGINE] %s: hyperpb type compiled (engine=%s)", method, engineFor(method))
	}
}

// recordPGO feeds warmup packets into the profile; after pgoWarmupSamples it
// recompiles once and swaps the optimized type in.
func (e *hyperEngine) recordPGO(payload []byte) {
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
		log.Infof("[PROTO_ENGINE] PGO recompile complete after %d samples", e.profile.seen)
	}
}

func decodeHyperpb[T any](method string, payload []byte, wrap func(protoreflect.Message) T, process func(T) string) (string, error) {
	e := hyperEngines[method]
	if e == nil {
		return decodeStd(method, payload, wrap, process)
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
