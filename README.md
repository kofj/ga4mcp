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

## Tools

| Tool | API |
|---|---|
| get_account_summaries / get_property_details / list_google_ads_links | Admin v1beta |
| list_property_annotations / create_property_annotation | Admin v1alpha |
| get_custom_dimensions_and_metrics / run_report / run_realtime_report | Data v1beta |
| run_funnel_report / run_conversions_report | Data v1alpha (REST) |

Report tools take Data API payload shapes directly (`dimensions: [{"name":"country"}]`),
so the schema matches the REST reference and the Python server's arguments.
