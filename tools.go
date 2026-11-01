package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	adminv1alpha "google.golang.org/api/analyticsadmin/v1alpha"
)

const dataAPIBase = "https://analyticsdata.googleapis.com/"

// reportCore mirrors the shared part of RunReportRequest. Request payloads are
// built as maps and sent to the REST surface directly, because the generated Go
// client does not expose runFunnelReport or the v1alpha conversions API.
type reportCore struct {
	PropertyID      string           `json:"property_id"`
	DateRanges      []map[string]any `json:"date_ranges,omitempty"`
	Dimensions      []map[string]any `json:"dimensions"`
	Metrics         []map[string]any `json:"metrics"`
	DimensionFilter map[string]any   `json:"dimension_filter,omitempty"`
	MetricFilter    map[string]any   `json:"metric_filter,omitempty"`
	OrderBys        []map[string]any `json:"order_bys,omitempty"`
	Limit           int64            `json:"limit,omitempty"`
	Offset          int64            `json:"offset,omitempty"`
	CurrencyCode    string           `json:"currency_code,omitempty"`
	ReturnQuota     bool             `json:"return_property_quota,omitempty"`
}

type reportArgs struct{ reportCore }

type realtimeArgs struct{ reportCore }

type funnelArgs struct {
	reportCore
	FunnelSteps      []map[string]any `json:"funnel_steps"`
	FunnelBreakdown  map[string]any   `json:"funnel_breakdown,omitempty"`
	FunnelNextAction map[string]any   `json:"funnel_next_action,omitempty"`
	Segments         []map[string]any `json:"segments,omitempty"`
}

type conversionsArgs struct {
	reportCore
	ConversionSpec map[string]any `json:"conversion_spec"`
}

type createAnnotationArgs struct {
	PropertyID  string `json:"property_id"`
	Title       string `json:"title"`
	StartDate   string `json:"start_date"`
	Color       string `json:"color,omitempty"`
	Description string `json:"description,omitempty"`
	EndDate     string `json:"end_date,omitempty"`
}

var (
	reportSchema = json.RawMessage(`{"type":"object","properties":{` + coreProps +
		`},"required":["property_id","date_ranges","dimensions","metrics"]}`)

	realtimeSchema = json.RawMessage(`{"type":"object","properties":{` + coreProps +
		`},"required":["property_id","dimensions","metrics"]}`)

	funnelSchema = json.RawMessage(`{"type":"object","properties":{` + coreProps +
		`,"funnel_steps":{"type":"array","items":{"type":"object"},"description":"Either {name, event} or {name, filter_expression}"},"funnel_breakdown":{"type":"object"},"funnel_next_action":{"type":"object"},"segments":{"type":"array","items":{"type":"object"}}` +
		`},"required":["property_id","funnel_steps"]}`)

	conversionsSchema = json.RawMessage(`{"type":"object","properties":{` + coreProps +
		`,"conversion_spec":{"type":"object","description":"{conversion_actions: [...], attribution_model: ...}"}` +
		`},"required":["property_id","date_ranges","dimensions","metrics","conversion_spec"]}`)

	annotationSchema = json.RawMessage(`{"type":"object","properties":{` +
		`"property_id":{"type":"string"},"title":{"type":"string"},"start_date":{"type":"string"},` +
		`"color":{"type":"string"},"description":{"type":"string"},"end_date":{"type":"string"}` +
		`},"required":["property_id","title","start_date"]}`)
)

// coreProps is the argument surface shared by every reporting tool. Only
// property_id, dimensions and metrics carry descriptions; the rest reuse the
// Data API JSON shapes verbatim so the payloads pass straight through.
const coreProps = `"property_id":{"type":"string","description":"GA4 property ID, either a number or 'properties/<number>'"},` +
	`"date_ranges":{"type":"array","items":{"type":"object"},"description":"[{start_date,end_date}] e.g. {start_date:'7daysAgo',end_date:'today'}"},` +
	`"dimensions":{"type":"array","items":{"type":"object"},"description":"[{name:'country'}]"},` +
	`"metrics":{"type":"array","items":{"type":"object"},"description":"[{name:'activeUsers'}]"},` +
	`"dimension_filter":{"type":"object"},"metric_filter":{"type":"object"},"order_bys":{"type":"array","items":{"type":"object"}},` +
	`"limit":{"type":"integer"},"offset":{"type":"integer"},"currency_code":{"type":"string"},"return_property_quota":{"type":"boolean"}`

func (r reportCore) body(withDateRanges bool) map[string]any {
	b := map[string]any{
		"dimensions": toAny(r.Dimensions),
		"metrics":    toAny(r.Metrics),
	}
	if withDateRanges {
		b["dateRanges"] = toAny(r.DateRanges)
	}
	if r.DimensionFilter != nil {
		b["dimensionFilter"] = r.DimensionFilter
	}
	if r.MetricFilter != nil {
		b["metricFilter"] = r.MetricFilter
	}
	if len(r.OrderBys) > 0 {
		b["orderBys"] = toAny(r.OrderBys)
	}
	if r.Limit > 0 {
		b["limit"] = r.Limit
	}
	if r.Offset > 0 {
		b["offset"] = r.Offset
	}
	if r.CurrencyCode != "" {
		b["currencyCode"] = r.CurrencyCode
	}
	if r.ReturnQuota {
		b["returnPropertyQuota"] = true
	}
	return b
}

func toAny(in []map[string]any) []any {
	out := make([]any, len(in))
	for i, v := range in {
		out[i] = v
	}
	return out
}

func (c *clients) runReport(ctx context.Context, a reportArgs) (any, error) {
	name, err := propertyName(a.PropertyID)
	if err != nil {
		return nil, err
	}
	return c.postDataAPI(ctx, "v1beta", name, "runReport", a.body(true))
}

func (c *clients) runRealtimeReport(ctx context.Context, a realtimeArgs) (any, error) {
	name, err := propertyName(a.PropertyID)
	if err != nil {
		return nil, err
	}
	return c.postDataAPI(ctx, "v1beta", name, "runRealtimeReport", a.body(false))
}

func (c *clients) runFunnelReport(ctx context.Context, a funnelArgs) (any, error) {
	name, err := propertyName(a.PropertyID)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"funnel": funnelBody(a.FunnelSteps)}
	if len(a.DateRanges) > 0 {
		body["dateRanges"] = toAny(a.DateRanges)
	}
	if a.FunnelBreakdown != nil {
		body["funnelBreakdown"] = a.FunnelBreakdown
	}
	if a.FunnelNextAction != nil {
		body["funnelNextAction"] = a.FunnelNextAction
	}
	if len(a.Segments) > 0 {
		body["segments"] = toAny(a.Segments)
	}
	if a.ReturnQuota {
		body["returnPropertyQuota"] = true
	}
	return c.postDataAPI(ctx, "v1alpha", name, "runFunnelReport", body)
}

// funnelBody accepts the {name, event} shorthand used by the Python server and
// expands it into the Data API's explicit step representation.
func funnelBody(steps []map[string]any) map[string]any {
	out := make([]any, 0, len(steps))
	for _, s := range steps {
		step := map[string]any{"name": s["name"]}
		if expr, ok := s["filter_expression"]; ok {
			step["filterExpression"] = expr
		} else if event, ok := s["event"]; ok {
			step["filterExpression"] = map[string]any{
				"filter": map[string]any{
					"fieldName":    "eventName",
					"stringFilter": map[string]any{"value": event, "matchType": "EXACT"},
				},
			}
		}
		out = append(out, step)
	}
	return map[string]any{"steps": out}
}

func (c *clients) runConversionsReport(ctx context.Context, a conversionsArgs) (any, error) {
	name, err := propertyName(a.PropertyID)
	if err != nil {
		return nil, err
	}
	body := a.body(true)
	body["conversionSpec"] = a.ConversionSpec
	return c.postDataAPI(ctx, "v1alpha", name, "runReport", body)
}

// postDataAPI reaches REST methods the generated Go client does not expose.
func (c *clients) postDataAPI(ctx context.Context, version, property, method string, body map[string]any) (any, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	url := dataAPIBase + version + "/" + property + ":" + method
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s failed (%s): %s", method, httpStatusName(resp.StatusCode), strings.TrimSpace(string(data)))
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *clients) createAnnotation(ctx context.Context, a createAnnotationArgs) (any, error) {
	name, err := propertyName(a.PropertyID)
	if err != nil {
		return nil, err
	}
	title := strings.TrimSpace(a.Title)
	if title == "" {
		return nil, fmt.Errorf("title must be a non-empty string")
	}
	if len(title) > 60 {
		return nil, fmt.Errorf("title must be at most 60 characters (got %d)", len(title))
	}
	if len(a.Description) > 150 {
		return nil, fmt.Errorf("description must be at most 150 characters (got %d)", len(a.Description))
	}

	start, err := parseDate(a.StartDate)
	if err != nil {
		return nil, fmt.Errorf("start_date: %w", err)
	}

	color := strings.ToUpper(strings.TrimSpace(a.Color))
	if color == "" {
		color = "BLUE"
	}
	if !validAnnotationColor(color) {
		return nil, fmt.Errorf("invalid color %q: use PURPLE, BROWN, BLUE, GREEN, RED or CYAN (ORANGE is reserved by Google Analytics)", a.Color)
	}

	annotation := &adminv1alpha.GoogleAnalyticsAdminV1alphaReportingDataAnnotation{
		Title: title,
		Color: color,
	}
	if a.Description != "" {
		annotation.Description = a.Description
	}
	if a.EndDate != "" {
		end, err := parseDate(a.EndDate)
		if err != nil {
			return nil, fmt.Errorf("end_date: %w", err)
		}
		if end.Year < start.Year || (end.Year == start.Year && end.Month < start.Month) ||
			(end.Year == start.Year && end.Month == start.Month && end.Day < start.Day) {
			return nil, fmt.Errorf("end_date (%s) must be on or after start_date (%s)", a.EndDate, a.StartDate)
		}
		annotation.AnnotationDateRange = &adminv1alpha.GoogleAnalyticsAdminV1alphaReportingDataAnnotationDateRange{
			StartDate: start,
			EndDate:   end,
		}
	} else {
		annotation.AnnotationDate = start
	}

	return c.adminAlpha.Properties.ReportingDataAnnotations.Create(name, annotation).Context(ctx).Do()
}

func validAnnotationColor(c string) bool {
	switch c {
	case "PURPLE", "BROWN", "BLUE", "GREEN", "RED", "CYAN":
		return true
	}
	return false
}

func parseDate(s string) (*adminv1alpha.GoogleTypeDate, error) {
	t, err := time.Parse("2006-01-02", strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("expected YYYY-MM-DD, got %q", s)
	}
	return &adminv1alpha.GoogleTypeDate{
		Year:  int64(t.Year()),
		Month: int64(t.Month()),
		Day:   int64(t.Day()),
	}, nil
}

func httpStatusName(code int) string {
	switch code {
	case 401:
		return "unauthenticated"
	case 403:
		return "permission denied"
	case 404:
		return "not found"
	case 429:
		return "quota exceeded"
	default:
		return fmt.Sprint(code)
	}
}
