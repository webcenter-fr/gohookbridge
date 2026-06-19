package gohookbridge

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetNewHookURL_Success(t *testing.T) {
	expectedURL := "https://example.com/redirected"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("Expected GET request, got %s", r.Method)
		}
		fmt.Fprint(w, expectedURL)
	}))
	defer server.Close()

	output, err := GetNewHookURL(server.URL)
	if err != nil {
		t.Errorf("GetNewHookURL() error = %v", err)
	}
	if output != expectedURL {
		t.Errorf("GetNewHookURL() output = %q, want %q", output, expectedURL)
	}
}
