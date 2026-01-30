package decoder

import (
	"context"
	"database/sql"
	"time"

	"github.com/jellydator/ttlcache/v3"
	log "github.com/sirupsen/logrus"
	null "gopkg.in/guregu/null.v4"

	"golbat/db"
	"golbat/pogo"
	"golbat/webhooks"
)

// Incident struct.
// REMINDER! Dirty flag pattern - use setter methods to modify fields
type Incident struct {
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

	dirty     bool `db:"-" json:"-"` // Not persisted - tracks if object needs saving
	newRecord bool `db:"-" json:"-"` // Not persisted - tracks if this is a new record

	oldValues IncidentOldValues `db:"-" json:"-"` // Old values for webhook comparison
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

func getIncidentRecord(ctx context.Context, db db.DbDetails, incidentId string) (*Incident, error) {
	inMemoryIncident := incidentCache.Get(incidentId)
	if inMemoryIncident != nil {
		incident := inMemoryIncident.Value()
		incident.snapshotOldValues()
		return incident, nil
	}

	incident := Incident{}
	err := db.GeneralDb.GetContext(ctx, &incident,
		"SELECT id, pokestop_id, start, expiration, display_type, style, `character`, updated, confirmed, slot_1_pokemon_id, slot_1_form, slot_2_pokemon_id, slot_2_form, slot_3_pokemon_id, slot_3_form "+
			"FROM incident "+
			"WHERE incident.id = ? ", incidentId)
	statsCollector.IncDbQuery("select incident", err)
	if err == sql.ErrNoRows {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	incidentCache.Set(incidentId, &incident, ttlcache.DefaultTTL)
	incident.snapshotOldValues()
	return &incident, nil
}

func saveIncidentRecord(ctx context.Context, db db.DbDetails, incident *Incident) {
	// Skip save if not dirty and not new
	if !incident.IsDirty() && !incident.IsNewRecord() {
		return
	}

	incident.Updated = time.Now().Unix()

	if incident.IsNewRecord() {
		res, err := db.GeneralDb.NamedExec("INSERT INTO incident (id, pokestop_id, start, expiration, display_type, style, `character`, updated, confirmed, slot_1_pokemon_id, slot_1_form, slot_2_pokemon_id, slot_2_form, slot_3_pokemon_id, slot_3_form) "+
			"VALUES (:id, :pokestop_id, :start, :expiration, :display_type, :style, :character, :updated, :confirmed, :slot_1_pokemon_id, :slot_1_form, :slot_2_pokemon_id, :slot_2_form, :slot_3_pokemon_id, :slot_3_form)", incident)

		if err != nil {
			log.Errorf("insert incident: %s", err)
			return
		}
		statsCollector.IncDbQuery("insert incident", err)
		_, _ = res, err
	} else {
		res, err := db.GeneralDb.NamedExec("UPDATE incident SET "+
			"start = :start, "+
			"expiration = :expiration, "+
			"display_type = :display_type, "+
			"style = :style, "+
			"`character` = :character, "+
			"updated = :updated, "+
			"confirmed = :confirmed, "+
			"slot_1_pokemon_id = :slot_1_pokemon_id, "+
			"slot_1_form = :slot_1_form, "+
			"slot_2_pokemon_id = :slot_2_pokemon_id, "+
			"slot_2_form = :slot_2_form, "+
			"slot_3_pokemon_id = :slot_3_pokemon_id, "+
			"slot_3_form = :slot_3_form "+
			"WHERE id = :id", incident,
		)
		statsCollector.IncDbQuery("update incident", err)
		if err != nil {
			log.Errorf("Update incident %s", err)
		}
		_, _ = res, err
	}

	createIncidentWebhooks(ctx, db, incident)

	var stopLat, stopLon float64
	stop, unlock, _ := getPokestopRecordReadOnly(ctx, db, incident.PokestopId)
	if stop != nil {
		stopLat, stopLon = stop.Lat, stop.Lon
		unlock()
	}

	areas := MatchStatsGeofence(stopLat, stopLon)
	updateIncidentStats(incident, areas)

	incident.ClearDirty()
	incident.newRecord = false
	//incidentCache.Set(incident.Id, incident, ttlcache.DefaultTTL)
}

func createIncidentWebhooks(ctx context.Context, db db.DbDetails, incident *Incident) {
	old := &incident.oldValues
	isNew := incident.IsNewRecord()

	if isNew || (old.ExpirationTime != incident.ExpirationTime || old.Character != incident.Character || old.Confirmed != incident.Confirmed || old.Slot1PokemonId != incident.Slot1PokemonId) {
		var pokestopName, stopUrl string
		var stopLat, stopLon float64
		var stopEnabled bool
		stop, unlock, _ := getPokestopRecordReadOnly(ctx, db, incident.PokestopId)
		if stop != nil {
			pokestopName = stop.Name.ValueOrZero()
			stopLat, stopLon = stop.Lat, stop.Lon
			stopUrl = stop.Url.ValueOrZero()
			stopEnabled = stop.Enabled.ValueOrZero()
			unlock()
		}
		if pokestopName == "" {
			pokestopName = "Unknown"
		}

		var lineup []webhookLineup
		if incident.Slot1PokemonId.Valid {
			lineup = []webhookLineup{
				{
					Slot:      1,
					PokemonId: incident.Slot1PokemonId,
					Form:      incident.Slot1Form,
				},
				{
					Slot:      2,
					PokemonId: incident.Slot2PokemonId,
					Form:      incident.Slot2Form,
				},
				{
					Slot:      3,
					PokemonId: incident.Slot3PokemonId,
					Form:      incident.Slot3Form,
				},
			}
		}

		incidentHook := IncidentWebhook{
			Id:                      incident.Id,
			PokestopId:              incident.PokestopId,
			Latitude:                stopLat,
			Longitude:               stopLon,
			PokestopName:            pokestopName,
			Url:                     stopUrl,
			Enabled:                 stopEnabled,
			Start:                   incident.StartTime,
			IncidentExpireTimestamp: incident.ExpirationTime,
			Expiration:              incident.ExpirationTime,
			DisplayType:             incident.DisplayType,
			Style:                   incident.Style,
			GruntType:               incident.Character,
			Character:               incident.Character,
			Updated:                 incident.Updated,
			Confirmed:               incident.Confirmed,
			Lineup:                  lineup,
		}

		areas := MatchStatsGeofence(stop.Lat, stop.Lon)
		webhooksSender.AddMessage(webhooks.Invasion, incidentHook, areas)
		statsCollector.UpdateIncidentCount(areas)
	}
}

func (incident *Incident) updateFromPokestopIncidentDisplay(pokestopDisplay *pogo.PokestopIncidentDisplayProto) {
	incident.SetId(pokestopDisplay.IncidentId)
	incident.SetStartTime(int64(pokestopDisplay.IncidentStartMs / 1000))
	incident.SetExpirationTime(int64(pokestopDisplay.IncidentExpirationMs / 1000))
	incident.SetDisplayType(int16(pokestopDisplay.IncidentDisplayType))
	if (incident.Character == int16(pogo.EnumWrapper_CHARACTER_DECOY_GRUNT_MALE) || incident.Character == int16(pogo.EnumWrapper_CHARACTER_DECOY_GRUNT_FEMALE)) && incident.Confirmed {
		log.Debugf("Incident has already been confirmed as a decoy: %s", incident.Id)
		return
	}
	characterDisplay := pokestopDisplay.GetCharacterDisplay()
	if characterDisplay != nil {
		// team := pokestopDisplay.Open
		incident.SetStyle(int16(characterDisplay.Style))
		incident.SetCharacter(int16(characterDisplay.Character))
	} else {
		incident.SetStyle(0)
		incident.SetCharacter(0)
	}
}

func (incident *Incident) updateFromOpenInvasionCombatSessionOut(protoRes *pogo.OpenInvasionCombatSessionOutProto) {
	incident.SetSlot1PokemonId(null.NewInt(int64(protoRes.Combat.Opponent.ActivePokemon.PokedexId.Number()), true))
	incident.SetSlot1Form(null.NewInt(int64(protoRes.Combat.Opponent.ActivePokemon.PokemonDisplay.Form.Number()), true))
	for i, pokemon := range protoRes.Combat.Opponent.ReservePokemon {
		if i == 0 {
			incident.SetSlot2PokemonId(null.NewInt(int64(pokemon.PokedexId.Number()), true))
			incident.SetSlot2Form(null.NewInt(int64(pokemon.PokemonDisplay.Form.Number()), true))
		} else if i == 1 {
			incident.SetSlot3PokemonId(null.NewInt(int64(pokemon.PokedexId.Number()), true))
			incident.SetSlot3Form(null.NewInt(int64(pokemon.PokemonDisplay.Form.Number()), true))
		}
	}
	incident.SetConfirmed(true)
}

func (incident *Incident) updateFromStartIncidentOut(proto *pogo.StartIncidentOutProto) {
	incident.SetCharacter(int16(proto.GetIncident().GetStep()[0].GetPokestopDialogue().GetDialogueLine()[0].GetCharacter()))
	if incident.Character == int16(pogo.EnumWrapper_CHARACTER_GIOVANNI) ||
		incident.Character == int16(pogo.EnumWrapper_CHARACTER_DECOY_GRUNT_MALE) ||
		incident.Character == int16(pogo.EnumWrapper_CHARACTER_DECOY_GRUNT_FEMALE) {
		incident.SetConfirmed(true)
	}
	incident.SetStartTime(int64(proto.Incident.GetCompletionDisplay().GetIncidentStartMs() / 1000))
	incident.SetExpirationTime(int64(proto.Incident.GetCompletionDisplay().GetIncidentExpirationMs() / 1000))
}
