package external

import (
	"github.com/getsentry/sentry-go"
	log "github.com/sirupsen/logrus"
	"golbat/config"
)

func InitSentry() {
	if config.Config.Sentry.DSN != "" {
		log.Infof("Sentry init")

		err := sentry.Init(sentry.ClientOptions{
			Dsn:              config.Config.Sentry.DSN,
			Debug:            false,
			EnableTracing:    config.Config.Sentry.EnableTracing,
			TracesSampleRate: config.Config.Sentry.TracesSampleRate,
			SampleRate:       config.Config.Sentry.SampleRate,
		})
		if err != nil {
			log.Errorf("Sentry Init Failed: %s", err)
		}
	}
}
