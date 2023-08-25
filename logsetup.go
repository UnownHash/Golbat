package main

import (
	"fmt"
	log "github.com/sirupsen/logrus"
	"gopkg.in/natefinch/lumberjack.v2"
	"golbat/config"
	"io"
	"os"
	"path/filepath"
)

var lumberjackLogger *lumberjack.Logger

func SetupLogger(logLevel log.Level, fileLoggingEnabled bool) {

	lumberjackLogger = &lumberjack.Logger{
		// Log file absolute path, os agnostic
		Filename:   filepath.ToSlash("logs/golbat.log"),
		MaxSize:    config.Config.Logging.MaxSize, // MB
		MaxBackups: config.Config.Logging.MaxBackups,
		MaxAge:     config.Config.Logging.MaxAge,   // days
		Compress:   config.Config.Logging.Compress, // disabled by default
	}

	var output io.Writer
	if fileLoggingEnabled {
		// Fork writing into two outputs
		output = io.MultiWriter(os.Stdout, lumberjackLogger)
	} else {
		output = os.Stdout
	}

	logFormatter := new(PlainFormatter)
	logFormatter.TimestampFormat = "2006-01-02 15:04:05"
	logFormatter.LevelDesc = []string{"PANC", "FATL", "ERRO", "WARN", "INFO", "DEBG"}

	log.SetFormatter(logFormatter)
	log.SetLevel(logLevel)
	log.SetOutput(output)
}

func RotateLogs() {
	if lumberjackLogger != nil {
		_ = lumberjackLogger.Rotate()
	}
}

type PlainFormatter struct {
	TimestampFormat string
	LevelDesc       []string
}

func (f *PlainFormatter) Format(entry *log.Entry) ([]byte, error) {
	timestamp := fmt.Sprintf(entry.Time.Format(f.TimestampFormat))
	return []byte(fmt.Sprintf("%s %s %s\n", f.LevelDesc[entry.Level], timestamp, entry.Message)), nil
}
