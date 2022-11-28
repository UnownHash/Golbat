package webhooks

import (
	"encoding/json"
	log "github.com/sirupsen/logrus"
	"golang.org/x/exp/slices"
	"golbat/config"
	"sync"
)

type WebhookMessage struct {
	Type    string      `json:"type"`
	Message interface{} `json:"message"`
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
}

func AddMessage(webhookType string, message interface{}) {
	collectionAccess.Lock()
	list := webhookCollections[webhookType]
	list.AddItem(WebhookMessage{
		Type:    webhookType,
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
			totalCollection = append(totalCollection, currentCollection[GymDetails].Messages...)
		}
		if hook.Types == nil || slices.Contains(hook.Types, "raid") {
			totalCollection = append(totalCollection, currentCollection[Raid].Messages...)
		}
		if hook.Types == nil || slices.Contains(hook.Types, "pokemon") {
			totalCollection = append(totalCollection, currentCollection[Pokemon].Messages...)
		}
		if hook.Types == nil || slices.Contains(hook.Types, "quest") {
			totalCollection = append(totalCollection, currentCollection[Quest].Messages...)
		}
		if hook.Types == nil || slices.Contains(hook.Types, "invasion") {
			totalCollection = append(totalCollection, currentCollection[Invasion].Messages...)
		}
		if hook.Types == nil || slices.Contains(hook.Types, "pokestop") {
			totalCollection = append(totalCollection, currentCollection[Pokestop].Messages...)
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
