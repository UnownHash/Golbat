package webhooks

import (
	"bytes"
	log "github.com/sirupsen/logrus"
	"net/http"
	"time"
)

type WebhookQueue struct {
	url     string
	webhook []byte
}

func StartSender() {
	ticker := time.NewTicker(1 * time.Second)
	go func() {
		for {
			<-ticker.C
			hooks := collectHooks()
			for _, hookContents := range hooks {
				go sendWebhooks(hookContents)
			}
		}
	}()
}

func sendWebhooks(queue WebhookQueue) {
	req, err := http.NewRequest("POST", queue.url, bytes.NewBuffer(queue.webhook))

	if err != nil {
		log.Warnf("Webhook: unable to connect to %s - %s", queue.url, err)
		return
	}

	req.Header.Set("X-Golbat", "hey!")
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Warningf("Webhook: %s", err)
		return
	}
	defer resp.Body.Close()

	log.Debugf("Webhook: Response %s", resp.Status)
	//fmt.Println("response Status:", resp.Status)
	//fmt.Println("response Headers:", resp.Header)
	//body, _ := ioutil.ReadAll(resp.Body)
	//fmt.Println("response Body:", string(body))
}
