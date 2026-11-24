package fetch

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A plain http:// URL to a host off this machine is refused before any
// request is made, so the authorization token never crosses a network in the
// clear. 192.0.2.1 is TEST-NET-1: a request there could only time out.
func TestFetchRefusesPlainHTTPOffThisMachine(t *testing.T) {
	start := time.Now()
	_, err := Fetch("http://192.0.2.1:8080/doc.yaml", "secret-token")
	if err == nil || !strings.Contains(err.Error(), "must be https://") {
		t.Fatalf("a plain http URL off this machine: %v, want refused for its scheme", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("the refusal took %v: a request was attempted", d)
	}
	for _, u := range []string{"ftp://host/doc", "doc.yaml", "https:///doc"} {
		if _, err := Fetch(u, ""); err == nil {
			t.Fatalf("%s was fetched", u)
		}
	}
}

// Plain http:// to a loopback host is fetched: its traffic never leaves the
// machine.
func TestFetchAdmitsPlainHTTPToLoopback(t *testing.T) {
	var auth atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth.Store(r.Header.Get("Authorization"))
		w.Write([]byte("name: doc\n"))
	}))
	defer srv.Close()
	body, err := Fetch(srv.URL+"/doc.yaml", "tok")
	if err != nil || string(body) != "name: doc\n" {
		t.Fatalf("a loopback http URL: %q, %v", body, err)
	}
	if got, _ := auth.Load().(string); got != "token tok" {
		t.Fatalf("the loopback server saw Authorization %q", got)
	}
}
