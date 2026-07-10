package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func guardedEcho(t *testing.T) http.Handler {
	t.Helper()
	return originGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
}

func TestOriginGuardAllowsLoopbackGET(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "http://127.0.0.1:7681/tab/self-check", nil)
	guardedEcho(t).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("loopback GET: got %d, want 200", rr.Code)
	}
}

func TestOriginGuardRejectsForeignHost(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "http://evil.example.com/tab/self-check", nil)
	req.Host = "evil.example.com"
	guardedEcho(t).ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("foreign Host: got %d, want 403", rr.Code)
	}
}

func TestOriginGuardRejectsForeignOriginOnPOST(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "http://127.0.0.1:7681/api/runs", strings.NewReader("{}"))
	req.Header.Set("Origin", "https://evil.example.com")
	guardedEcho(t).ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("foreign Origin POST: got %d, want 403", rr.Code)
	}
}

func TestOriginGuardAllowsLoopbackOriginOnPOST(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "http://127.0.0.1:7681/api/runs", strings.NewReader("{}"))
	req.Header.Set("Origin", "http://127.0.0.1:7681")
	guardedEcho(t).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("loopback Origin POST: got %d, want 200", rr.Code)
	}
}

func TestOriginGuardAllowsAbsentOriginOnPOST(t *testing.T) {
	// curl / CLI clients send no Origin; must keep working.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "http://localhost:7681/api/runs", strings.NewReader("{}"))
	guardedEcho(t).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("absent Origin POST: got %d, want 200", rr.Code)
	}
}

func TestOriginGuardAllowsBracketedIPv6LoopbackHostNoPort(t *testing.T) {
	// Some clients send bracketed IPv6 Host without a port; net.SplitHostPort
	// errors on that form ("missing port in address"), so the guard must
	// still recognize "[::1]" as loopback after stripping brackets.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "http://[::1]/tab/self-check", nil)
	req.Host = "[::1]"
	guardedEcho(t).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("bracketed IPv6 loopback Host (no port): got %d, want 200", rr.Code)
	}
}

func TestOriginGuardRejectsMalformedOriginOnPOST(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "http://127.0.0.1:7681/api/runs", strings.NewReader("{}"))
	req.Header.Set("Origin", "://bad")
	guardedEcho(t).ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("malformed Origin POST: got %d, want 403", rr.Code)
	}
}
