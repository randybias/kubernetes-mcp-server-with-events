# Kubernetes Event Streaming - Performance Optimization Delta

This delta file reserves the design space for optimizing the fault processor performance. Specific requirement changes will be defined after the design investigation is complete.

## MODIFIED Requirements

### Requirement: Fault Notifications and Log Enrichment

For `mode="faults"` subscriptions, the server SHALL fetch container logs (both current and previous) from pods referenced in Warning events and include them in notifications. The server SHALL implement this log enrichment with sufficient performance to handle real-time fault scenarios without dropping events due to processing backpressure.

**Performance Characteristics (to be refined during design):**
- The system SHALL minimize API calls and processing latency per fault event
- The system SHALL provide configuration options for adjusting performance vs. completeness trade-offs
- The system SHALL handle burst scenarios with multiple simultaneous pod failures

#### Scenario: Log enrichment completes within processing budget
- **GIVEN** a Warning event referencing a failed pod
- **WHEN** the fault processor enriches the event with container logs
- **THEN** the processing completes quickly enough to prevent result channel overflow
- **AND** the enriched event includes current and previous logs from relevant containers

#### Scenario: High-volume fault scenario
- **GIVEN** multiple pod failures occurring simultaneously in the cluster
- **WHEN** the fault processor handles the burst of Warning events
- **THEN** the system maintains throughput without dropping events from the result channel
- **AND** log enrichment continues for all fault events within configured limits

#### Scenario: Configuration for performance tuning
- **GIVEN** different deployment scenarios (development vs. production, low vs. high volume)
- **WHEN** administrators configure the fault processor
- **THEN** configuration options allow tuning the performance vs. completeness trade-off
- **AND** the system respects the configured limits and behavior
