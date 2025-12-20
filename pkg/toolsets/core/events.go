package core

import (
	"encoding/json"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"

	"github.com/containers/kubernetes-mcp-server/pkg/api"
	"github.com/containers/kubernetes-mcp-server/pkg/events"
	"github.com/containers/kubernetes-mcp-server/pkg/kubernetes"
	"github.com/containers/kubernetes-mcp-server/pkg/mcplog"
	"github.com/containers/kubernetes-mcp-server/pkg/output"
)

func initEvents() []api.ServerTool {
	return []api.ServerTool{
		{Tool: api.Tool{
			Name:        "events_list",
			Description: "List all the Kubernetes events in the current cluster from all namespaces",
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"namespace": {
						Type:        "string",
						Description: "Optional Namespace to retrieve the events from. If not provided, will list events from all namespaces",
					},
				},
			},
			Annotations: api.ToolAnnotations{
				Title:           "Events: List",
				ReadOnlyHint:    ptr.To(true),
				DestructiveHint: ptr.To(false),
				OpenWorldHint:   ptr.To(true),
			},
		}, Handler: eventsList},
		{Tool: api.Tool{
			Name:        "events_subscribe",
			Description: "Subscribe to Kubernetes event notifications in real-time. Requires HTTP/SSE transport (start server with --port). Client must call logging/setLevel before receiving notifications.",
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"mode": {
						Type:        "string",
						Description: "Subscription mode: 'events' for all events (default), 'faults' for resource-based fault detection with edge-triggered state change notifications",
						Enum:        []any{"events", "faults"},
						Default:     json.RawMessage(`"events"`),
					},
					"namespaces": {
						Type:        "array",
						Description: "Optional list of namespaces to watch. If not provided, watches all namespaces (cluster-wide)",
						Items: &jsonschema.Schema{
							Type: "string",
						},
					},
					"labelSelector": {
						Type:        "string",
						Description: "Optional label selector for filtering events by involved object labels (e.g., 'app=nginx,tier=frontend')",
					},
					"involvedKind": {
						Type:        "string",
						Description: "Optional involved object kind filter (e.g., 'Pod', 'Deployment')",
					},
					"involvedName": {
						Type:        "string",
						Description: "Optional involved object name filter",
					},
					"involvedNamespace": {
						Type:        "string",
						Description: "Optional involved object namespace filter",
					},
					"type": {
						Type:        "string",
						Description: "Optional event type filter: 'Normal' or 'Warning'",
						Enum:        []any{"Normal", "Warning"},
					},
					"reason": {
						Type:        "string",
						Description: "Optional event reason prefix filter (e.g., 'BackOff', 'Failed')",
					},
				},
			},
			Annotations: api.ToolAnnotations{
				Title:           "Events: Subscribe",
				ReadOnlyHint:    ptr.To(true),
				DestructiveHint: ptr.To(false),
				OpenWorldHint:   ptr.To(true),
			},
		}, Handler: eventsSubscribe},
		{Tool: api.Tool{
			Name:        "events_unsubscribe",
			Description: "Unsubscribe from event notifications by subscription ID",
			InputSchema: &jsonschema.Schema{
				Type:     "object",
				Required: []string{"subscriptionId"},
				Properties: map[string]*jsonschema.Schema{
					"subscriptionId": {
						Type:        "string",
						Description: "The subscription ID returned by events_subscribe",
					},
				},
			},
			Annotations: api.ToolAnnotations{
				Title:           "Events: Unsubscribe",
				ReadOnlyHint:    ptr.To(true),
				DestructiveHint: ptr.To(false),
				OpenWorldHint:   ptr.To(false),
			},
		}, Handler: eventsUnsubscribe},
		{Tool: api.Tool{
			Name:        "events_list_subscriptions",
			Description: "List active event subscriptions for the current session",
			InputSchema: &jsonschema.Schema{
				Type: "object",
			},
			Annotations: api.ToolAnnotations{
				Title:           "Events: List Subscriptions",
				ReadOnlyHint:    ptr.To(true),
				DestructiveHint: ptr.To(false),
				OpenWorldHint:   ptr.To(false),
			},
		}, Handler: eventsListSubscriptions},
	}
}

func eventsList(params api.ToolHandlerParams) (*api.ToolCallResult, error) {
	namespace := params.GetArguments()["namespace"]
	if namespace == nil {
		namespace = ""
	}
	eventMap, err := kubernetes.NewCore(params).EventsList(params, namespace.(string))
	if err != nil {
		mcplog.HandleK8sError(params.Context, err, "events listing")
		return api.NewToolCallResult("", fmt.Errorf("failed to list events in all namespaces: %w", err)), nil
	}
	if len(eventMap) == 0 {
		return api.NewToolCallResult("# No events found", nil), nil
	}
	yamlEvents, err := output.MarshalYaml(eventMap)
	if err != nil {
		err = fmt.Errorf("failed to list events in all namespaces: %w", err)
	}
	return api.NewToolCallResult(fmt.Sprintf("# The following events (YAML format) were found:\n%s", yamlEvents), err), nil
}

func eventsSubscribe(params api.ToolHandlerParams) (*api.ToolCallResult, error) {
	// Transport check: verify sessionID is not empty
	if params.SessionID == "" {
		klog.V(1).Info("Event subscription rejected: no session ID (stdio transport)")
		return api.NewToolCallResult("", fmt.Errorf("event subscriptions require an HTTP/SSE transport. Start the server with --port and connect via HTTP")), nil
	}

	// Check if EventManager is available
	if params.EventManager == nil {
		return api.NewToolCallResult("", fmt.Errorf("event subscription manager not available")), nil
	}

	// Parse subscription mode (default to "events")
	mode := "events"
	if modeArg, ok := params.GetArguments()["mode"].(string); ok && modeArg != "" {
		mode = modeArg
	}

	// Parse filters from arguments
	filters := events.ParseFiltersFromMap(params.GetArguments())

	// Validate filters for the specified mode
	if err := filters.ValidateForMode(mode); err != nil {
		klog.V(1).Infof("Event subscription validation failed for session %s: %v", params.SessionID, err)
		return api.NewToolCallResult("", fmt.Errorf("invalid subscription filters: %v", err)), nil
	}

	// Create the subscription (using the cluster from params)
	subInterface, err := params.EventManager.Create(params.SessionID, params.Cluster, mode, filters)
	if err != nil {
		klog.V(1).Infof("Failed to create event subscription for session %s: %v", params.SessionID, err)
		return api.NewToolCallResult("", fmt.Errorf("failed to create subscription: %v", err)), nil
	}

	// Cast to concrete type to access subscription details
	sub, ok := subInterface.(*events.Subscription)
	if !ok {
		return api.NewToolCallResult("", fmt.Errorf("unexpected subscription type returned")), nil
	}

	// Start the event watcher - the manager handles this internally via the subscription's Cancel function
	// The watcher will be started when the subscription is activated
	klog.V(1).Infof("Event subscription created: %s for session %s (cluster=%s, mode=%s)", sub.ID, params.SessionID, params.Cluster, mode)

	// Build response
	response := map[string]interface{}{
		"subscriptionId": sub.ID,
		"cluster":        sub.Cluster,
		"mode":           sub.Mode,
		"filters":        sub.Filters.ToMap(),
		"createdAt":      sub.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		"status":         "active",
	}

	responseJSON, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return api.NewToolCallResult("", fmt.Errorf("failed to marshal response: %v", err)), nil
	}

	return api.NewToolCallResult(fmt.Sprintf("# Event Subscription Created\n\n%s\n\nYou will receive event notifications via the logging/message protocol.", string(responseJSON)), nil), nil
}

func eventsUnsubscribe(params api.ToolHandlerParams) (*api.ToolCallResult, error) {
	// Transport check
	if params.SessionID == "" {
		return api.NewToolCallResult("", fmt.Errorf("event subscriptions require an HTTP/SSE transport")), nil
	}

	// Check if EventManager is available
	if params.EventManager == nil {
		return api.NewToolCallResult("", fmt.Errorf("event subscription manager not available")), nil
	}

	subscriptionID, ok := params.GetArguments()["subscriptionId"].(string)
	if !ok || subscriptionID == "" {
		return api.NewToolCallResult("", fmt.Errorf("subscriptionId is required")), nil
	}

	// Cancel the subscription
	err := params.EventManager.CancelBySessionAndID(params.SessionID, subscriptionID)
	if err != nil {
		klog.V(1).Infof("Failed to cancel subscription %s for session %s: %v", subscriptionID, params.SessionID, err)
		return api.NewToolCallResult("", fmt.Errorf("failed to cancel subscription: %v", err)), nil
	}

	klog.V(1).Infof("Subscription %s cancelled for session %s", subscriptionID, params.SessionID)

	response := map[string]interface{}{
		"subscriptionId": subscriptionID,
		"cancelled":      true,
		"status":         "success",
	}

	responseJSON, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return api.NewToolCallResult("", fmt.Errorf("failed to marshal response: %v", err)), nil
	}

	return api.NewToolCallResult(fmt.Sprintf("# Subscription Cancelled\n\n%s", string(responseJSON)), nil), nil
}

func eventsListSubscriptions(params api.ToolHandlerParams) (*api.ToolCallResult, error) {
	// Transport check
	if params.SessionID == "" {
		return api.NewToolCallResult("", fmt.Errorf("event subscriptions require an HTTP/SSE transport")), nil
	}

	// Check if EventManager is available
	if params.EventManager == nil {
		return api.NewToolCallResult("", fmt.Errorf("event subscription manager not available")), nil
	}

	// Get subscriptions for this session
	subsInterface := params.EventManager.ListSubscriptionsForSession(params.SessionID)

	// Cast to concrete type
	subs, ok := subsInterface.([]*events.Subscription)
	if !ok {
		return api.NewToolCallResult("", fmt.Errorf("unexpected subscriptions type returned")), nil
	}

	// Build response with subscription details
	subscriptionsList := make([]map[string]interface{}, 0, len(subs))
	for _, sub := range subs {
		subscriptionsList = append(subscriptionsList, map[string]interface{}{
			"subscriptionId": sub.ID,
			"cluster":        sub.Cluster,
			"mode":           sub.Mode,
			"filters":        sub.Filters.ToMap(),
			"createdAt":      sub.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
			"degraded":       sub.Degraded,
		})
	}

	response := map[string]interface{}{
		"subscriptions": subscriptionsList,
		"total":         len(subs),
	}

	responseJSON, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return api.NewToolCallResult("", fmt.Errorf("failed to marshal response: %v", err)), nil
	}

	return api.NewToolCallResult(fmt.Sprintf("# Active Subscriptions\n\n%s", string(responseJSON)), nil), nil
}
