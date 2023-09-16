package webhooks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"golbat/config"
	"golbat/geo"
	"io"
	"net/http"
	"net/url"

	log "github.com/sirupsen/logrus"
)

type WebhookType uint8

const (
	GymDetails WebhookType = iota
	Raid
	Quest
	Pokestop
	Invasion
	Weather
	FortUpdate
	PokemonIV
	PokemonNoIV
	// this magically becomes the number of types we have
	webhookTypesLength
)

var webhookTypeToPayloadType [webhookTypesLength]string

func init() {
	webhookTypeToPayloadType[GymDetails] = "gym_details"
	webhookTypeToPayloadType[Raid] = "raid"
	webhookTypeToPayloadType[Quest] = "quest"
	webhookTypeToPayloadType[Pokestop] = "pokestop"
	webhookTypeToPayloadType[Invasion] = "invasion"
	webhookTypeToPayloadType[Weather] = "weather"
	webhookTypeToPayloadType[FortUpdate] = "fort_update"
	webhookTypeToPayloadType[PokemonIV] = "pokemon"
	webhookTypeToPayloadType[PokemonNoIV] = "pokemon"

	// if we add more types, make sure one has added everything here
	for _, str := range webhookTypeToPayloadType {
		if str == "" {
			panic(errors.New("ruh roh! looks like you forgot to add a new webhook type to webhookTypeToPayload"))
		}
	}
}

var webhookConfigStringToType = map[string][]WebhookType{
	"gym":           []WebhookType{GymDetails},
	"raid":          []WebhookType{Raid},
	"quest":         []WebhookType{Quest},
	"pokestop":      []WebhookType{Pokestop},
	"invasion":      []WebhookType{Invasion},
	"weather":       []WebhookType{Weather},
	"fort_update":   []WebhookType{FortUpdate},
	"pokemon_iv":    []WebhookType{PokemonIV},
	"pokemon_no_iv": []WebhookType{PokemonNoIV},
	"pokemon":       []WebhookType{PokemonIV, PokemonNoIV},
}

type webhook struct {
	url         string
	areaNames   []geo.AreaName
	typesWanted []WebhookType
	httpClient  *http.Client
}

func (wh *webhook) getPayload(collection webhookCollection) ([]byte, error) {
	var totalCollection []webhookMessage

	if len(wh.areaNames) == 0 {
		for _, whType := range wh.typesWanted {
			totalCollection = append(
				totalCollection,
				collection[whType].Messages...,
			)
		}
	} else {
		for _, whType := range wh.typesWanted {
			for _, message := range collection[whType].Messages {
				if geo.AreaMatchWithWildcards(message.Areas, wh.areaNames) {
					totalCollection = append(totalCollection, message)
				}
			}
		}
	}

	log.Infof("There are %d webhooks to send to %s", len(totalCollection), wh.url)

	if len(totalCollection) == 0 {
		return nil, nil
	}

	return json.Marshal(totalCollection)
}

func (wh *webhook) sendCollection(collection webhookCollection) error {
	payload, err := wh.getPayload(collection)
	if err != nil {
		return fmt.Errorf("failed to generate payload: %s", err)
	}

	if payload == nil {
		// nothing to send
		return nil
	}

	req, err := http.NewRequest("POST", wh.url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("failed to create http request to %s: %s", wh.url, err)
	}

	req.Header.Set("X-Golbat", "hey!")
	req.Header.Set("Content-Type", "application/json")

	resp, err := wh.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send webhook to %s: %s", wh.url, err)
	}

	defer func() {
		// full body must be read to reuse keep-alive connections.
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	log.Debugf("Webhook: Response %s", resp.Status)
	return nil
}

func webhookFromConfigWebhook(configWh config.Webhook) (*webhook, error) {
	urlStr := configWh.Url

	urlObj, err := url.Parse(urlStr)
	if err == nil && urlObj.Scheme == "" {
		err = errors.New("no scheme")
	}
	if err != nil {
		return nil, fmt.Errorf("invalid webhook url '%s': %s", urlStr, err)
	}

	var typesWanted []WebhookType
	deduped := make(map[WebhookType]bool)

	for _, typeStr := range configWh.Types {
		whTypes, ok := webhookConfigStringToType[typeStr]
		if !ok {
			return nil, fmt.Errorf("unknown webhook type '%s'", typeStr)
		}
		for _, whType := range whTypes {
			deduped[whType] = true
		}
	}

	// we want typesWanted to return the types in order to make testing easier
	for i := WebhookType(0); i < webhookTypesLength; i++ {
		// make sure the type is wanted when no types were specified
		if len(deduped) == 0 || deduped[i] {
			typesWanted = append(typesWanted, i)
		}
	}

	return &webhook{
		url:         urlStr,
		typesWanted: typesWanted,
		areaNames:   configWh.AreaNames,
		httpClient:  &http.Client{},
	}, nil
}
