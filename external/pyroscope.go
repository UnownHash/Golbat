package external

import (
	"golbat/config"
	"os"
	"runtime"

	"github.com/grafana/pyroscope-go"
	log "github.com/sirupsen/logrus"
)

func InitPyroscope() {
	if config.Config.Pyroscope.ServerAddress != "" {
		log.Infof("Pyroscope starting")

		runtime.SetMutexProfileFraction(config.Config.Pyroscope.MutexProfileFraction)
		runtime.SetBlockProfileRate(config.Config.Pyroscope.BlockProfileRate)

		pyroscopeConfig := pyroscope.Config{
			ApplicationName: config.Config.Pyroscope.ApplicationName,
			ServerAddress:   config.Config.Pyroscope.ServerAddress,
			Tags:            map[string]string{"hostname": os.Getenv("HOSTNAME")},
			ProfileTypes: []pyroscope.ProfileType{
				pyroscope.ProfileCPU,
				pyroscope.ProfileAllocObjects,
				pyroscope.ProfileAllocSpace,
				pyroscope.ProfileInuseObjects,
				pyroscope.ProfileInuseSpace,

				pyroscope.ProfileGoroutines,
				pyroscope.ProfileMutexCount,
				pyroscope.ProfileMutexDuration,
				pyroscope.ProfileBlockCount,
				pyroscope.ProfileBlockDuration,
			},
		}

		if config.Config.Pyroscope.Logger {
			pyroscopeConfig.Logger = pyroscope.StandardLogger
		} else {
			pyroscopeConfig.Logger = nil
		}

		if apiKey := config.Config.Pyroscope.ApiKey; apiKey != "" {
			pyroscopeConfig.HTTPHeaders = map[string]string{
				"Authorization": "Bearer " + apiKey,
			}
		} else if basicAuthUser := config.Config.Pyroscope.BasicAuthUser; basicAuthUser != "" {
			pyroscopeConfig.BasicAuthUser = basicAuthUser
			pyroscopeConfig.BasicAuthPassword = config.Config.Pyroscope.BasicAuthPassword
		}

		_, err := pyroscope.Start(pyroscopeConfig)

		if err != nil {
			log.Errorf("Pyroscope Init Failed: %s", err)
		}
	}
}
