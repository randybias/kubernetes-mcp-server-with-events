package events

import (
	"context"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

// FaultSignalCallback is a function that handles emitted fault signals.
// It is called by ResourceWatcher when a fault is detected after deduplication
// and enrichment.
type FaultSignalCallback func(signal FaultSignal)

// ResourceWatcher manages watching Kubernetes resources using SharedInformers
// for fault detection. It uses client-go's SharedInformerFactory to watch
// resources (Pods, Nodes, Deployments, Jobs) and detect fault conditions
// through edge-triggered detection (comparing old vs new object state).
//
// The ResourceWatcher runs a detection pipeline for each resource update:
// 1. Run registered detectors to produce FaultSignals
// 2. Deduplicate signals using FaultDeduplicator
// 3. Enrich signals with additional context using FaultContextEnricher
// 4. Emit signals via the FaultSignalCallback
type ResourceWatcher struct {
	clientset       kubernetes.Interface
	informerFactory informers.SharedInformerFactory
	stopChan        chan struct{}
	detectors       []Detector
	deduplicator    *FaultDeduplicator
	enricher        *FaultContextEnricher
	signalCallback  FaultSignalCallback
}

// ResourceWatcherConfig holds configuration for the resource watcher
type ResourceWatcherConfig struct {
	Clientset kubernetes.Interface
	// ResyncPeriod is the interval for full resync of cached resources.
	// A resync period of 0 disables resyncing.
	ResyncPeriod time.Duration
	// Detectors is a list of fault detectors to run on resource updates.
	// If empty, no fault detection will be performed.
	Detectors []Detector
	// Deduplicator is used to suppress duplicate fault signals.
	// If nil, a default deduplicator will be created.
	Deduplicator *FaultDeduplicator
	// Enricher is used to enrich fault signals with additional context.
	// If nil, a default enricher will be created.
	Enricher *FaultContextEnricher
	// SignalCallback is called when a fault signal is emitted after
	// detection, deduplication, and enrichment. If nil, signals are
	// logged but not emitted.
	SignalCallback FaultSignalCallback
}

// NewResourceWatcher creates a new resource watcher with the given configuration
func NewResourceWatcher(config ResourceWatcherConfig) *ResourceWatcher {
	if config.ResyncPeriod == 0 {
		// Default to 10 minutes if not specified
		config.ResyncPeriod = 10 * time.Minute
	}

	// Use provided deduplicator or create a default one
	deduplicator := config.Deduplicator
	if deduplicator == nil {
		deduplicator = NewFaultDeduplicator()
	}

	// Use provided enricher or create a default one
	enricher := config.Enricher
	if enricher == nil {
		enricher = NewFaultContextEnricher()
	}

	// Create SharedInformerFactory with resync period
	informerFactory := informers.NewSharedInformerFactory(config.Clientset, config.ResyncPeriod)

	return &ResourceWatcher{
		clientset:       config.Clientset,
		informerFactory: informerFactory,
		stopChan:        make(chan struct{}),
		detectors:       config.Detectors,
		deduplicator:    deduplicator,
		enricher:        enricher,
		signalCallback:  config.SignalCallback,
	}
}

// Start begins watching for resource updates
func (w *ResourceWatcher) Start(ctx context.Context) error {
	// Register Pod informer with Update callback
	podInformer := w.informerFactory.Core().V1().Pods().Informer()

	// Add event handler for Pod updates
	_, err := podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: func(oldObj, newObj interface{}) {
			oldPod, ok := oldObj.(*v1.Pod)
			if !ok {
				klog.Warningf("Expected *v1.Pod in UpdateFunc, got %T", oldObj)
				return
			}
			newPod, ok := newObj.(*v1.Pod)
			if !ok {
				klog.Warningf("Expected *v1.Pod in UpdateFunc, got %T", newObj)
				return
			}

			// Log Pod update for verification
			klog.V(2).Infof("Pod update detected: %s/%s (ResourceVersion: %s -> %s)",
				newPod.Namespace, newPod.Name,
				oldPod.ResourceVersion, newPod.ResourceVersion)

			// Run detection pipeline
			w.processPodUpdate(ctx, oldPod, newPod)
		},
	})
	if err != nil {
		return err
	}

	// Register Node informer with Update callback
	nodeInformer := w.informerFactory.Core().V1().Nodes().Informer()

	// Add event handler for Node updates
	_, err = nodeInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: func(oldObj, newObj interface{}) {
			oldNode, ok := oldObj.(*v1.Node)
			if !ok {
				klog.Warningf("Expected *v1.Node in UpdateFunc, got %T", oldObj)
				return
			}
			newNode, ok := newObj.(*v1.Node)
			if !ok {
				klog.Warningf("Expected *v1.Node in UpdateFunc, got %T", newObj)
				return
			}

			// Log Node update for verification
			klog.V(2).Infof("Node update detected: %s (ResourceVersion: %s -> %s)",
				newNode.Name,
				oldNode.ResourceVersion, newNode.ResourceVersion)

			// Run detection pipeline
			w.processNodeUpdate(ctx, oldNode, newNode)
		},
	})
	if err != nil {
		return err
	}

	// Register Deployment informer with Update callback
	deploymentInformer := w.informerFactory.Apps().V1().Deployments().Informer()

	// Add event handler for Deployment updates
	_, err = deploymentInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: func(oldObj, newObj interface{}) {
			oldDeployment, ok := oldObj.(*appsv1.Deployment)
			if !ok {
				klog.Warningf("Expected *appsv1.Deployment in UpdateFunc, got %T", oldObj)
				return
			}
			newDeployment, ok := newObj.(*appsv1.Deployment)
			if !ok {
				klog.Warningf("Expected *appsv1.Deployment in UpdateFunc, got %T", newObj)
				return
			}

			// Log Deployment update for verification
			klog.V(2).Infof("Deployment update detected: %s/%s (ResourceVersion: %s -> %s)",
				newDeployment.Namespace, newDeployment.Name,
				oldDeployment.ResourceVersion, newDeployment.ResourceVersion)

			// Run detection pipeline
			w.processDeploymentUpdate(ctx, oldDeployment, newDeployment)
		},
	})
	if err != nil {
		return err
	}

	// Register Job informer with Update callback
	jobInformer := w.informerFactory.Batch().V1().Jobs().Informer()

	// Add event handler for Job updates
	_, err = jobInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: func(oldObj, newObj interface{}) {
			oldJob, ok := oldObj.(*batchv1.Job)
			if !ok {
				klog.Warningf("Expected *batchv1.Job in UpdateFunc, got %T", oldObj)
				return
			}
			newJob, ok := newObj.(*batchv1.Job)
			if !ok {
				klog.Warningf("Expected *batchv1.Job in UpdateFunc, got %T", newObj)
				return
			}

			// Log Job update for verification
			klog.V(2).Infof("Job update detected: %s/%s (ResourceVersion: %s -> %s)",
				newJob.Namespace, newJob.Name,
				oldJob.ResourceVersion, newJob.ResourceVersion)

			// Run detection pipeline
			w.processJobUpdate(ctx, oldJob, newJob)
		},
	})
	if err != nil {
		return err
	}

	// Start the informer factory
	w.informerFactory.Start(w.stopChan)

	// Wait for cache sync
	klog.V(1).Info("Waiting for informer caches to sync...")
	synced := w.informerFactory.WaitForCacheSync(w.stopChan)
	for informerType, isSynced := range synced {
		if !isSynced {
			klog.Warningf("Failed to sync cache for informer: %v", informerType)
		} else {
			klog.V(2).Infof("Cache synced for informer: %v", informerType)
		}
	}
	klog.V(1).Info("Informer caches synced successfully")

	return nil
}

// processPodUpdate runs the detection pipeline on a Pod update event.
// Pipeline stages:
// 1. Run all registered detectors to produce fault signals
// 2. Deduplicate signals using FaultDeduplicator
// 3. Enrich signals with additional context using FaultContextEnricher
// 4. Emit signals via the FaultSignalCallback
func (w *ResourceWatcher) processPodUpdate(ctx context.Context, oldPod, newPod *v1.Pod) {
	// Skip if no detectors are registered
	if len(w.detectors) == 0 {
		return
	}

	// Stage 1: Run all detectors
	var allSignals []FaultSignal
	for _, detector := range w.detectors {
		signals := detector.Detect(oldPod, newPod)
		allSignals = append(allSignals, signals...)
	}

	// Stage 2: Deduplicate signals
	var dedupedSignals []FaultSignal
	for _, signal := range allSignals {
		if w.deduplicator.ShouldEmit(signal) {
			dedupedSignals = append(dedupedSignals, signal)
		} else {
			klog.V(2).Infof("Suppressed duplicate fault signal: %s for %s/%s (container: %s)",
				signal.FaultType, signal.Namespace, signal.Name, signal.ContainerName)
		}
	}

	// Stage 3: Enrich signals with additional context
	for i := range dedupedSignals {
		// Enrich modifies the signal in place
		err := w.enricher.Enrich(ctx, &dedupedSignals[i], w.clientset)
		if err != nil {
			// Log enrichment errors but don't block signal emission
			klog.V(2).Infof("Failed to enrich fault signal: %v", err)
		}
	}

	// Stage 4: Emit signals
	for _, signal := range dedupedSignals {
		if w.signalCallback != nil {
			w.signalCallback(signal)
		} else {
			// If no callback is provided, log the signal
			klog.Infof("Fault detected: %s in %s/%s (container: %s), severity: %s, context: %s",
				signal.FaultType, signal.Namespace, signal.Name, signal.ContainerName,
				signal.Severity, signal.Context)
		}
	}
}

// processNodeUpdate runs the detection pipeline on a Node update event.
// Pipeline stages:
// 1. Run all registered detectors to produce fault signals
// 2. Deduplicate signals using FaultDeduplicator
// 3. Enrich signals with additional context using FaultContextEnricher
// 4. Emit signals via the FaultSignalCallback
func (w *ResourceWatcher) processNodeUpdate(ctx context.Context, oldNode, newNode *v1.Node) {
	// Skip if no detectors are registered
	if len(w.detectors) == 0 {
		return
	}

	// Stage 1: Run all detectors
	var allSignals []FaultSignal
	for _, detector := range w.detectors {
		signals := detector.Detect(oldNode, newNode)
		allSignals = append(allSignals, signals...)
	}

	// Stage 2: Deduplicate signals
	var dedupedSignals []FaultSignal
	for _, signal := range allSignals {
		if w.deduplicator.ShouldEmit(signal) {
			dedupedSignals = append(dedupedSignals, signal)
		} else {
			klog.V(2).Infof("Suppressed duplicate fault signal: %s for Node %s",
				signal.FaultType, signal.Name)
		}
	}

	// Stage 3: Enrich signals with additional context
	for i := range dedupedSignals {
		// Enrich modifies the signal in place
		err := w.enricher.Enrich(ctx, &dedupedSignals[i], w.clientset)
		if err != nil {
			// Log enrichment errors but don't block signal emission
			klog.V(2).Infof("Failed to enrich fault signal: %v", err)
		}
	}

	// Stage 4: Emit signals
	for _, signal := range dedupedSignals {
		if w.signalCallback != nil {
			w.signalCallback(signal)
		} else {
			// If no callback is provided, log the signal
			klog.Infof("Fault detected: %s in Node %s, severity: %s, context: %s",
				signal.FaultType, signal.Name, signal.Severity, signal.Context)
		}
	}
}

// processDeploymentUpdate runs the detection pipeline on a Deployment update event.
// Pipeline stages:
// 1. Run all registered detectors to produce fault signals
// 2. Deduplicate signals using FaultDeduplicator
// 3. Enrich signals with additional context using FaultContextEnricher
// 4. Emit signals via the FaultSignalCallback
func (w *ResourceWatcher) processDeploymentUpdate(ctx context.Context, oldDeployment, newDeployment *appsv1.Deployment) {
	// Skip if no detectors are registered
	if len(w.detectors) == 0 {
		return
	}

	// Stage 1: Run all detectors
	var allSignals []FaultSignal
	for _, detector := range w.detectors {
		signals := detector.Detect(oldDeployment, newDeployment)
		allSignals = append(allSignals, signals...)
	}

	// Stage 2: Deduplicate signals
	var dedupedSignals []FaultSignal
	for _, signal := range allSignals {
		if w.deduplicator.ShouldEmit(signal) {
			dedupedSignals = append(dedupedSignals, signal)
		} else {
			klog.V(2).Infof("Suppressed duplicate fault signal: %s for Deployment %s/%s",
				signal.FaultType, signal.Namespace, signal.Name)
		}
	}

	// Stage 3: Enrich signals with additional context
	for i := range dedupedSignals {
		// Enrich modifies the signal in place
		err := w.enricher.Enrich(ctx, &dedupedSignals[i], w.clientset)
		if err != nil {
			// Log enrichment errors but don't block signal emission
			klog.V(2).Infof("Failed to enrich fault signal: %v", err)
		}
	}

	// Stage 4: Emit signals
	for _, signal := range dedupedSignals {
		if w.signalCallback != nil {
			w.signalCallback(signal)
		} else {
			// If no callback is provided, log the signal
			klog.Infof("Fault detected: %s in Deployment %s/%s, severity: %s, context: %s",
				signal.FaultType, signal.Namespace, signal.Name, signal.Severity, signal.Context)
		}
	}
}

// processJobUpdate runs the detection pipeline on a Job update event.
// Pipeline stages:
// 1. Run all registered detectors to produce fault signals
// 2. Deduplicate signals using FaultDeduplicator
// 3. Enrich signals with additional context using FaultContextEnricher
// 4. Emit signals via the FaultSignalCallback
func (w *ResourceWatcher) processJobUpdate(ctx context.Context, oldJob, newJob *batchv1.Job) {
	// Skip if no detectors are registered
	if len(w.detectors) == 0 {
		return
	}

	// Stage 1: Run all detectors
	var allSignals []FaultSignal
	for _, detector := range w.detectors {
		signals := detector.Detect(oldJob, newJob)
		allSignals = append(allSignals, signals...)
	}

	// Stage 2: Deduplicate signals
	var dedupedSignals []FaultSignal
	for _, signal := range allSignals {
		if w.deduplicator.ShouldEmit(signal) {
			dedupedSignals = append(dedupedSignals, signal)
		} else {
			klog.V(2).Infof("Suppressed duplicate fault signal: %s for Job %s/%s",
				signal.FaultType, signal.Namespace, signal.Name)
		}
	}

	// Stage 3: Enrich signals with additional context
	for i := range dedupedSignals {
		// Enrich modifies the signal in place
		err := w.enricher.Enrich(ctx, &dedupedSignals[i], w.clientset)
		if err != nil {
			// Log enrichment errors but don't block signal emission
			klog.V(2).Infof("Failed to enrich fault signal: %v", err)
		}
	}

	// Stage 4: Emit signals
	for _, signal := range dedupedSignals {
		if w.signalCallback != nil {
			w.signalCallback(signal)
		} else {
			// If no callback is provided, log the signal
			klog.Infof("Fault detected: %s in Job %s/%s, severity: %s, context: %s",
				signal.FaultType, signal.Namespace, signal.Name, signal.Severity, signal.Context)
		}
	}
}

// Stop stops the resource watcher
func (w *ResourceWatcher) Stop() {
	close(w.stopChan)
	klog.V(1).Info("ResourceWatcher stopped")
}
