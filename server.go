package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/api/analyticsdata/v1beta"
)

type emptyArgs struct{}

type propertyArgs struct {
	PropertyID string `json:"property_id" jsonschema:"GA4 property ID, either a number or 'properties/<number>'"`
}

func propertyName(raw string) (string, error) {
	s := strings.TrimPrefix(strings.TrimSpace(raw), "properties/")
	if _, err := strconv.ParseInt(s, 10, 64); err != nil {
		return "", fmt.Errorf("invalid property ID %q: expected a number or 'properties/<number>'", raw)
	}
	return "properties/" + s, nil
}

func readOnlyAnnotations() *mcp.ToolAnnotations {
	no, yes := false, true
	return &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: &no, OpenWorldHint: &yes}
}

func writeAnnotations() *mcp.ToolAnnotations {
	no, yes := false, true
	return &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &no, IdempotentHint: false, OpenWorldHint: &yes}
}

func newServer(ctx context.Context, scopes []string, cfg authConfig) (*mcp.Server, error) {
	c, err := newClients(ctx, scopes, cfg)
	if err != nil {
		return nil, err
	}

	srv := mcp.NewServer(&mcp.Implementation{Name: "Google Analytics MCP Server", Version: "1.0.0"}, nil)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_account_summaries",
		Description: "Retrieves information about the user's Google Analytics accounts and properties.",
		Annotations: readOnlyAnnotations(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ emptyArgs) (*mcp.CallToolResult, any, error) {
		resp, err := c.admin.AccountSummaries.List().Context(ctx).Do()
		return nil, resp, err
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_property_details",
		Description: "Returns details about a property.",
		Annotations: readOnlyAnnotations(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a propertyArgs) (*mcp.CallToolResult, any, error) {
		name, err := propertyName(a.PropertyID)
		if err != nil {
			return nil, nil, err
		}
		resp, err := c.admin.Properties.Get(name).Context(ctx).Do()
		return nil, resp, err
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_google_ads_links",
		Description: "Returns a list of links to Google Ads accounts for a property.",
		Annotations: readOnlyAnnotations(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a propertyArgs) (*mcp.CallToolResult, any, error) {
		name, err := propertyName(a.PropertyID)
		if err != nil {
			return nil, nil, err
		}
		resp, err := c.admin.Properties.GoogleAdsLinks.List(name).Context(ctx).Do()
		return nil, resp, err
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_property_annotations",
		Description: "Returns annotations for a property. Annotations are notes pinned to specific dates or periods, typically recording releases, campaign launches or traffic anomalies.",
		Annotations: readOnlyAnnotations(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a propertyArgs) (*mcp.CallToolResult, any, error) {
		name, err := propertyName(a.PropertyID)
		if err != nil {
			return nil, nil, err
		}
		resp, err := c.adminAlpha.Properties.ReportingDataAnnotations.List(name).Context(ctx).Do()
		return nil, resp, err
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_custom_dimensions_and_metrics",
		Description: "Returns the property's custom dimensions and metrics.",
		Annotations: readOnlyAnnotations(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a propertyArgs) (*mcp.CallToolResult, any, error) {
		name, err := propertyName(a.PropertyID)
		if err != nil {
			return nil, nil, err
		}
		out, err := c.getCustomDimensionsAndMetrics(ctx, name)
		return nil, out, err
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "run_report",
		Description: runReportDoc,
		Annotations: readOnlyAnnotations(),
		InputSchema: reportSchema,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a reportArgs) (*mcp.CallToolResult, any, error) {
		out, err := c.runReport(ctx, a)
		return nil, out, err
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "run_realtime_report",
		Description: realtimeReportDoc,
		Annotations: readOnlyAnnotations(),
		InputSchema: realtimeSchema,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a realtimeArgs) (*mcp.CallToolResult, any, error) {
		out, err := c.runRealtimeReport(ctx, a)
		return nil, out, err
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "run_funnel_report",
		Description: funnelReportDoc,
		Annotations: readOnlyAnnotations(),
		InputSchema: funnelSchema,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a funnelArgs) (*mcp.CallToolResult, any, error) {
		out, err := c.runFunnelReport(ctx, a)
		return nil, out, err
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "run_conversions_report",
		Description: conversionsReportDoc,
		Annotations: readOnlyAnnotations(),
		InputSchema: conversionsSchema,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a conversionsArgs) (*mcp.CallToolResult, any, error) {
		out, err := c.runConversionsReport(ctx, a)
		return nil, out, err
	})

	if hasScope(scopes, editScope) {
		mcp.AddTool(srv, &mcp.Tool{
			Name:        "create_property_annotation",
			Description: annotationDoc,
			Annotations: writeAnnotations(),
			InputSchema: annotationSchema,
		}, func(ctx context.Context, _ *mcp.CallToolRequest, a createAnnotationArgs) (*mcp.CallToolResult, any, error) {
			out, err := c.createAnnotation(ctx, a)
			return nil, out, err
		})
	}

	return srv, nil
}

func (c *clients) getCustomDimensionsAndMetrics(ctx context.Context, name string) (map[string]any, error) {
	md, err := c.data.Properties.GetMetadata(name + "/metadata").Context(ctx).Do()
	if err != nil {
		return nil, err
	}
	dims := []*analyticsdata.DimensionMetadata{}
	for _, d := range md.Dimensions {
		if d.CustomDefinition {
			dims = append(dims, d)
		}
	}
	metrics := []*analyticsdata.MetricMetadata{}
	for _, m := range md.Metrics {
		if m.CustomDefinition {
			metrics = append(metrics, m)
		}
	}
	return map[string]any{"custom_dimensions": dims, "custom_metrics": metrics}, nil
}

const runReportDoc = `Runs a Google Analytics Data API report.

Dimensions and metrics are passed as Data API schema names, for example
{"name":"country"} / {"name":"activeUsers"}. Custom names come from
get_custom_dimensions_and_metrics.

Date ranges accept relative values: "today", "yesterday", "7daysAgo",
"30daysAgo", or explicit YYYY-MM-DD.

dimension_filter / metric_filter / order_bys take the Data API FilterExpression
and OrderBy JSON shapes (camelCase field names, as in the REST reference).`

const realtimeReportDoc = `Runs a Google Analytics Data API realtime report.

Only realtime dimensions and metrics are valid; custom metrics are not
supported. User-scoped custom dimensions (apiName starting with "customUser:")
are allowed.`

const funnelReportDoc = `Run a Google Analytics Data API funnel report (v1alpha).

funnel_steps entries are either {"name": "...", "event": "..."} for simple
event steps or {"name": "...", "filter_expression": {...}} for full filter
expressions. Optional funnel_breakdown {"breakdown_dimension": "deviceCategory"}
and funnel_next_action {"next_action_dimension": "eventName", "limit": 5}.`

const conversionsReportDoc = `Runs a Google Analytics Data API conversions report (v1alpha).

Use this instead of run_report for conversions, ad performance, ROAS or
attribution. conversion_spec takes {"conversion_actions": [...],
"attribution_model": "DATA_DRIVEN"}.`

const annotationDoc = `Creates a reporting data annotation for a property.

Annotations mark specific dates or date ranges, typically used to record
releases, campaign launches or traffic anomalies. title is at most 60
characters, description at most 150. Color accepts PURPLE, BROWN, BLUE, GREEN,
RED or CYAN; ORANGE is reserved by Google Analytics.`
