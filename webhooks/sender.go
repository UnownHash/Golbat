package webhooks

import (
	"context"
	"golbat/config"
	"golbat/geo"
	"sync"
	"time"
)

type configInterface interface {
	GetWebhooks() []config.Webhook
	GetWebhookInterval() time.Duration
}

type webhookCollection [webhookTypesLength]webhookList

type webhookMessage struct {
	Type    string         `json:"type"`
	Areas   []geo.AreaName `json:"-"`
	Message any            `json:"message"`
}

type webhookList struct {
	Messages []webhookMessage
}

func (webhookList *webhookList) AddMessage(message webhookMessage) {
	webhookList.Messages = append(webhookList.Messages, message)
}

type webhooksSender struct {
	mutex           sync.Mutex
	webhookInterval time.Duration

	collections webhookCollection
	webhooks    []*webhook
}

// this grabs the current collection of webhooks and resets the state such
// that new messages begin to be added to an empty collection. if a caller
// does nothing with the collection returned here, the collection is lost.
func (sender *webhooksSender) getCurrentCollection() webhookCollection {
	sender.mutex.Lock()
	current := sender.collections
	sender.collections = webhookCollection{}
	sender.mutex.Unlock()
	return current
}

func (sender *webhooksSender) AddMessage(wh_type WebhookType, message any, areas []geo.AreaName) {
	wh_message := webhookMessage{
		Type:    webhookTypeToPayloadType[wh_type],
		Areas:   areas,
		Message: message,
	}
	sender.mutex.Lock()
	sender.collections[wh_type].AddMessage(wh_message)
	sender.mutex.Unlock()
}

// Flush will send the collected webhooks. This is meant to be used after
// the web server has been shut down and before the program exits.
func (sender *webhooksSender) Flush() {
	var wg sync.WaitGroup

	currentCollection := sender.getCurrentCollection()
	for _, wh := range sender.webhooks {
		wg.Add(1)
		go func(wh *webhook) {
			defer wg.Done()
			wh.sendCollection(currentCollection)
		}(wh)
	}
	wg.Wait()
}

// Run will monitor the webhooks collection and send in bulk every 1s.
// This blocks until `ctx` is cancelled.
func (sender *webhooksSender) Run(ctx context.Context) error {
	ticker := time.NewTicker(sender.webhookInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			go sender.Flush()
		}
	}
}

func NewWebhooksSender(cfg configInterface) (*webhooksSender, error) {
	configWebhooks := cfg.GetWebhooks()
	webhooks := make([]*webhook, len(configWebhooks))
	for i, configWh := range configWebhooks {
		webhook, err := webhookFromConfigWebhook(configWh)
		if err != nil {
			return nil, err
		}
		webhooks[i] = webhook
	}

	interval := cfg.GetWebhookInterval()
	if interval <= 0 {
		interval = time.Second
	}

	sender := &webhooksSender{
		webhookInterval: interval,
		webhooks:        webhooks,
	}

	return sender, nil
}
