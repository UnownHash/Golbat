package pogoshim

// Hand-maintained accessors for the one construct cmd/pogoshimgen does not
// generate: map<K,V> fields. gen.collect() and gen.message() both skip any
// field where FieldDescriptor.IsMap() is true (see cmd/pogoshimgen/main.go),
// so a message ONLY reachable from a root through a map field -- as
// BattleActorProto and BattlePokemonProto are, via BattleStateProto.actors
// and .pokemon -- never gets a generated shim type at all, not just a
// missing accessor. decoder/incident_decode.go's updateFromBattleState is
// the one piece of migrated Wave 3 consumer code that reads these maps
// (BattleStateProto's other two map fields, team_actor_count and
// party_member_count, are unread and so have no accessors here); this file
// hand-writes exactly what that call site needs, following the generator's
// own conventions (cached FieldDescriptor vars, Has/Get getters that chain
// to a zero shim on any absence, strings.Clone on every extracted string) so
// the hand-written surface reads like generated code.
//
// If a future root needs more map fields (or more BattleActorProto/
// BattlePokemonProto fields than are here), prefer teaching the generator
// proper map support over growing this file ad hoc -- see gen.collect's
// early return and gen.message's `if f.IsMap() { continue }` in
// cmd/pogoshimgen/main.go.

import (
	"iter"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"

	"golbat/pogo"
)

var (
	fd_BattleStateProto_actors  = mustFD((*pogo.BattleStateProto)(nil).ProtoReflect().Descriptor(), "actors")
	fd_BattleStateProto_pokemon = mustFD((*pogo.BattleStateProto)(nil).ProtoReflect().Descriptor(), "pokemon")

	fd_BattleActorProto_id                = mustFD((*pogo.BattleActorProto)(nil).ProtoReflect().Descriptor(), "id")
	fd_BattleActorProto_type              = mustFD((*pogo.BattleActorProto)(nil).ProtoReflect().Descriptor(), "type")
	fd_BattleActorProto_team              = mustFD((*pogo.BattleActorProto)(nil).ProtoReflect().Descriptor(), "team")
	fd_BattleActorProto_active_pokemon_id = mustFD((*pogo.BattleActorProto)(nil).ProtoReflect().Descriptor(), "active_pokemon_id")
	fd_BattleActorProto_pokemon_roster    = mustFD((*pogo.BattleActorProto)(nil).ProtoReflect().Descriptor(), "pokemon_roster")

	fd_BattlePokemonProto_pokedex_id = mustFD((*pogo.BattlePokemonProto)(nil).ProtoReflect().Descriptor(), "pokedex_id")
	fd_BattlePokemonProto_display    = mustFD((*pogo.BattlePokemonProto)(nil).ProtoReflect().Descriptor(), "display")
)

// BattleActorProto wraps a hyperpb/protoreflect POGOProtos.Rpc.BattleActorProto
// message. Only the fields updateFromBattleState reads are exposed -- see
// this file's header comment for how to extend it.
type BattleActorProto struct{ m protoreflect.Message }

// AsBattleActorProto wraps a parsed message. A nil or invalid message yields
// the zero shim, exactly like every generated As<Root> constructor.
func AsBattleActorProto(m protoreflect.Message) BattleActorProto {
	if m == nil || !m.IsValid() {
		return BattleActorProto{}
	}
	return BattleActorProto{m}
}

func (x BattleActorProto) IsZero() bool { return x.m == nil }

func (x BattleActorProto) GetId() string {
	if x.m == nil {
		return ""
	}
	// Same arena-retention hazard as every generated singular string getter:
	// hyperpb's String() is an unsafe view into the parse's payload copy.
	return strings.Clone(x.m.Get(fd_BattleActorProto_id).String())
}

func (x BattleActorProto) GetType() pogo.BattleActorProto_ActorType {
	if x.m == nil {
		return pogo.BattleActorProto_ActorType(0)
	}
	return pogo.BattleActorProto_ActorType(x.m.Get(fd_BattleActorProto_type).Enum())
}

func (x BattleActorProto) GetTeam() pogo.Team {
	if x.m == nil {
		return pogo.Team(0)
	}
	return pogo.Team(x.m.Get(fd_BattleActorProto_team).Enum())
}

func (x BattleActorProto) GetActivePokemonId() uint64 {
	if x.m == nil {
		return 0
	}
	return x.m.Get(fd_BattleActorProto_active_pokemon_id).Uint()
}

func (x BattleActorProto) GetPokemonRoster() ScalarList {
	if x.m == nil {
		return ScalarList{}
	}
	return ScalarList{x.m.Get(fd_BattleActorProto_pokemon_roster).List()}
}

// BattlePokemonProto wraps a hyperpb/protoreflect
// POGOProtos.Rpc.BattlePokemonProto message. Only the fields
// updateFromBattleState reads are exposed -- see this file's header comment.
type BattlePokemonProto struct{ m protoreflect.Message }

// AsBattlePokemonProto wraps a parsed message. A nil or invalid message
// yields the zero shim, exactly like every generated As<Root> constructor.
func AsBattlePokemonProto(m protoreflect.Message) BattlePokemonProto {
	if m == nil || !m.IsValid() {
		return BattlePokemonProto{}
	}
	return BattlePokemonProto{m}
}

func (x BattlePokemonProto) IsZero() bool { return x.m == nil }

func (x BattlePokemonProto) GetPokedexId() pogo.HoloPokemonId {
	if x.m == nil {
		return pogo.HoloPokemonId(0)
	}
	return pogo.HoloPokemonId(x.m.Get(fd_BattlePokemonProto_pokedex_id).Enum())
}

func (x BattlePokemonProto) HasDisplay() bool {
	return x.m != nil && x.m.Has(fd_BattlePokemonProto_display)
}

func (x BattlePokemonProto) GetDisplay() PokemonDisplayProto {
	if x.m == nil {
		return PokemonDisplayProto{}
	}
	if v := x.m.Get(fd_BattlePokemonProto_display).Message(); v.IsValid() {
		return PokemonDisplayProto{v}
	}
	return PokemonDisplayProto{}
}

// BattleActorProtoMap wraps BattleStateProto.actors (map<string,
// BattleActorProto>). Iteration order is unspecified -- exactly like the
// native Go map[string]*BattleActorProto range this replaces.
type BattleActorProtoMap struct{ m protoreflect.Map }

func (l BattleActorProtoMap) Len() int {
	if l.m == nil {
		return 0
	}
	return l.m.Len()
}

// All iterates actor values only. The one caller (updateFromBattleState)
// never needs the map key (the actor's own "id" field, read via GetId(), is
// what it actually uses), so this never extracts/clones a key string --
// sidestepping that retention concern entirely rather than cloning a value
// nothing retains.
func (l BattleActorProtoMap) All() iter.Seq[BattleActorProto] {
	return func(yield func(BattleActorProto) bool) {
		if l.m == nil {
			return
		}
		l.m.Range(func(_ protoreflect.MapKey, v protoreflect.Value) bool {
			if bm := v.Message(); bm.IsValid() {
				return yield(BattleActorProto{bm})
			}
			return yield(BattleActorProto{})
		})
	}
}

func (x BattleStateProto) GetActors() BattleActorProtoMap {
	if x.m == nil {
		return BattleActorProtoMap{}
	}
	return BattleActorProtoMap{x.m.Get(fd_BattleStateProto_actors).Map()}
}

// BattlePokemonProtoMap wraps BattleStateProto.pokemon (map<uint64,
// BattlePokemonProto>).
type BattlePokemonProtoMap struct{ m protoreflect.Map }

func (l BattlePokemonProtoMap) Len() int {
	if l.m == nil {
		return 0
	}
	return l.m.Len()
}

// Get looks up a single pokemon by id -- the exact access pattern
// updateFromBattleState needs (pokemon[id] on the pre-shim
// map[uint64]*BattlePokemonProto, nil-checked). Returns the zero shim
// (IsZero() true) for a missing key, matching every other typed-nil-safe
// getter in this package: protoreflect.Map.Get returns an invalid
// (zero-type) Value for a missing key, and calling .Message() on THAT would
// panic, so the IsValid() check below guards it before the .Message() call
// that appears safe elsewhere in this package (there, it's always guarded by
// a Has() this map interface doesn't have an equivalent for).
func (l BattlePokemonProtoMap) Get(id uint64) BattlePokemonProto {
	if l.m == nil {
		return BattlePokemonProto{}
	}
	v := l.m.Get(protoreflect.ValueOfUint64(id).MapKey())
	if !v.IsValid() {
		return BattlePokemonProto{}
	}
	if bm := v.Message(); bm.IsValid() {
		return BattlePokemonProto{bm}
	}
	return BattlePokemonProto{}
}

func (x BattleStateProto) GetPokemon() BattlePokemonProtoMap {
	if x.m == nil {
		return BattlePokemonProtoMap{}
	}
	return BattlePokemonProtoMap{x.m.Get(fd_BattleStateProto_pokemon).Map()}
}
