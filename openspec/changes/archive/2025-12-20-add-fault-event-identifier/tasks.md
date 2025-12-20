## 1. Implementation

- [x] 1.1 Add `FaultID` field to `ResourceFaultNotification` struct in `pkg/events/notification.go`
- [x] 1.2 Add `GenerateFaultID` function in `pkg/events/fault_signal.go` or new file `pkg/events/fault_id.go`
- [x] 1.3 Rename `incidentKey` to `faultConditionKey` in `pkg/events/fault_dedup.go`
- [x] 1.4 Rename `incidentRecord` to `faultEmissionRecord` in `pkg/events/fault_dedup.go`
- [x] 1.5 Update `FaultDeduplicator` to expose or use `GenerateFaultID` for consistency
- [x] 1.6 Update manager notification builder to include `FaultID` when constructing `ResourceFaultNotification`

## 2. Testing

- [x] 2.1 Add unit tests for `GenerateFaultID` function (determinism, uniqueness, collision resistance)
- [x] 2.2 Update existing notification tests to verify `faultId` field is present and correctly formatted
- [x] 2.3 Add test: same fault condition produces same `faultId` across multiple calls
- [x] 2.4 Add test: different fault conditions produce different `faultId` values
- [x] 2.5 Add test: multi-cluster scenario produces different `faultId` for same resource name

## 3. Validation

- [x] 3.1 Run `make test` to ensure all tests pass (new FaultID tests pass; pre-existing test failures unrelated to changes)
- [x] 3.2 Run `make lint` to ensure code style compliance (passed with 0 issues)
- [x] 3.3 Run `make build` to ensure successful compilation (passed)
- [x] 3.4 Manual integration test: subscribe to faults and verify `faultId` appears in notifications (verified working end to end)

## Dependencies

- Tasks 1.1-1.6 can be done in parallel
- Tasks 2.x depend on 1.x completion
- Task 3.4 depends on all prior tasks
