## 1. Remove PodCrash Detector Implementation

- [ ] 1.1 Delete `pkg/events/detectors/pod_crash.go` file
- [ ] 1.2 Delete `pkg/events/detectors/pod_crash_test.go` file
- [ ] 1.3 Remove `FaultTypePodCrash` constant from `pkg/events/fault_signal.go`
- [ ] 1.4 Remove `detectors.NewPodCrashDetector()` from detector list in `pkg/mcp/mcp.go` line 136

## 2. Update Tests

- [ ] 2.1 Remove all PodCrash fault type references from `pkg/events/fault_dedup_test.go`
- [ ] 2.2 Remove PodCrash references from `pkg/events/notification_test.go`
- [ ] 2.3 Remove PodCrash references from `pkg/events/integration_test.go`
- [ ] 2.4 Remove PodCrash references from `pkg/events/resource_watcher_test.go`
- [ ] 2.5 Remove PodCrash references from `pkg/events/fault_id_test.go`
- [ ] 2.6 Remove PodCrash references from `pkg/events/fault_signal_test.go`
- [ ] 2.7 Remove PodCrash references from `pkg/events/fault_enricher_test.go`

## 3. Validation

- [ ] 3.1 Run `make test` to ensure all tests pass
- [ ] 3.2 Run `make lint` to ensure code style compliance
- [ ] 3.3 Run `make build` to ensure successful compilation
- [ ] 3.4 Verify no remaining references to PodCrash in codebase: `rg "PodCrash" --type go`

## 4. Update Specification

- [ ] 4.1 Remove "Detect pod crash via state transition" scenario from kubernetes-event-streaming spec
- [ ] 4.2 Remove PodCrash from fault type list in "Resource Fault Notification Format" requirement
- [ ] 4.3 Remove PodCrash from notification payload example scenarios
- [ ] 4.4 Update "Resource-Based Fault Subscription Mode" requirement to clarify CrashLoop-only detection for pods

## Dependencies

- Tasks 1.1-1.4 should be done sequentially (remove detector, then constant, then registration)
- Tasks 2.x can be done in parallel after 1.x completes
- Task 3.1-3.3 must be done after 1.x and 2.x complete
- Task 3.4 should be done after all code changes to verify cleanup
- Task 4.x should be done after code changes are validated
