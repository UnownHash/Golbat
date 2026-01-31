package decoder

import (
	"sync"

	"github.com/guregu/null/v6"
)

// Incident struct.
// REMINDER! Dirty flag pattern - use setter methods to modify fields
type Incident struct {
	mu sync.Mutex `db:"-"` // Object-level mutex

	Id             string   `db:"id"`
	PokestopId     string   `db:"pokestop_id"`
	StartTime      int64    `db:"start"`
	ExpirationTime int64    `db:"expiration"`
	DisplayType    int16    `db:"display_type"`
	Style          int16    `db:"style"`
	Character      int16    `db:"character"`
	Updated        int64    `db:"updated"`
	Confirmed      bool     `db:"confirmed"`
	Slot1PokemonId null.Int `db:"slot_1_pokemon_id"`
	Slot1Form      null.Int `db:"slot_1_form"`
	Slot2PokemonId null.Int `db:"slot_2_pokemon_id"`
	Slot2Form      null.Int `db:"slot_2_form"`
	Slot3PokemonId null.Int `db:"slot_3_pokemon_id"`
	Slot3Form      null.Int `db:"slot_3_form"`

	dirty     bool `db:"-"` // Not persisted - tracks if object needs saving
	newRecord bool `db:"-"` // Not persisted - tracks if this is a new record

	oldValues IncidentOldValues `db:"-"` // Old values for webhook comparison
}

// IncidentOldValues holds old field values for webhook comparison and stats
type IncidentOldValues struct {
	StartTime      int64
	ExpirationTime int64
	Character      int16
	Confirmed      bool
	Slot1PokemonId null.Int
}

type webhookLineup struct {
	Slot      uint8    `json:"slot"`
	PokemonId null.Int `json:"pokemon_id"`
	Form      null.Int `json:"form"`
}

type IncidentWebhook struct {
	Id                      string          `json:"id"`
	PokestopId              string          `json:"pokestop_id"`
	Latitude                float64         `json:"latitude"`
	Longitude               float64         `json:"longitude"`
	PokestopName            string          `json:"pokestop_name"`
	Url                     string          `json:"url"`
	Enabled                 bool            `json:"enabled"`
	Start                   int64           `json:"start"`
	IncidentExpireTimestamp int64           `json:"incident_expire_timestamp"`
	Expiration              int64           `json:"expiration"`
	DisplayType             int16           `json:"display_type"`
	Style                   int16           `json:"style"`
	GruntType               int16           `json:"grunt_type"`
	Character               int16           `json:"character"`
	Updated                 int64           `json:"updated"`
	Confirmed               bool            `json:"confirmed"`
	Lineup                  []webhookLineup `json:"lineup"`
}

//->   `id` varchar(35) NOT NULL,
//->   `pokestop_id` varchar(35) NOT NULL,
//->   `start` int unsigned NOT NULL,
//->   `expiration` int unsigned NOT NULL,
//->   `display_type` smallint unsigned NOT NULL,
//->   `style` smallint unsigned NOT NULL,
//->   `character` smallint unsigned NOT NULL,
//->   `updated` int unsigned NOT NULL,

// IsDirty returns true if any field has been modified
func (incident *Incident) IsDirty() bool {
	return incident.dirty
}

// ClearDirty resets the dirty flag (call after saving to DB)
func (incident *Incident) ClearDirty() {
	incident.dirty = false
}

// IsNewRecord returns true if this is a new record (not yet in DB)
func (incident *Incident) IsNewRecord() bool {
	return incident.newRecord
}

// Lock acquires the Incident's mutex
func (incident *Incident) Lock() {
	incident.mu.Lock()
}

// Unlock releases the Incident's mutex
func (incident *Incident) Unlock() {
	incident.mu.Unlock()
}

// snapshotOldValues saves current values for webhook comparison
// Call this after loading from cache/DB but before modifications
func (incident *Incident) snapshotOldValues() {
	incident.oldValues = IncidentOldValues{
		StartTime:      incident.StartTime,
		ExpirationTime: incident.ExpirationTime,
		Character:      incident.Character,
		Confirmed:      incident.Confirmed,
		Slot1PokemonId: incident.Slot1PokemonId,
	}
}

// --- Set methods with dirty tracking ---

func (incident *Incident) SetId(v string) {
	if incident.Id != v {
		incident.Id = v
		incident.dirty = true
	}
}

func (incident *Incident) SetPokestopId(v string) {
	if incident.PokestopId != v {
		incident.PokestopId = v
		incident.dirty = true
	}
}

func (incident *Incident) SetStartTime(v int64) {
	if incident.StartTime != v {
		incident.StartTime = v
		incident.dirty = true
	}
}

func (incident *Incident) SetExpirationTime(v int64) {
	if incident.ExpirationTime != v {
		incident.ExpirationTime = v
		incident.dirty = true
	}
}

func (incident *Incident) SetDisplayType(v int16) {
	if incident.DisplayType != v {
		incident.DisplayType = v
		incident.dirty = true
	}
}

func (incident *Incident) SetStyle(v int16) {
	if incident.Style != v {
		incident.Style = v
		incident.dirty = true
	}
}

func (incident *Incident) SetCharacter(v int16) {
	if incident.Character != v {
		incident.Character = v
		incident.dirty = true
	}
}

func (incident *Incident) SetConfirmed(v bool) {
	if incident.Confirmed != v {
		incident.Confirmed = v
		incident.dirty = true
	}
}

func (incident *Incident) SetSlot1PokemonId(v null.Int) {
	if incident.Slot1PokemonId != v {
		incident.Slot1PokemonId = v
		incident.dirty = true
	}
}

func (incident *Incident) SetSlot1Form(v null.Int) {
	if incident.Slot1Form != v {
		incident.Slot1Form = v
		incident.dirty = true
	}
}

func (incident *Incident) SetSlot2PokemonId(v null.Int) {
	if incident.Slot2PokemonId != v {
		incident.Slot2PokemonId = v
		incident.dirty = true
	}
}

func (incident *Incident) SetSlot2Form(v null.Int) {
	if incident.Slot2Form != v {
		incident.Slot2Form = v
		incident.dirty = true
	}
}

func (incident *Incident) SetSlot3PokemonId(v null.Int) {
	if incident.Slot3PokemonId != v {
		incident.Slot3PokemonId = v
		incident.dirty = true
	}
}

func (incident *Incident) SetSlot3Form(v null.Int) {
	if incident.Slot3Form != v {
		incident.Slot3Form = v
		incident.dirty = true
	}
}
