package main

import (
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"
)

var (
	gitRevision string
	gitModified bool
	buildTime   string
	goVersion   string
)

func init() {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	goVersion = info.GoVersion
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			gitRevision = s.Value
		case "vcs.modified":
			gitModified = s.Value == "true"
		case "vcs.time":
			buildTime = s.Value
		}
	}
}

func GetVersion(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"revision":   gitRevision,
		"modified":   gitModified,
		"build_time": buildTime,
		"go_version": goVersion,
	})
}
