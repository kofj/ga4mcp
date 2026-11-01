# ga4mcp

Google Analytics 4 MCP server in Go, speaking streamable HTTP.

## Run

Default mode uses ADC (service account or `gcloud auth application-default login`):

```bash
gcloud auth application-default login \
  --scopes https://www.googleapis.com/auth/analytics.readonly,https://www.googleapis.com/auth/cloud-platform

go run . -addr :8080
```

OAuth mode signs in as a real user and caches the token on disk, so the login
URL is only printed when there is no usable token yet:

```bash
go run . -auth oauth \
  -oauth-client-id  $GA4MCP_CLIENT_ID \
  -oauth-client-secret $GA4MCP_CLIENT_SECRET \
  -token-file ~/.config/ga4mcp/token.json
```

First run prints an authorization URL and waits on `http://127.0.0.1:<port>/callback`.
Set `-oauth-redirect-url` (or `GA4MCP_REDIRECT_URL`) when the callback the browser
can reach differs from the loopback listen address, and make sure the same URL is
registered on the OAuth client.
Later runs reuse the cached token; a rejected refresh token falls back to login.
Add `-allow-write` to request `analytics.edit` and expose `create_property_annotation`.

`PORT` overrides `-addr` for Cloud Run. `-stateless` (or `MCP_STATELESS=1`) drops
MCP sessions so any instance can serve any request.

## Endpoint auth

`-auth-token` (or `MCP_AUTH_TOKEN`) requires `Authorization: Bearer <token>` on
`/mcp`. It is independent of the Google credential mode: the server still holds
one Google identity, so this only keeps the endpoint from being reached by
anyone who can route to it. Leave it unset only when the listener is loopback-only.
`/healthz` is never guarded.

## Wire it into Claude Code

```bash
claude mcp add --transport http ga4 http://localhost:8080/mcp \
  --header "Authorization: Bearer $MCP_AUTH_TOKEN"
```

## Docker

Credentials come from `~/.config/ga4mcp/.env`; copy `.env.example` there and
fill in the client id, client secret and `MCP_AUTH_TOKEN`. Then:

```bash
task build   # docker build, tagged with the git describe version
task start   # build, then docker compose up -d
task logs
task stop
```

The image is built as `ga4mcp:<git describe output>` plus `ga4mcp:latest`.
The container runs as `GA4MCP_UID:GA4MCP_GID` (default `1000:1000`) and mounts
the host's `~/.config/ga4mcp` at `/data`, so `token.json` survives restarts and
refreshes are written back to the host.

That directory must already contain a token — the interactive flow needs a
loopback browser that a container cannot provide, so sign in on the host first.
Running the binary locally reads the same `.env` the container does:

```bash
set -a; . ~/.config/ga4mcp/.env; set +a
go run . -auth oauth -oauth-client-id "$GA4MCP_CLIENT_ID" \
  -oauth-client-secret "$GA4MCP_CLIENT_SECRET" \
  -token-file ~/.config/ga4mcp/token.json -auth-token "$MCP_AUTH_TOKEN"
```

If the host directory is owned by a different uid, set `GA4MCP_UID`/`GA4MCP_GID`
to `id -u`/`id -g`. `CONFIG_DIR` overrides where `.env` and `token.json` are
read from.

Compose reads that `.env` twice: through `env_file` for the container, and
through `--env-file` so `${GA4MCP_PORT}` and friends interpolate. Calling
`docker compose` by hand without `--env-file` leaves the port at 8080.

## Tools

| Tool | API |
|---|---|
| get_account_summaries / get_property_details / list_google_ads_links | Admin v1beta |
| list_property_annotations / create_property_annotation | Admin v1alpha |
| get_custom_dimensions_and_metrics / run_report / run_realtime_report | Data v1beta |
| run_funnel_report / run_conversions_report | Data v1alpha (REST) |

Report tools take Data API payload shapes directly (`dimensions: [{"name":"country"}]`),
so the schema matches the REST reference and the Python server's arguments.
