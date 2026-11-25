package fetch

import (
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func trustServer(t *testing.T, srv *httptest.Server) {
	t.Helper()
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	tlsRoots = pool
	t.Cleanup(func() { tlsRoots = nil })
}

// A document up to MaxBodyBytes is read; one byte more is refused, never
// read whole.
func TestFetchBoundsTheBody(t *testing.T) {
	var size int
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write([]byte(strings.Repeat("a", size))); err != nil {
			t.Errorf("w.Write: %v", err)
		}
	}))
	defer srv.Close()
	trustServer(t, srv)
	size = MaxBodyBytes
	if body, err := Fetch(srv.URL+"/doc.yaml", "tok"); err != nil || len(body) != MaxBodyBytes {
		t.Fatalf("a %d-byte document: %d bytes, %v", MaxBodyBytes, len(body), err)
	}
	size = MaxBodyBytes + 1
	if body, err := Fetch(srv.URL+"/doc.yaml", "tok"); err == nil {
		t.Fatalf("a %d-byte response was read (%d bytes)", MaxBodyBytes+1, len(body))
	}
}

// A redirect is followed only where the token stays off the network in the
// clear: to https, never to plain http on another host.
func TestFetchRefusesARedirectToPlainHTTP(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/moved" {
			if _, err := w.Write([]byte("name: doc\n")); err != nil {
				t.Errorf("w.Write: %v", err)
			}
			return
		}
		if r.URL.Path == "/down" {
			http.Redirect(w, r, "http://192.0.2.1:8080/doc.yaml", http.StatusFound)
			return
		}
		http.Redirect(w, r, "/moved", http.StatusFound)
	}))
	defer srv.Close()
	trustServer(t, srv)
	if body, err := Fetch(srv.URL+"/doc.yaml", "tok"); err != nil || string(body) != "name: doc\n" {
		t.Fatalf("an https redirect: %q, %v", body, err)
	}
	start := time.Now()
	if _, err := Fetch(srv.URL+"/down", "tok"); err == nil || !strings.Contains(err.Error(), "refusing a redirect") {
		t.Fatalf("a redirect to plain http off this machine: %v, want refused", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("the refusal took %v: the redirect was followed", d)
	}
}
