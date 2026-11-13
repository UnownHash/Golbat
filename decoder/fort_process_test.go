package decoder

import (
	"context"
	"testing"
	"time"

	"github.com/guregu/null/v6"

	"golbat/config"
	"golbat/db"
	"golbat/decoder/writebehind"
	"golbat/pogo"
)

func TestGetMapFortsPreservesConvertedGym(t *testing.T) {
	previousMemory, previousLimit := config.Config.FortInMemory, config.Config.Tuning.MaxFortResults
	config.Config.FortInMemory, config.Config.Tuning.MaxFortResults = true, 100
	previousGymQueue, previousPokestopQueue, previousWebhooks := gymQueue, pokestopQueue, webhooksSender
	gymQueue = writebehind.NewTypedQueue(writebehind.TypedQueueConfig[FortId, GymData]{
		Name: "gym", KeyFunc: func(g GymData) FortId { return g.Id },
		KeyCompare: FortId.Compare, Stats: getStatsCollector(),
	})
	pokestopQueue = writebehind.NewTypedQueue(writebehind.TypedQueueConfig[FortId, PokestopData]{
		Name: "pokestop", KeyFunc: func(p PokestopData) FortId { return p.Id },
		KeyCompare: FortId.Compare, Stats: getStatsCollector(),
	})
	webhooksSender = &recordingWebhooksSender{}
	t.Cleanup(func() {
		config.Config.FortInMemory, config.Config.Tuning.MaxFortResults = previousMemory, previousLimit
		gymQueue, pokestopQueue, webhooksSender = previousGymQueue, previousPokestopQueue, previousWebhooks
	})

	const lat, lon = 51.5, -0.12
	id := mustFortId(t, "00000000000000000000000000000d05")
	gym := &Gym{GymData: GymData{Id: id, Lat: lat, Lon: lon, Enabled: null.IntFrom(1)}}
	stop := &Pokestop{PokestopData: PokestopData{
		Id: id, Lat: lat, Lon: lon, Name: null.StringFrom("Old name"), Deleted: true,
	}}
	gymCache.Set(id, gym, time.Hour)
	pokestopCache.Set(id, stop, time.Hour)
	fortRtreeUpdateGymOnSave(gym)
	t.Cleanup(func() {
		gymCache.Delete(id)
		pokestopCache.Delete(id)
		gym.Deleted = true
		fortRtreeUpdateGymOnSave(gym)
		fortTreeSnapshot.Store(nil)
	})

	dbDetails := db.DbDetails{}
	params := ApiFortScan{
		Min: ApiLatLon{Lat: lat - .01, Lon: lon - .01},
		Max: ApiLatLon{Lat: lat + .01, Lon: lon + .01},
	}
	assertGymInScan := func() {
		t.Helper()
		fortTreeSnapshot.Store(nil)
		for _, result := range GymScanEndpoint(params, dbDetails).Gyms {
			if result.Id == id.String() {
				return
			}
		}
		t.Fatalf("gym %s missing from scan", id)
	}
	assertGymInScan()

	found, _ := UpdateFortRecordWithGetMapFortsOutProto(context.Background(), dbDetails, &pogo.GetMapFortsOutProto_FortProto{
		Id: id.String(), Latitude: lat, Longitude: lon, Name: "New name",
	})
	assertGymInScan()
	if !found || gym.Name.ValueOrZero() != "New name" || gymQueue.Size() != 1 {
		t.Fatal("GetMapForts did not update the gym")
	}
	if stop.Name.ValueOrZero() != "Old name" || pokestopQueue.Size() != 0 {
		t.Fatal("GetMapForts updated the deleted pokestop")
	}
}
