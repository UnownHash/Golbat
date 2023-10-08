//go:build unix

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	log "github.com/sirupsen/logrus"
)

// watchForShutdown blocks waiting for a SIGINT or SIGTERM signal.(or
// When received, cancelFn will be called and this function will return.
// The context `ctx` being cancelled will also cause this function to return.
func watchForShutdown(ctx context.Context, cancelFn context.CancelFunc) {
	defer cancelFn()

	sig_ch := make(chan os.Signal, 1)
	signal.Notify(sig_ch, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-ctx.Done():
		// something else told us to exit
	case sig := <-sig_ch:
		log.Infof("received signal '%s'", sig)
	}
}
