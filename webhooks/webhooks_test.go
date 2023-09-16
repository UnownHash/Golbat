package webhooks

import (
	"context"
	"encoding/json"
	"golbat/config"
	"golbat/geo"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type webhookConfig struct {
	interval time.Duration
	webhooks []config.Webhook
}

func (wc webhookConfig) GetWebhookInterval() time.Duration {
	return wc.interval
}

func (wc webhookConfig) GetWebhooks() []config.Webhook {
	return wc.webhooks
}

type testWebhookReceiver struct {
	mutex            sync.Mutex
	server           *httptest.Server
	payloadsReceived []webhookMessage
}

func (r *testWebhookReceiver) GetPayloads() []webhookMessage {
	r.mutex.Lock()
	payloads := r.payloadsReceived
	r.payloadsReceived = nil
	r.mutex.Unlock()
	return payloads
}

func (r *testWebhookReceiver) URL() string {
	return r.server.URL
}

func (r *testWebhookReceiver) Close() {
	r.server.Close()
}

func createTestServer(responseCode int) *testWebhookReceiver {
	receiver := &testWebhookReceiver{}
	h := http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		defer req.Body.Close()
		decoder := json.NewDecoder(req.Body)

		var payloads []webhookMessage
		decoder.Decode(&payloads)
		receiver.mutex.Lock()
		receiver.payloadsReceived = append(receiver.payloadsReceived, payloads...)
		receiver.mutex.Unlock()
		rw.WriteHeader(responseCode)
	})
	receiver.server = httptest.NewServer(h)
	return receiver
}

func TestWebhookInvalidUrl(t *testing.T) {
	whConfig := webhookConfig{
		webhooks: []config.Webhook{
			config.Webhook{
				Url: "bogus",
			},
		},
	}
	_, err := NewWebhooksSender(whConfig)
	if err == nil {
		t.Fatal("webhook w/ invalid url didn't error")
	}
	if !strings.Contains(err.Error(), "invalid webhook url 'bogus'") {
		t.Fatalf("error string doesn't match expected: %s", err.Error())
	}
}

func TestWebhookInvalidConfigName(t *testing.T) {
	whConfig := webhookConfig{
		webhooks: []config.Webhook{
			config.Webhook{
				Url:   "http://localhost",
				Types: []string{"pokemon", "wut"},
			},
		},
	}
	_, err := NewWebhooksSender(whConfig)
	if err == nil {
		t.Fatal("webhook type 'unknown' didn't error")
	}
	if !strings.Contains(err.Error(), "unknown webhook type 'wut'") {
		t.Fatalf("error string doesn't match expected: %s", err.Error())
	}
}

func geoAreaNames(names ...string) []geo.AreaName {
	areas := make([]geo.AreaName, len(names))
	for i, name := range names {
		areas[i].Parent = "*"
		areas[i].Name = name
	}
	return areas
}

func TestWebhooksFull(t *testing.T) {
	var wg sync.WaitGroup

	allTypesAndAreasServer := createTestServer(200)
	everythingAreaServer := createTestServer(200)
	raidPokemonIVAreaServer := createTestServer(200)
	raidInvasionPokemonNoIVAreaServer := createTestServer(200)

	ctx, cancelFn := context.WithCancel(context.Background())
	defer func() {
		cancelFn()
		wg.Wait()
		allTypesAndAreasServer.Close()
		everythingAreaServer.Close()
		raidPokemonIVAreaServer.Close()
		raidInvasionPokemonNoIVAreaServer.Close()
	}()

	whConfig := webhookConfig{
		interval: 100 * time.Millisecond,
		webhooks: []config.Webhook{
			config.Webhook{
				Url: allTypesAndAreasServer.URL(),
			},
			config.Webhook{
				Url:       everythingAreaServer.URL(),
				AreaNames: geoAreaNames("everything-area"),
			},
			config.Webhook{
				Url:       raidPokemonIVAreaServer.URL(),
				Types:     []string{"raid", "pokemon_iv"},
				AreaNames: geoAreaNames("raid-pokemon_iv-area"),
			},
			config.Webhook{
				Url:       raidInvasionPokemonNoIVAreaServer.URL(),
				Types:     []string{"raid", "invasion", "pokemon_no_iv"},
				AreaNames: geoAreaNames("raid-invasion-pokemon_no_iv-area1", "raid-invasion-pokemon_no_iv-area2"),
			},
		},
	}

	sender, err := NewWebhooksSender(whConfig)
	if err != nil {
		t.Fatalf("unexpected error creating webhooksSender: %s", err)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		err := sender.Run(ctx)
		if err != nil {
			t.Fatalf("unexpected error starting webhooksSender: %s", err)
		}
	}()

	addMessages := func() {
		sender.AddMessage(PokemonIV, "pokemon-iv-payload1", geoAreaNames("everything-area", "raid-pokemon_iv-area"))
		sender.AddMessage(GymDetails, "pokemon-gym-payload1", geoAreaNames("everything-area", "raid-invasion-pokemon_no_iv-area"))
		sender.AddMessage(PokemonNoIV, "pokemon-noiv-payload1", geoAreaNames("everything-area", "raid-invasion-pokemon_no_iv-area"))
		sender.AddMessage(PokemonIV, "pokemon-iv-payload2", geoAreaNames("raid-invasion-pokemon_no_iv-area"))
		sender.AddMessage(PokemonIV, "pokemon-iv-payload3", geoAreaNames("everything-area", "raid-pokemon_iv-area"))
		sender.AddMessage(Raid, "pokemon-raid-payload1", geoAreaNames("everything-area", "raid-pokemon_iv-area"))
		sender.AddMessage(Invasion, "pokemon-invasion-payload1", geoAreaNames("everything-area", "raid-invasion_iv-area"))
		sender.AddMessage(Invasion, "pokemon-invasion-payload2", geoAreaNames("everything-area", "raid-invasion-pokemon_no_iv-area1"))
		sender.AddMessage(Invasion, "pokemon-invasion-payload3", geoAreaNames("everything-area", "raid-invasion-pokemon_no_iv-area2"))
		sender.AddMessage(PokemonNoIV, "pokemon-noiv-payload2", geoAreaNames("raid-invasion-pokemon_no_iv-area1"))
		sender.AddMessage(PokemonIV, "pokemon-iv-payload4", geoAreaNames("raid-invasion-pokemon_no_iv-area1"))
	}

	comparePayloads := func() {
		// payloads are ordered by type
		payloads := allTypesAndAreasServer.GetPayloads()
		if !reflect.DeepEqual(
			payloads,
			[]webhookMessage{
				webhookMessage{
					Type:    "gym_details",
					Message: "pokemon-gym-payload1",
				},
				webhookMessage{
					Type:    "raid",
					Message: "pokemon-raid-payload1",
				},
				webhookMessage{
					Type:    "invasion",
					Message: "pokemon-invasion-payload1",
				},
				webhookMessage{
					Type:    "invasion",
					Message: "pokemon-invasion-payload2",
				},
				webhookMessage{
					Type:    "invasion",
					Message: "pokemon-invasion-payload3",
				},
				webhookMessage{
					Type:    "pokemon",
					Message: "pokemon-iv-payload1",
				},
				webhookMessage{
					Type:    "pokemon",
					Message: "pokemon-iv-payload2",
				},
				webhookMessage{
					Type:    "pokemon",
					Message: "pokemon-iv-payload3",
				},
				webhookMessage{
					Type:    "pokemon",
					Message: "pokemon-iv-payload4",
				},
				webhookMessage{
					Type:    "pokemon",
					Message: "pokemon-noiv-payload1",
				},
				webhookMessage{
					Type:    "pokemon",
					Message: "pokemon-noiv-payload2",
				},
			},
		) {
			t.Fatalf("unexpected payload for allTypesAndAreasServer: %+v", payloads)
		}

		payloads = everythingAreaServer.GetPayloads()
		if !reflect.DeepEqual(
			payloads,
			[]webhookMessage{
				webhookMessage{
					Type:    "gym_details",
					Message: "pokemon-gym-payload1",
				},
				webhookMessage{
					Type:    "raid",
					Message: "pokemon-raid-payload1",
				},
				webhookMessage{
					Type:    "invasion",
					Message: "pokemon-invasion-payload1",
				},
				webhookMessage{
					Type:    "invasion",
					Message: "pokemon-invasion-payload2",
				},
				webhookMessage{
					Type:    "invasion",
					Message: "pokemon-invasion-payload3",
				},
				webhookMessage{
					Type:    "pokemon",
					Message: "pokemon-iv-payload1",
				},
				webhookMessage{
					Type:    "pokemon",
					Message: "pokemon-iv-payload3",
				},
				webhookMessage{
					Type:    "pokemon",
					Message: "pokemon-noiv-payload1",
				},
			},
		) {
			t.Fatalf("unexpected payload for everythingAreaServer: %+v", payloads)
		}

		payloads = raidPokemonIVAreaServer.GetPayloads()
		if !reflect.DeepEqual(
			payloads,
			[]webhookMessage{
				webhookMessage{
					Type:    "raid",
					Message: "pokemon-raid-payload1",
				},
				webhookMessage{
					Type:    "pokemon",
					Message: "pokemon-iv-payload1",
				},
				webhookMessage{
					Type:    "pokemon",
					Message: "pokemon-iv-payload3",
				},
			},
		) {
			t.Fatalf("unexpected payload for raidPokemonIVAreaServer: %+v", payloads)
		}

		payloads = raidInvasionPokemonNoIVAreaServer.GetPayloads()
		if !reflect.DeepEqual(
			payloads,
			[]webhookMessage{
				webhookMessage{
					Type:    "invasion",
					Message: "pokemon-invasion-payload2",
				},
				webhookMessage{
					Type:    "invasion",
					Message: "pokemon-invasion-payload3",
				},
				webhookMessage{
					Type:    "pokemon",
					Message: "pokemon-noiv-payload2",
				},
			},
		) {
			t.Fatalf("unexpected payload for raidInvasionPokemonNoIVAreaServer: %+v", payloads)
		}
	}

	addMessages()
	time.Sleep(150 * time.Millisecond)
	comparePayloads()

	// shutdown sender
	cancelFn()
	wg.Wait()

	// re-add messages while sender is shutdown
	addMessages()
	time.Sleep(150 * time.Millisecond)

	if len(allTypesAndAreasServer.GetPayloads()) != 0 {
		t.Fatal("payloads received after server shut down unexpectedly")
	}

	// flush should flush them all
	sender.Flush()
	comparePayloads()
}
