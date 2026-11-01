package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/oauth2"
	adminv1alpha "google.golang.org/api/analyticsadmin/v1alpha"
	adminv1beta "google.golang.org/api/analyticsadmin/v1beta"
	"google.golang.org/api/analyticsdata/v1beta"
	"google.golang.org/api/option"
)

const (
	readOnlyScope = "https://www.googleapis.com/auth/analytics.readonly"
	editScope     = "https://www.googleapis.com/auth/analytics.edit"
)

type clients struct {
	admin      *adminv1beta.Service
	adminAlpha *adminv1alpha.Service
	data       *analyticsdata.Service
	http       *http.Client
}

func newClients(ctx context.Context, scopes []string, cfg authConfig) (*clients, error) {
	src, err := authenticate(ctx, scopes, cfg)
	if err != nil {
		return nil, err
	}
	opt := option.WithTokenSource(src)
	base := context.WithValue(ctx, oauth2.HTTPClient, &http.Client{Timeout: 120 * time.Second})

	admin, err := adminv1beta.NewService(base, opt)
	if err != nil {
		return nil, err
	}
	adminAlpha, err := adminv1alpha.NewService(base, opt)
	if err != nil {
		return nil, err
	}
	data, err := analyticsdata.NewService(base, opt)
	if err != nil {
		return nil, err
	}

	return &clients{
		admin:      admin,
		adminAlpha: adminAlpha,
		data:       data,
		http:       oauth2.NewClient(base, src),
	}, nil
}

func main() {
	allowWrite := flag.Bool("allow-write", false, "enable write tools (requires the analytics.edit scope)")
	stateless := flag.Bool("stateless", os.Getenv("MCP_STATELESS") == "1", "serve without MCP sessions (Cloud Run friendly)")
	addr := flag.String("addr", defaultAddr(), "listen address")
	auth := flag.String("auth", envOr("GA4MCP_AUTH", "adc"), "credential mode: adc or oauth")
	clientID := flag.String("oauth-client-id", os.Getenv("GA4MCP_CLIENT_ID"), "OAuth client ID (with -auth oauth)")
	clientSecret := flag.String("oauth-client-secret", os.Getenv("GA4MCP_CLIENT_SECRET"), "OAuth client secret (with -auth oauth)")
	redirectURL := flag.String("oauth-redirect-url", envOr("GA4MCP_REDIRECT_URL", ""), "OAuth callback URL (with -auth oauth); defaults to http://<loopback listen addr>/callback")
	tokenFile := flag.String("token-file", envOr("GA4MCP_TOKEN_FILE", defaultTokenFile()), "where the OAuth token is cached")
	authToken := flag.String("auth-token", os.Getenv("MCP_AUTH_TOKEN"), "bearer token clients must send to /mcp; empty leaves the endpoint open")
	healthcheck := flag.Bool("healthcheck", false, "probe the local /healthz endpoint and exit")
	flag.Parse()

	if *healthcheck {
		os.Exit(probeHealthz(loopbackHost(*addr)))
	}

	scopes := []string{readOnlyScope}
	if *allowWrite {
		scopes = append(scopes, editScope)
	}

	cfg := authConfig{
		Mode:         *auth,
		ClientID:     *clientID,
		ClientSecret: *clientSecret,
		TokenFile:    *tokenFile,
		Listen:       *addr,
		RedirectURL:  *redirectURL,
	}

	ctx := context.Background()
	srv, err := newServer(ctx, scopes, cfg)
	if err != nil {
		slog.Error("init server", "err", err)
		os.Exit(1)
	}

	handler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{Stateless: *stateless, JSONResponse: true},
	)

	mcpHandler := acceptBothMediaTypes(handler)

	mux := http.NewServeMux()
	mux.Handle("/mcp", bearerAuth(*authToken, mcpHandler))
	mux.Handle("/mcp/", bearerAuth(*authToken, mcpHandler))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	slog.Info("listening", "addr", *addr, "auth", cfg.Mode, "stateless", *stateless, "bearer_auth", *authToken != "")
	server := &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("serve", "err", err)
		os.Exit(1)
	}
}

// acceptBothMediaTypes rewrites Accept so the streamable transport accepts the
// request. The transport demands both application/json and text/event-stream,
// while responses are JSON here, so a client that sends neither or only one of
// them means the same thing.
func acceptBothMediaTypes(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("Accept", "application/json, text/event-stream")
		next.ServeHTTP(w, r)
	})
}

func probeHealthz(hostport string) int {
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get("http://" + hostport + "/healthz")
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

func defaultAddr() string {
	return envOr("PORT_ADDR", ":"+port())
}

func port() string {
	if p := os.Getenv("PORT"); p != "" {
		return p
	}
	return "8080"
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
