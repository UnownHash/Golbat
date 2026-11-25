package decoder

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/guregu/null/v6"
	log "github.com/sirupsen/logrus"

	"golbat/db"
	"golbat/pogo"
)

// fortImage is one entry of the `images` column of a pokestop or gym: a
// player-submitted photo from the fort's gallery, with its vote counts.
// has_player_voted and submitter_codename are left out: the first describes the
// scanning account, the second names a player.
type fortImage struct {
	ImageId             string `json:"image_id"`
	ImageUrl            string `json:"image_url"`
	CreationTimestampMs int64  `json:"creation_timestamp_ms"`
	NumVotesFromGame    int32  `json:"num_votes_from_game"`
	NumVotesNianticWide int32  `json:"num_votes_niantic_wide"`
}

// fortImagesJson builds the stored JSON for a gallery. Images are ordered by
// creation time, then id, so the same gallery always serializes the same way
// and an unchanged one does not rewrite the fort. An empty gallery is NULL.
func fortImagesJson(images []*pogo.TitanGameClientPhotoGalleryPoiImageProto) null.String {
	out := make([]fortImage, 0, len(images))
	for _, image := range images {
		if image.GetImageId() == "" {
			continue
		}
		out = append(out, fortImage{
			ImageId:             image.GetImageId(),
			ImageUrl:            image.GetImageUrl(),
			CreationTimestampMs: image.GetCreationTimestampMs(),
			NumVotesFromGame:    image.GetNumVotesFromGame(),
			NumVotesNianticWide: image.GetNumVotesNianticWide(),
		})
	}
	if len(out) == 0 {
		return null.NewString("", false)
	}
	slices.SortFunc(out, func(a, b fortImage) int {
		return cmp.Or(cmp.Compare(a.CreationTimestampMs, b.CreationTimestampMs), cmp.Compare(a.ImageId, b.ImageId))
	})
	b, _ := json.Marshal(out)
	return null.StringFrom(string(b))
}

// UpdateFortRecordWithImagesProto stores the photo gallery of a fort (the
// response to TITAN_PLAYER_SUBMISSION_ACTION_GET_IMAGES_FOR_POI). poiId comes
// from the request when there is one, otherwise from the images themselves.
// Only forts already known are updated: the gallery says nothing else about one.
func UpdateFortRecordWithImagesProto(ctx context.Context, db db.DbDetails, poiId string, resp *pogo.TitanGetImagesForPoiOutProto) string {
	if poiId == "" {
		for _, image := range resp.GetPhotoGalleryPoiImages() {
			if poiId = image.GetPoiId(); poiId != "" {
				break
			}
		}
	}
	if poiId == "" {
		return "No poi id: an empty gallery needs the request"
	}
	fortId, ok := ParseFortId(poiId)
	if !ok {
		log.Errorf("UpdateFortRecordWithImagesProto: unparseable fort id %q", poiId)
		return fmt.Sprintf("%s unparseable fort id", poiId)
	}
	images := fortImagesJson(resp.GetPhotoGalleryPoiImages())
	count := len(resp.GetPhotoGalleryPoiImages())

	// Either table can retain the deleted row from a conversion: use the active one.
	if done, err := updatePokestopImages(ctx, db, fortId, images); err != nil {
		return err.Error()
	} else if done {
		return fmt.Sprintf("%s pokestop: %d images", poiId, count)
	}
	if done, err := updateGymImages(ctx, db, fortId, images); err != nil {
		return err.Error()
	} else if done {
		return fmt.Sprintf("%s gym: %d images", poiId, count)
	}
	return fmt.Sprintf("%s Fort not present", poiId)
}

func updatePokestopImages(ctx context.Context, db db.DbDetails, fortId FortId, images null.String) (bool, error) {
	pokestop, unlock, err := getPokestopRecordForUpdate(ctx, db, fortId, "UpdatePokestopImages")
	if err != nil || pokestop == nil {
		return false, err
	}
	defer unlock()
	if pokestop.Deleted {
		return false, nil
	}
	pokestop.SetImages(images)
	savePokestopRecord(ctx, db, pokestop)
	return true, nil
}

func updateGymImages(ctx context.Context, db db.DbDetails, fortId FortId, images null.String) (bool, error) {
	gym, unlock, err := getGymRecordForUpdate(ctx, db, fortId, "UpdateGymImages")
	if err != nil || gym == nil {
		return false, err
	}
	defer unlock()
	if gym.Deleted {
		return false, nil
	}
	gym.SetImages(images)
	saveGymRecord(ctx, db, gym)
	return true, nil
}
