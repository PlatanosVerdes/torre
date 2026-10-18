package telegram

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNothingIsSentWithoutAToken(t *testing.T) {
	if err := New("", "").Notify(context.Background(), "hola"); err != nil {
		t.Errorf("an unconfigured bot returned an error instead of staying quiet: %v", err)
	}
}

func TestErrorsDoNotCarryTheToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	}))
	bot := New("secret-token", "chat")
	bot.APIURL = srv.URL + "/bot"
	err := bot.Notify(context.Background(), "hola")
	srv.Close()
	if err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("answered error: %v", err)
	}

	err = bot.Notify(context.Background(), "hola")
	if err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("transport error: %v", err)
	}
}
