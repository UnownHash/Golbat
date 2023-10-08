//go:build !unix

package main

// If someone cares to implement it for Windows or some other non-unix, this function
// should block waiting for a shutdown signal and should call cancelFn() and return when
// one is receeived.
func watchForShutdown(ctx context.Context, cancelFn context.CancelFunc) {
}
