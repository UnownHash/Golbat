package external

import (
	"github.com/pyroscope-io/client/pyroscope"
	log "github.com/sirupsen/logrus"
	"golbat/config"
	"os"
	"runtime"
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

		if config.Config.Pyroscope.ApiKey != "" {
			pyroscopeConfig.AuthToken = config.Config.Pyroscope.ApiKey
		}

		_, err := pyroscope.Start(pyroscopeConfig)

		if err != nil {
			log.Errorf("Pyroscope Init Failed: %s", err)
		}
	}
}
