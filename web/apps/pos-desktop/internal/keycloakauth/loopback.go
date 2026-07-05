package keycloakauth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

// CallbackResult is the code+state pair received on the loopback redirect
// URI.
type CallbackResult struct {
	Code  string
	State string
}

// ErrCallbackTimeout is returned by Wait when no callback arrives before the
// given timeout elapses — e.g. the cashier closed the browser tab or never
// completed the Keycloak login form.
var ErrCallbackTimeout = errors.New("keycloakauth: no callback received before timeout")

// successPage is shown in the system browser once the callback is received.
// It carries no session data whatsoever — the actual token exchange happens
// separately, in Go, after Wait returns (see Client.Exchange). This is
// static HTML, not a redirect back into the app: there is nothing for a
// native app to receive here beyond the loopback request itself (RFC 8252
// §7.3 — no custom URI scheme handoff needed for a desktop app that already
// owns the listener).
const successPage = `<!DOCTYPE html>
<html lang="tr"><head><meta charset="utf-8"><title>onlinemenu.tr POS</title></head>
<body style="font-family:sans-serif;text-align:center;padding-top:4rem">
<h1>Giriş tamamlandı</h1>
<p>Uygulamaya dönebilirsiniz, bu sekmeyi kapatabilirsiniz.</p>
</body></html>`

const errorPage = `<!DOCTYPE html>
<html lang="tr"><head><meta charset="utf-8"><title>onlinemenu.tr POS</title></head>
<body style="font-family:sans-serif;text-align:center;padding-top:4rem">
<h1>Giriş başarısız</h1>
<p>Uygulamaya dönüp tekrar deneyin.</p>
</body></html>`

// LoopbackServer is a one-shot HTTP listener bound to 127.0.0.1 (never
// "localhost" — the "pos-desktop" client's registered redirect URI is the
// literal wildcard "http://127.0.0.1:*/callback"; "localhost" can resolve
// to ::1 on some hosts and would not match it). It accepts exactly one
// /callback request, then shuts itself down — see Wait.
type LoopbackServer struct {
	ln  net.Listener
	srv *http.Server

	once     sync.Once
	resultCh chan CallbackResult
	errCh    chan error

	closeOnce sync.Once
	wg        sync.WaitGroup
}

// NewLoopbackServer binds 127.0.0.1:0 (OS-assigned ephemeral port) and
// starts serving in the background. Call Wait to block for the callback (it
// always tears the listener down before returning, on every exit path).
func NewLoopbackServer() (*LoopbackServer, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("keycloakauth: bind loopback listener: %w", err)
	}

	s := &LoopbackServer{
		ln:       ln,
		resultCh: make(chan CallbackResult, 1),
		errCh:    make(chan error, 1),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", s.handleCallback)
	s.srv = &http.Server{
		Handler: mux,
		// Defense in depth even though this listener only accepts one
		// request from the local system browser and self-shuts-down
		// immediately after (see Wait/Close) — an unbounded
		// ReadHeaderTimeout is a slowloris footgun gosec (G112) rightly
		// flags regardless of how short-lived the server is.
		ReadHeaderTimeout: 5 * time.Second,
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		// http.ErrServerClosed is the expected exit once Close/Shutdown is
		// called — not a real failure.
		if err := s.srv.Serve(s.ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			select {
			case s.errCh <- fmt.Errorf("keycloakauth: loopback server: %w", err):
			default:
			}
		}
	}()

	return s, nil
}

// Port returns the OS-assigned loopback port.
func (s *LoopbackServer) Port() int {
	return s.ln.Addr().(*net.TCPAddr).Port
}

// RedirectURI is the exact redirect_uri to use in the authorize request —
// it matches the "pos-desktop" client's "http://127.0.0.1:*/callback"
// wildcard redirect URI registration.
func (s *LoopbackServer) RedirectURI() string {
	return fmt.Sprintf("http://127.0.0.1:%d/callback", s.Port())
}

func (s *LoopbackServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	if errCode := q.Get("error"); errCode != "" {
		desc := q.Get("error_description")
		if desc == "" {
			desc = errCode
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(errorPage))
		s.once.Do(func() {
			s.errCh <- fmt.Errorf("keycloakauth: authorization error: %s", desc)
		})
		return
	}

	code := q.Get("code")
	state := q.Get("state")
	if code == "" || state == "" {
		http.Error(w, "missing code/state", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(successPage))

	s.once.Do(func() {
		s.resultCh <- CallbackResult{Code: code, State: state}
	})
}

// Wait blocks until the callback fires, the timeout elapses, or ctx is
// cancelled — whichever comes first — and always shuts the listener down
// before returning (Close is idempotent and safe to call again by the
// caller). By the time Wait returns, the server's Serve goroutine is
// guaranteed to have exited (see Close) — verified with goleak in
// loopback_test.go, mirroring internal/hardware's convention.
func (s *LoopbackServer) Wait(ctx context.Context, timeout time.Duration) (CallbackResult, error) {
	defer s.Close()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case res := <-s.resultCh:
		return res, nil
	case err := <-s.errCh:
		return CallbackResult{}, err
	case <-timer.C:
		return CallbackResult{}, ErrCallbackTimeout
	case <-ctx.Done():
		return CallbackResult{}, ctx.Err()
	}
}

// Close shuts down the HTTP server and blocks until its Serve goroutine has
// fully exited. Idempotent — safe to call from Wait and again by the
// caller.
func (s *LoopbackServer) Close() {
	s.closeOnce.Do(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.srv.Shutdown(shutdownCtx)
	})
	s.wg.Wait()
}
