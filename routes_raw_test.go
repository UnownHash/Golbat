package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"golbat/config"
)

func TestRawRejectsWrongBearerWith401(t *testing.T) {
	prev := config.Config.RawBearer
	config.Config.RawBearer = "right"
	t.Cleanup(func() { config.Config.RawBearer = prev })

	gin.SetMode(gin.TestMode)
	for _, header := range []string{"", "Bearer wrong", "right"} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/raw", strings.NewReader(`{"contents":[]}`))
		if header != "" {
			c.Request.Header.Set("Authorization", header)
		}
		Raw(c)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("Authorization %q: status %d, want 401", header, w.Code)
		}
	}
}

func TestRawTimestampMsAcceptsJsonNumbers(t *testing.T) {
	var body map[string]interface{}
	if err := json.Unmarshal([]byte(`{"timestamp_ms":1731580800123}`), &body); err != nil {
		t.Fatal(err)
	}
	if ts, ok := rawTimestampMs(body["timestamp_ms"]); !ok || ts != 1731580800123 {
		t.Errorf("rawTimestampMs = %d, %v; want 1731580800123, true", ts, ok)
	}
	for _, v := range []interface{}{nil, "1731580800123", float64(0), float64(-5)} {
		if _, ok := rawTimestampMs(v); ok {
			t.Errorf("rawTimestampMs(%#v) accepted; want rejected", v)
		}
	}
}
