# Phase 12: End-to-End Manual Testing Guide

This guide walks through manual verification of the resource-based fault detection system against a real Kubernetes cluster.

## Prerequisites

1. Binary built: `./kubernetes-mcp-server` in the worktree directory
2. Kind cluster running (or any Kubernetes cluster)
3. kubectl configured to access the cluster
4. mcp-inspector installed: `npm install -g @modelcontextprotocol/inspector`

## Test Setup

The worktree is located at:
```
/Users/rbias/code/worktrees/feature/resource-fault-detection
```

## Test 1: Pod Crash Detection

### Goal
Verify that single pod crashes are detected and emit fault notifications with termination messages.

### Steps

1. **Start the MCP server with inspector:**
   ```bash
   cd /Users/rbias/code/worktrees/feature/resource-fault-detection
   npx @modelcontextprotocol/inspector ./kubernetes-mcp-server
   ```

2. **Create a subscription with mode="resource-faults":**
   - In the MCP inspector UI, call the `events_subscribe` tool
   - Parameters:
     ```json
     {
       "mode": "resource-faults",
       "namespace": "default"
     }
     ```
   - Note the returned `subscriptionId`

3. **Deploy a crashing pod:**
   ```bash
   kubectl run crash-test --image=busybox --restart=Never -- sh -c 'exit 42'
   ```

4. **Verify notification received:**
   - Watch the MCP inspector logs
   - Should see a notification with:
     - `logger="kubernetes/resource-faults"`
     - `level="warning"`
     - `faultType="PodCrash"`
     - `severity="warning"`
     - `resource.name="crash-test"`
     - `context` containing exit code 42
   - Should include termination message if available

5. **Cleanup:**
   ```bash
   kubectl delete pod crash-test
   ```

### Expected Result
✓ Single fault notification received with PodCrash type and termination message context.

---

## Test 2: CrashLoopBackOff Detection

### Goal
Verify that CrashLoopBackOff is detected and only ONE notification is emitted (deduplication working).

### Steps

1. **Ensure subscription is still active from Test 1**

2. **Deploy a pod that crashes repeatedly:**
   ```bash
   kubectl run crashloop-test --image=busybox -- sh -c 'while true; do echo "Crash!"; exit 1; done'
   ```

3. **Wait for CrashLoopBackOff state:**
   ```bash
   # Wait about 2-3 minutes for Kubernetes to recognize the crash loop
   kubectl get pod crashloop-test -w
   ```

   Look for: `STATUS: CrashLoopBackOff`

4. **Verify SINGLE notification received:**
   - Watch the MCP inspector logs
   - Should see ONE notification with:
     - `logger="kubernetes/resource-faults"`
     - `level="warning"`
     - `faultType="CrashLoop"`
     - `severity="critical"`
     - `resource.name="crashloop-test"`
     - `context` containing restart count and waiting message
   - Should NOT see multiple notifications for the same crash loop

5. **Cleanup:**
   ```bash
   kubectl delete pod crashloop-test
   ```

### Expected Result
✓ Only ONE fault notification received despite multiple crashes (deduplication working).
✓ Severity is "critical" for CrashLoop.

---

## Test 3: Node Fault Detection

### Goal
Verify that node health transitions are detected.

### Steps

1. **Ensure subscription is still active**

2. **Simulate node unhealthy (if using Kind):**
   ```bash
   # Get node name
   NODE_NAME=$(kubectl get nodes -o jsonpath='{.items[0].metadata.name}')

   # Cordon the node (simulation - real unhealthy requires more complex setup)
   kubectl cordon $NODE_NAME
   ```

   **Note:** This is a simplified test. True node failure detection requires the Ready condition to transition to False/Unknown, which is harder to simulate safely. Skip this test if you prefer.

3. **Verify notification (if node actually becomes unhealthy):**
   - Should see notification with:
     - `faultType="NodeUnhealthy"`
     - `severity="critical"` (for False) or `"warning"` (for Unknown)
     - Node name and status information

4. **Cleanup:**
   ```bash
   kubectl uncordon $NODE_NAME
   ```

### Expected Result
✓ Node fault notification received when Ready condition transitions.

**Alternative:** Skip this test as it's hard to safely simulate node failures.

---

## Test 4: Deployment Failure Detection

### Goal
Verify that deployment rollout failures (ProgressDeadlineExceeded) are detected.

### Steps

1. **Ensure subscription is still active**

2. **Create a deployment with invalid image:**
   ```bash
   kubectl create deployment bad-deploy --image=this-image-does-not-exist:latest
   kubectl scale deployment bad-deploy --replicas=3
   ```

3. **Set progress deadline:**
   ```bash
   kubectl patch deployment bad-deploy -p '{"spec":{"progressDeadlineSeconds":30}}'
   ```

4. **Wait for progress deadline to exceed:**
   ```bash
   # Wait about 40 seconds
   kubectl get deployment bad-deploy -w
   ```

   Look for condition: `ProgressDeadlineExceeded`

5. **Verify notification received:**
   - Should see notification with:
     - `faultType="DeploymentFailure"`
     - `severity="critical"`
     - `resource.name="bad-deploy"`
     - Context explaining the failure

6. **Cleanup:**
   ```bash
   kubectl delete deployment bad-deploy
   ```

### Expected Result
✓ Deployment failure notification received when ProgressDeadlineExceeded.

---

## Test 5: Job Failure Detection

### Goal
Verify that job failures are detected.

### Steps

1. **Ensure subscription is still active**

2. **Create a failing job:**
   ```bash
   kubectl create job fail-test --image=busybox -- sh -c 'exit 1'
   ```

3. **Wait for job to fail:**
   ```bash
   kubectl get job fail-test -w
   ```

   Look for: `COMPLETIONS: 0/1` and `Failed` condition

4. **Verify notification received:**
   - Should see notification with:
     - `faultType="JobFailure"`
     - `severity="warning"` or `"critical"` depending on reason
     - `resource.name="fail-test"`
     - Context explaining the failure

5. **Cleanup:**
   ```bash
   kubectl delete job fail-test
   ```

### Expected Result
✓ Job failure notification received when job fails.

---

## Test 6: Verify Backwards Compatibility

### Goal
Ensure existing `mode="faults"` subscriptions still work.

### Steps

1. **Create a subscription with mode="faults" (the old event-based mode):**
   ```json
   {
     "mode": "faults",
     "namespace": "default"
   }
   ```

2. **Trigger some cluster activity** (create pods, etc.)

3. **Verify notifications use logger="kubernetes/faults"** (not "resource-faults")

### Expected Result
✓ Event-based fault detection still works.
✓ Different logger value distinguishes the two modes.

---

## Test Completion Checklist

Mark each test as you complete it:

- [ ] Test 1: Pod Crash Detection - PASS/FAIL
- [ ] Test 2: CrashLoopBackOff Detection - PASS/FAIL
- [ ] Test 3: Node Fault Detection - PASS/FAIL (or SKIPPED)
- [ ] Test 4: Deployment Failure Detection - PASS/FAIL
- [ ] Test 5: Job Failure Detection - PASS/FAIL
- [ ] Test 6: Backwards Compatibility - PASS/FAIL

## Notes

Document any issues or observations here:

---

## Next Steps After Testing

Once all tests pass:
1. Mark Phase 12 as complete in the todo list
2. Proceed to Phase 13: Documentation updates
3. Consider creating a commit with all the changes
