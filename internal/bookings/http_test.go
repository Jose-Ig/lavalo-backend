package bookings

import (
	"bytes"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"testing"
)

func TestHTTPReservationLifecycle(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s, i := fixture(t)
	r := gin.New()
	Register(r.Group("/api/v1"), s)
	body, _ := json.Marshal(i)
	request := httptest.NewRequest("POST", "/api/v1/reservations", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, request)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var result struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/reservations/1", nil))
	if w.Code != 404 {
		t.Fatalf("unauthorized: %d", w.Code)
	}
	req := httptest.NewRequest("GET", "/api/v1/reservations/1", nil)
	req.Header.Set("Authorization", "Bearer "+result.AccessToken)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("get: %d", w.Code)
	}
	req = httptest.NewRequest("DELETE", "/api/v1/reservations/1", nil)
	req.Header.Set("Authorization", "Bearer "+result.AccessToken)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 204 {
		t.Fatalf("cancel: %d", w.Code)
	}
}
