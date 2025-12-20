# Implementation Tasks: Filter Historical Events

**Change ID**: `filter-historical-events`
**Total Tasks**: 15
**Estimated Complexity**: Low to Medium

## 1. Core Implementation

### 1.1 Get Current Resource Version
- [x] 1.1.1 Add method to EventSubscriptionManager to fetch current resource version before starting watch
- [x] 1.1.2 Implement List operation with Limit=1 to get current resourceVersion without fetching all events
- [x] 1.1.3 Return error if List operation fails (fail fast - do not create subscription)

### 1.2 Update EventWatcher Configuration
- [x] 1.2.1 Add `InitialResourceVersion` field to EventWatcherConfig struct
- [x] 1.2.2 Modify `NewEventWatcher()` to accept and store initial resource version
- [x] 1.2.3 Update `startWatch()` to use initial resource version when `w.resourceVersion` is empty

### 1.3 Update Subscription Manager
- [x] 1.3.1 Modify `startWatcher()` in manager.go to fetch current resource version before creating EventWatcher
- [x] 1.3.2 Pass initial resource version to EventWatcherConfig
- [x] 1.3.3 Add logging to show when starting from specific resource version

## 2. Testing

### 2.1 Unit Tests
- [x] 2.1.1 Test EventWatcher starts from provided initial resource version
- [x] 2.1.2 Test List operation to get current resource version works correctly
- [x] 2.1.3 Test subscription creation fails when List operation fails (fail-fast behavior)

### 2.2 Integration Tests
- [x] 2.2.1 Create test that verifies historical events are NOT sent on new subscription
- [x] 2.2.2 Create test that verifies new events (after subscription) ARE sent
- [x] 2.2.3 Test edge case: event created during List→Watch gap

### 2.3 Manual Testing
- [ ] 2.3.1 Deploy crashlooping pod, wait 5 minutes, create subscription, verify no historical events received
- [ ] 2.3.2 Create subscription, then trigger new fault, verify fault notification IS received
- [ ] 2.3.3 Verify ongoing faults continue to generate notifications (Kubernetes creates new events)

## 3. Documentation

### 3.1 Code Documentation
- [x] 3.1.1 Add godoc comments explaining resource version initialization
- [x] 3.1.2 Update EventWatcherConfig documentation

### 3.2 User Documentation
- [x] 3.2.1 Update spec to document that subscriptions only notify on events after subscription time (no retro)
