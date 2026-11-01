package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const tokenFileRelPath = ".config/ga4mcp/token.json"

type authConfig struct {
	Mode         string
	ClientID     string
	ClientSecret string
	TokenFile    string
	Listen       string
	RedirectURL  string
}

func authenticate(ctx context.Context, scopes []string, cfg authConfig) (oauth2.TokenSource, error) {
	switch cfg.Mode {
	case "", "adc":
		creds, err := google.FindDefaultCredentials(ctx, scopes...)
		if err != nil {
			return nil, fmt.Errorf("load default credentials: %w", err)
		}
		return creds.TokenSource, nil
	case "oauth":
		return oauthTokenSource(ctx, scopes, cfg)
	default:
		return nil, fmt.Errorf("unknown -auth mode %q: use adc or oauth", cfg.Mode)
	}
}

func oauthTokenSource(ctx context.Context, scopes []string, cfg authConfig) (oauth2.TokenSource, error) {
	if cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil, errors.New("-auth oauth requires -oauth-client-id and -oauth-client-secret")
	}
	path, err := expandHome(cfg.TokenFile)
	if err != nil {
		return nil, err
	}

	conf := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		Endpoint:     google.Endpoint,
		RedirectURL:  resolveRedirectURL(cfg.RedirectURL, cfg.Listen),
		Scopes:       scopes,
	}

	if stored, err := readToken(path); err == nil {
		src := oauth2.ReuseTokenSource(stored, conf.TokenSource(ctx, stored))
		if _, err := src.Token(); err == nil {
			slog.Info("oauth token valid, skipping login", "file", path)
			return newPersistingSource(path, src), nil
		} else {
			slog.Warn("stored oauth token rejected, starting login", "file", path, "err", err)
		}
	}

	tok, err := loginInteractive(ctx, conf, cfg.Listen)
	if err != nil {
		return nil, err
	}
	if err := writeToken(path, tok); err != nil {
		return nil, err
	}
	slog.Info("oauth token saved", "file", path)
	return newPersistingSource(path, oauth2.ReuseTokenSource(tok, conf.TokenSource(ctx, tok))), nil
}

// loginInteractive runs the authorization-code flow with PKCE and waits for the
// browser redirect on a loopback listener.
func loginInteractive(ctx context.Context, conf *oauth2.Config, listen string) (*oauth2.Token, error) {
	state := randomHex(16)
	verifier := oauth2.GenerateVerifier()
	authURL := conf.AuthCodeURL(state,
		oauth2.AccessTypeOffline,
		oauth2.ApprovalForce,
		oauth2.S256ChallengeOption(verifier),
	)

	type result struct {
		token *oauth2.Token
		err   error
	}
	done := make(chan result, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != state {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			return
		}
		if e := r.URL.Query().Get("error"); e != "" {
			http.Error(w, e, http.StatusBadRequest)
			done <- result{err: fmt.Errorf("authorization denied: %s", e)}
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			done <- result{err: errors.New("missing authorization code")}
			return
		}
		tok, err := conf.Exchange(context.WithoutCancel(r.Context()), code, oauth2.VerifierOption(verifier))
		if err != nil {
			http.Error(w, "token exchange failed", http.StatusBadGateway)
			done <- result{err: err}
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, "Authorization complete. You can close this tab and return to the terminal.")
		done <- result{token: tok}
	})

	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", listen, err)
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			done <- result{err: err}
		}
	}()
	defer srv.Close()

	fmt.Fprintf(os.Stderr, "\nOpen this URL to authorize Google Analytics access:\n\n%s\n\n", authURL)

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-done:
		return r.token, r.err
	}
}

type persistingSource struct {
	mu     sync.Mutex
	path   string
	source oauth2.TokenSource
	last   string
}

func newPersistingSource(path string, src oauth2.TokenSource) oauth2.TokenSource {
	return &persistingSource{path: path, source: src}
}

func (p *persistingSource) Token() (*oauth2.Token, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	tok, err := p.source.Token()
	if err != nil {
		return nil, err
	}
	if tok.AccessToken != p.last {
		if err := writeToken(p.path, tok); err != nil {
			slog.Warn("persist refreshed token", "file", p.path, "err", err)
		}
		p.last = tok.AccessToken
	}
	return tok, nil
}

func readToken(path string) (*oauth2.Token, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var tok oauth2.Token
	if err := json.Unmarshal(data, &tok); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if tok.RefreshToken == "" && tok.AccessToken == "" {
		return nil, fmt.Errorf("%s holds no usable token", path)
	}
	return &tok, nil
}

func writeToken(path string, tok *oauth2.Token) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(tok, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func expandHome(path string) (string, error) {
	if !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, path[2:]), nil
}

func defaultTokenFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "token.json"
	}
	return filepath.Join(home, tokenFileRelPath)
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func hasScope(scopes []string, want string) bool {
	return slices.Contains(scopes, want)
}

// resolveRedirectURL builds the OAuth callback URL. An explicit redirect URL
// wins; otherwise the listen address is used, and Google's desktop clients
// require a loopback IP there, so an all-interfaces listen address maps to
// 127.0.0.1.
func resolveRedirectURL(redirect, listen string) string {
	if redirect != "" {
		return redirect
	}
	return "http://" + loopbackHost(listen) + "/callback"
}

func loopbackHost(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "127.0.0.1" + listen
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

// bearerAuth guards the MCP endpoint with a shared secret. An empty token
// disables the check, which is only safe when the listener is loopback-only.
func bearerAuth(token string, next http.Handler) http.Handler {
	if token == "" {
		slog.Warn("MCP_AUTH_TOKEN is unset, the MCP endpoint is unauthenticated")
		return next
	}
	want := sha256.Sum256([]byte(token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := sha256.Sum256([]byte(bearerFrom(r)))
		if subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="ga4mcp"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func bearerFrom(r *http.Request) string {
	scheme, token, ok := strings.Cut(strings.TrimSpace(r.Header.Get("Authorization")), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return token
}
