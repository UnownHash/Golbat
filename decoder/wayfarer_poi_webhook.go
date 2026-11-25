package decoder

import (
	"golbat/webhooks"
)

// WayfarerPoiWebhook is one side (old or new) of the "wayfarer_poi" webhook.
// Shared fields use the fort_update hook's names.
type WayfarerPoiWebhook struct {
	Id          string   `json:"id"`
	Name        *string  `json:"name"`
	Description *string  `json:"description"`
	ImageUrl    *string  `json:"image_url"`
	Location    Location `json:"location"`
	InGame      bool     `json:"in_game"`
}

// WayfarerPoiChangeWebhook is the "wayfarer_poi" webhook payload. There is no
// removal: a POI that is no longer returned is not known to be gone.
type WayfarerPoiChangeWebhook struct {
	ChangeType string              `json:"change_type"`
	EditTypes  []string            `json:"edit_types,omitempty"`
	Old        *WayfarerPoiWebhook `json:"old,omitempty"`
	New        *WayfarerPoiWebhook `json:"new"`
}

func buildWayfarerPoiWebhook(p *WayfarerPoiData) *WayfarerPoiWebhook {
	return &WayfarerPoiWebhook{
		Id:          p.Id,
		Name:        p.Name.Ptr(),
		Description: p.Description.Ptr(),
		ImageUrl:    p.Url.Ptr(),
		Location:    Location{Latitude: p.Lat, Longitude: p.Lon},
		InGame:      p.InGame,
	}
}

// wayfarerPoiEditTypes lists what changed between two states of a POI. The
// image is compared by URL path, as in fort_update: the host can change
// without the image doing so.
func wayfarerPoiEditTypes(old, new *WayfarerPoiData) []string {
	var editTypes []string
	if old.Name != new.Name {
		editTypes = append(editTypes, "name")
	}
	if old.Description != new.Description {
		editTypes = append(editTypes, "description")
	}
	if old.Url.Valid != new.Url.Valid || getPathFromURL(old.Url.String) != getPathFromURL(new.Url.String) {
		editTypes = append(editTypes, "image_url")
	}
	if !floatAlmostEqual(old.Lat, new.Lat, floatTolerance) || !floatAlmostEqual(old.Lon, new.Lon, floatTolerance) {
		editTypes = append(editTypes, "location")
	}
	if old.InGame != new.InGame {
		editTypes = append(editTypes, "in_game")
	}
	return editTypes
}

// buildWayfarerPoiChangeWebhook returns the webhook a save should send, or nil
// when nothing a receiver cares about changed.
func buildWayfarerPoiChangeWebhook(old, new *WayfarerPoiData, isNewRecord bool) *WayfarerPoiChangeWebhook {
	if isNewRecord {
		return &WayfarerPoiChangeWebhook{ChangeType: NEW.String(), New: buildWayfarerPoiWebhook(new)}
	}
	editTypes := wayfarerPoiEditTypes(old, new)
	if len(editTypes) == 0 {
		return nil
	}
	return &WayfarerPoiChangeWebhook{
		ChangeType: EDIT.String(),
		EditTypes:  editTypes,
		Old:        buildWayfarerPoiWebhook(old),
		New:        buildWayfarerPoiWebhook(new),
	}
}

// wayfarerPoiWebhookOnSave sends the wayfarer_poi webhook for a new POI or one
// whose details changed. Caller holds the POI lock.
func wayfarerPoiWebhookOnSave(p *WayfarerPoi, isNewRecord bool) {
	if hook := buildWayfarerPoiChangeWebhook(&p.oldValues, &p.WayfarerPoiData, isNewRecord); hook != nil {
		webhooksSender.AddMessage(webhooks.WayfarerPoi, hook, MatchStatsGeofence(p.Lat, p.Lon))
	}
	p.snapshotOldValues()
}
