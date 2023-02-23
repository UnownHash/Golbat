package webhooks

import (
	"encoding/json"
	log "github.com/sirupsen/logrus"
	"golang.org/x/exp/slices"
	"golbat/config"
	"golbat/geo"
	"sync"
)

type WebhookMessage struct {
	Type    string         `json:"type"`
	Areas   []geo.AreaName `json:"-"`
	Message interface{}    `json:"message"`
}

type WebhookList struct {
	Messages []WebhookMessage
}

func (webhookList *WebhookList) AddItem(item WebhookMessage) {
	webhookList.Messages = append(webhookList.Messages, item)
}

var webhookCollections map[string]*WebhookList

var GymDetails = "gym_details"
var Raid = "raid"
var Pokemon = "pokemon"
var Quest = "quest"
var Pokestop = "pokestop"
var Invasion = "invasion"
var Weather = "weather"
var FortUpdate = "fort_update"

var collectionAccess sync.Mutex

func init() {
	SetMaps()

}

func SetMaps() {
	webhookCollections = make(map[string]*WebhookList)
	webhookCollections[GymDetails] = &WebhookList{}
	webhookCollections[Raid] = &WebhookList{}
	webhookCollections[Pokemon] = &WebhookList{}
	webhookCollections[Quest] = &WebhookList{}
	webhookCollections[Pokestop] = &WebhookList{}
	webhookCollections[Invasion] = &WebhookList{}
	webhookCollections[Weather] = &WebhookList{}
}

func AddMessage(webhookType string, message interface{}, areas []geo.AreaName) {
	collectionAccess.Lock()
	list := webhookCollections[webhookType]
	list.AddItem(WebhookMessage{
		Type:    webhookType,
		Areas:   areas,
		Message: message,
	})
	//webhookCollections[webhookType] = list
	collectionAccess.Unlock()
}

func collectHooks() []WebhookQueue {
	collectionAccess.Lock()
	currentCollection := webhookCollections
	SetMaps()
	collectionAccess.Unlock()

	var destinations []WebhookQueue

	for _, hook := range config.Config.Webhooks {

		var totalCollection []WebhookMessage
		if hook.Types == nil || slices.Contains(hook.Types, "gym") {
			if len(hook.AreaNames) == 0 {
				totalCollection = append(totalCollection, currentCollection[GymDetails].Messages...)
			} else {
				for _, message := range currentCollection[GymDetails].Messages {
					if doAreasMatch(message.Areas, hook.AreaNames) {
						totalCollection = append(totalCollection, message)
					}
				}
			}
		}
		if hook.Types == nil || slices.Contains(hook.Types, "raid") {
			if len(hook.AreaNames) == 0 {
				totalCollection = append(totalCollection, currentCollection[Raid].Messages...)
			} else {
				for _, message := range currentCollection[Raid].Messages {
					if doAreasMatch(message.Areas, hook.AreaNames) {
						totalCollection = append(totalCollection, message)
					}
				}
			}
		}
		if hook.Types == nil || slices.Contains(hook.Types, "weather") {
			if len(hook.AreaNames) == 0 {
				totalCollection = append(totalCollection, currentCollection[Weather].Messages...)
			} else {
				for _, message := range currentCollection[Weather].Messages {
					if doAreasMatch(message.Areas, hook.AreaNames) {
						totalCollection = append(totalCollection, message)
					}
				}
			}
		}
		if hook.Types == nil || slices.Contains(hook.Types, "pokemon") {
			if len(hook.AreaNames) == 0 {
				totalCollection = append(totalCollection, currentCollection[Pokemon].Messages...)
			} else {
				for _, message := range currentCollection[Pokemon].Messages {
					if doAreasMatch(message.Areas, hook.AreaNames) {
						totalCollection = append(totalCollection, message)
					}
				}
			}

		}
		if hook.Types == nil || slices.Contains(hook.Types, "quest") {
			if len(hook.AreaNames) == 0 {
				totalCollection = append(totalCollection, currentCollection[Quest].Messages...)
			} else {
				for _, message := range currentCollection[Quest].Messages {
					if doAreasMatch(message.Areas, hook.AreaNames) {
						totalCollection = append(totalCollection, message)
					}
				}
			}
		}
		if hook.Types == nil || slices.Contains(hook.Types, "invasion") {
			if len(hook.AreaNames) == 0 {
				totalCollection = append(totalCollection, currentCollection[Invasion].Messages...)
			} else {
				for _, message := range currentCollection[Invasion].Messages {
					if doAreasMatch(message.Areas, hook.AreaNames) {
						totalCollection = append(totalCollection, message)
					}
				}
			}
		}
		if hook.Types == nil || slices.Contains(hook.Types, "pokestop") {
			if len(hook.AreaNames) == 0 {
				totalCollection = append(totalCollection, currentCollection[Pokestop].Messages...)
			} else {
				for _, message := range currentCollection[Pokestop].Messages {
					if doAreasMatch(message.Areas, hook.AreaNames) {
						totalCollection = append(totalCollection, message)
					}
				}
			}
		}
		if hook.Types == nil || slices.Contains(hook.Types, "fort_update") {
			if len(hook.AreaNames) == 0 {
				totalCollection = append(totalCollection, currentCollection[FortUpdate].Messages...)
			} else {
				for _, message := range currentCollection[FortUpdate].Messages {
					if doAreasMatch(message.Areas, hook.AreaNames) {
						totalCollection = append(totalCollection, message)
					}
				}
			}
		}
		log.Infof("There are %d webhooks to send to %s", len(totalCollection), hook.Url)

		if len(totalCollection) > 0 {
			output, _ := json.Marshal(totalCollection)

			collection := WebhookQueue{
				url:     hook.Url,
				webhook: output,
			}

			destinations = append(destinations, collection)
		}

	}

	return destinations
}

func doAreasMatch(messageAreas []geo.AreaName, hookAreas []geo.AreaName) bool {
	for _, hookArea := range hookAreas {
		for _, messageArea := range messageAreas {
			if hookArea.Name == "*" {
				if hookArea.Parent == messageArea.Parent {
					return true
				}
			} else if hookArea.Parent == "*" {
				if hookArea.Name == messageArea.Name {
					return true
				}
			} else {
				if hookArea.Parent == messageArea.Parent && hookArea.Name == messageArea.Name {
					return true
				}
			}
		}
	}
	return false
}
