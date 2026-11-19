package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"context"
	"github.com/gin-gonic/gin"
	"golbat/config"
	pb "golbat/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"net/http/httptest"

	log "github.com/sirupsen/logrus"
)

func TestRawAdmission(t *testing.T) {
	old, bearer := rawProcessingQueue, config.Config.RawBearer
	q := newRawQueue(1, 1)
	rawProcessingQueue, config.Config.RawBearer = q, ""
	defer func() { rawProcessingQueue, config.Config.RawBearer = old, bearer; close(q.jobs) }()
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	if !submitRaw(1, func() { close(started); <-release }) {
		t.Fatal("first job rejected")
	}
	<-started
	if submitRaw(rawQueueBytes+1, func() {}) {
		t.Fatal("oversized queued payload accepted")
	}
	if !submitRaw(4, func() { close(done) }) {
		t.Fatal("waiting job rejected")
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/raw", strings.NewReader(`{"contents":[]}`))
	Raw(c)
	if w.Code != 429 {
		t.Fatalf("full HTTP queue returned %d", w.Code)
	}
	server := grpcRawServer{}
	_, err := server.SubmitRawProto(context.Background(), &pb.RawProtoRequest{})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("full RPC queue returned %v", err)
	}
	if q.bytes.Load() != 4 {
		t.Fatal("rejected jobs consumed budget")
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("queued job did not run")
	}
	if q.bytes.Load() != 0 {
		t.Fatal("queue budget leaked")
	}
	resp, err := server.SubmitRawProto(context.Background(), &pb.RawProtoRequest{})
	if err != nil || resp.Message != "Processed" {
		t.Fatal("admission did not recover", err)
	}
}

func TestUnlimitedRawAdmission(t *testing.T) {
	old := rawProcessingQueue
	rawProcessingQueue = nil
	defer func() { rawProcessingQueue = old }()
	done := make(chan struct{})
	if !submitRaw(1, func() { close(done) }) {
		t.Fatal("unlimited mode rejected request")
	}
	<-done
}

// Slow slot waits are logged once per second with a count and the longest
// wait, not once per packet.
func TestSlowSlotWaitsAggregate(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	slowSlotWaits.Reset()
	slowSlotWaitMax.Store(0)

	reportSlowSlotWait(2*time.Second, 64)
	reportSlowSlotWait(5*time.Second, 64)
	reportSlowSlotWait(3*time.Second, 64)
	if lines := strings.Count(buf.String(), "[RAW_LIMITER]"); lines != 1 {
		t.Fatalf("got %d log lines within one second, want 1:\n%s", lines, buf.String())
	}

	time.Sleep(1100 * time.Millisecond)
	buf.Reset()
	reportSlowSlotWait(4*time.Second, 64)
	got := buf.String()
	if !strings.Contains(got, "3 packets") || !strings.Contains(got, "longest 5s") {
		t.Fatalf("second window should report the 3 suppressed waits and the longest of them, got:\n%s", got)
	}
}
