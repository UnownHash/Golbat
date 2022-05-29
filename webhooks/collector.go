package webhooks

import (
	"encoding/json"
	log "github.com/sirupsen/logrus"
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

var collectionAccess sync.Mutex

func init() {
	SetMaps()

}

func SetMaps() {
	webhookCollections = make(map[string]*WebhookList)
	webhookCollections[GymDetails] = &WebhookList{}
	webhookCollections[Raid] = &WebhookList{}
	webhookCollections[Pokemon] = &WebhookList{}
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

	var totalCollection []WebhookMessage
	totalCollection = append(totalCollection, currentCollection[GymDetails].Messages...)
	totalCollection = append(totalCollection, currentCollection[Raid].Messages...)
	totalCollection = append(totalCollection, currentCollection[Pokemon].Messages...)

	var destinations []WebhookQueue

	if len(totalCollection) > 0 {
		log.Printf("There are %d webhooks to send", len(totalCollection))
		output, _ := json.Marshal(totalCollection)

		for _, url := range config.Config.Webhooks {
			collection := WebhookQueue{
				url:     url,
				webhook: output,
			}

			destinations = append(destinations, collection)
		}
	} else {
		log.Debugln("There are no webhooks to send")
	}

	return destinations
}
