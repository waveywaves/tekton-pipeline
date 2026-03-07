// controller-code-optimizations.go
// Code-level optimizations for Tekton Pipeline Controller Performance
// These optimizations can be applied directly to the codebase without configuration changes

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	lru "github.com/hashicorp/golang-lru"
	"k8s.io/apimachinery/pkg/runtime"
	"knative.dev/pkg/controller"
)

// OPTIMIZATION 1: Add Schema Validation Cache
// Cache validation results to avoid repeated schema validation overhead
type ValidationCache struct {
	cache *lru.ARCCache
	mu    sync.RWMutex
}

func NewValidationCache(size int) (*ValidationCache, error) {
	cache, err := lru.NewARC(size)
	if err != nil {
		return nil, err
	}
	return &ValidationCache{cache: cache}, nil
}

func (vc *ValidationCache) GetValidationResult(key string) (bool, bool) {
	vc.mu.RLock()
	defer vc.mu.RUnlock()
	if val, ok := vc.cache.Get(key); ok {
		return val.(bool), true
	}
	return false, false
}

func (vc *ValidationCache) SetValidationResult(key string, valid bool) {
	vc.mu.Lock()
	defer vc.mu.Unlock()
	vc.cache.Add(key, valid)
}

// Generate a hash key for the resource to use in cache
func generateCacheKey(obj runtime.Object) string {
	h := sha256.New()
	h.Write([]byte(fmt.Sprintf("%v", obj)))
	return hex.EncodeToString(h.Sum(nil))
}

// OPTIMIZATION 2: Batch Processing for Multiple Resources
type BatchProcessor struct {
	batchSize    int
	batchTimeout time.Duration
	items        chan interface{}
	processor    func([]interface{}) error
}

func NewBatchProcessor(size int, timeout time.Duration, processor func([]interface{}) error) *BatchProcessor {
	bp := &BatchProcessor{
		batchSize:    size,
		batchTimeout: timeout,
		items:        make(chan interface{}, size*2),
		processor:    processor,
	}
	go bp.processBatches()
	return bp
}

func (bp *BatchProcessor) Add(item interface{}) {
	bp.items <- item
}

func (bp *BatchProcessor) processBatches() {
	ticker := time.NewTicker(bp.batchTimeout)
	defer ticker.Stop()

	batch := make([]interface{}, 0, bp.batchSize)

	for {
		select {
		case item := <-bp.items:
			batch = append(batch, item)
			if len(batch) >= bp.batchSize {
				bp.processor(batch)
				batch = batch[:0]
			}
		case <-ticker.C:
			if len(batch) > 0 {
				bp.processor(batch)
				batch = batch[:0]
			}
		}
	}
}

// OPTIMIZATION 3: Parallel Reconciliation for Independent Resources
type ParallelReconciler struct {
	workers   int
	workQueue chan ReconcileWork
	wg        sync.WaitGroup
}

type ReconcileWork struct {
	ctx      context.Context
	resource interface{}
	callback func(error)
}

func NewParallelReconciler(workers int) *ParallelReconciler {
	pr := &ParallelReconciler{
		workers:   workers,
		workQueue: make(chan ReconcileWork, workers*2),
	}

	for i := 0; i < workers; i++ {
		go pr.worker()
	}

	return pr
}

func (pr *ParallelReconciler) worker() {
	for work := range pr.workQueue {
		err := pr.reconcileResource(work.ctx, work.resource)
		work.callback(err)
		pr.wg.Done()
	}
}

func (pr *ParallelReconciler) reconcileResource(ctx context.Context, resource interface{}) error {
	// Actual reconciliation logic here
	// This is a placeholder - integrate with actual reconciliation
	return nil
}

func (pr *ParallelReconciler) AddWork(ctx context.Context, resource interface{}, callback func(error)) {
	pr.wg.Add(1)
	pr.workQueue <- ReconcileWork{
		ctx:      ctx,
		resource: resource,
		callback: callback,
	}
}

func (pr *ParallelReconciler) Wait() {
	pr.wg.Wait()
}

// OPTIMIZATION 4: Resource Pool for Frequently Used Objects
type ResourcePool struct {
	pool sync.Pool
}

func NewResourcePool() *ResourcePool {
	return &ResourcePool{
		pool: sync.Pool{
			New: func() interface{} {
				// Create new resource objects
				// This reduces GC pressure
				return &v1.TaskRun{}
			},
		},
	}
}

func (rp *ResourcePool) Get() interface{} {
	return rp.pool.Get()
}

func (rp *ResourcePool) Put(obj interface{}) {
	// Reset the object before putting back in pool
	if tr, ok := obj.(*v1.TaskRun); ok {
		// Reset only necessary fields, not the entire object
		tr.Status = v1.TaskRunStatus{}
	}
	rp.pool.Put(obj)
}

// OPTIMIZATION 5: Lazy Loading for Large Resources
type LazyLoader struct {
	cache     map[string]interface{}
	mu        sync.RWMutex
	loader    func(string) (interface{}, error)
	maxAge    time.Duration
	lastLoad  map[string]time.Time
}

func NewLazyLoader(loader func(string) (interface{}, error), maxAge time.Duration) *LazyLoader {
	return &LazyLoader{
		cache:    make(map[string]interface{}),
		loader:   loader,
		maxAge:   maxAge,
		lastLoad: make(map[string]time.Time),
	}
}

func (ll *LazyLoader) Get(key string) (interface{}, error) {
	ll.mu.RLock()
	if val, ok := ll.cache[key]; ok {
		if time.Since(ll.lastLoad[key]) < ll.maxAge {
			ll.mu.RUnlock()
			return val, nil
		}
	}
	ll.mu.RUnlock()

	// Load the resource
	ll.mu.Lock()
	defer ll.mu.Unlock()

	// Double-check after acquiring write lock
	if val, ok := ll.cache[key]; ok {
		if time.Since(ll.lastLoad[key]) < ll.maxAge {
			return val, nil
		}
	}

	val, err := ll.loader(key)
	if err != nil {
		return nil, err
	}

	ll.cache[key] = val
	ll.lastLoad[key] = time.Now()
	return val, nil
}

// OPTIMIZATION 6: Skip Validation for Known-Good Resources
type ValidationSkipper struct {
	knownGoodHashes map[string]bool
	mu              sync.RWMutex
}

func NewValidationSkipper() *ValidationSkipper {
	return &ValidationSkipper{
		knownGoodHashes: make(map[string]bool),
	}
}

func (vs *ValidationSkipper) ShouldSkipValidation(obj runtime.Object) bool {
	hash := generateCacheKey(obj)

	vs.mu.RLock()
	defer vs.mu.RUnlock()

	return vs.knownGoodHashes[hash]
}

func (vs *ValidationSkipper) MarkAsValidated(obj runtime.Object) {
	hash := generateCacheKey(obj)

	vs.mu.Lock()
	defer vs.mu.Unlock()

	vs.knownGoodHashes[hash] = true

	// Limit cache size to prevent memory issues
	if len(vs.knownGoodHashes) > 10000 {
		// Simple eviction: remove half of entries
		count := 0
		for k := range vs.knownGoodHashes {
			delete(vs.knownGoodHashes, k)
			count++
			if count > 5000 {
				break
			}
		}
	}
}

// OPTIMIZATION 7: Connection Pooling for API Calls
type APIConnectionPool struct {
	connections chan *APIConnection
	maxSize     int
}

type APIConnection struct {
	// Actual connection implementation
	lastUsed time.Time
}

func NewAPIConnectionPool(size int) *APIConnectionPool {
	pool := &APIConnectionPool{
		connections: make(chan *APIConnection, size),
		maxSize:     size,
	}

	// Pre-populate pool
	for i := 0; i < size; i++ {
		pool.connections <- &APIConnection{lastUsed: time.Now()}
	}

	return pool
}

func (p *APIConnectionPool) Get() *APIConnection {
	select {
	case conn := <-p.connections:
		return conn
	default:
		// Create new connection if pool is empty
		return &APIConnection{lastUsed: time.Now()}
	}
}

func (p *APIConnectionPool) Put(conn *APIConnection) {
	conn.lastUsed = time.Now()
	select {
	case p.connections <- conn:
		// Connection returned to pool
	default:
		// Pool is full, discard connection
	}
}

// OPTIMIZATION 8: Debouncer for Rapid Updates
type Debouncer struct {
	wait     time.Duration
	mu       sync.Mutex
	timers   map[string]*time.Timer
	callback func(string)
}

func NewDebouncer(wait time.Duration, callback func(string)) *Debouncer {
	return &Debouncer{
		wait:     wait,
		timers:   make(map[string]*time.Timer),
		callback: callback,
	}
}

func (d *Debouncer) Call(key string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if timer, exists := d.timers[key]; exists {
		timer.Stop()
	}

	d.timers[key] = time.AfterFunc(d.wait, func() {
		d.callback(key)
		d.mu.Lock()
		delete(d.timers, key)
		d.mu.Unlock()
	})
}

// OPTIMIZATION 9: Priority Queue for Critical Resources
type PriorityQueue struct {
	items    []*QueueItem
	mu       sync.RWMutex
}

type QueueItem struct {
	Priority  int
	Resource  interface{}
	Timestamp time.Time
}

func (pq *PriorityQueue) Push(item *QueueItem) {
	pq.mu.Lock()
	defer pq.mu.Unlock()

	// Insert in priority order
	inserted := false
	for i, existing := range pq.items {
		if item.Priority > existing.Priority {
			pq.items = append(pq.items[:i], append([]*QueueItem{item}, pq.items[i:]...)...)
			inserted = true
			break
		}
	}

	if !inserted {
		pq.items = append(pq.items, item)
	}
}

func (pq *PriorityQueue) Pop() *QueueItem {
	pq.mu.Lock()
	defer pq.mu.Unlock()

	if len(pq.items) == 0 {
		return nil
	}

	item := pq.items[0]
	pq.items = pq.items[1:]
	return item
}

// OPTIMIZATION 10: Circuit Breaker for Failed Operations
type CircuitBreaker struct {
	failureThreshold int
	resetTimeout     time.Duration
	failures         int
	lastFailureTime  time.Time
	state            string
	mu               sync.RWMutex
}

func NewCircuitBreaker(threshold int, timeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		failureThreshold: threshold,
		resetTimeout:     timeout,
		state:            "closed",
	}
}

func (cb *CircuitBreaker) Call(fn func() error) error {
	cb.mu.RLock()
	state := cb.state
	cb.mu.RUnlock()

	if state == "open" {
		cb.mu.RLock()
		if time.Since(cb.lastFailureTime) > cb.resetTimeout {
			cb.mu.RUnlock()
			cb.mu.Lock()
			cb.state = "half-open"
			cb.failures = 0
			cb.mu.Unlock()
		} else {
			cb.mu.RUnlock()
			return fmt.Errorf("circuit breaker is open")
		}
	}

	err := fn()

	cb.mu.Lock()
	defer cb.mu.Unlock()

	if err != nil {
		cb.failures++
		cb.lastFailureTime = time.Now()

		if cb.failures >= cb.failureThreshold {
			cb.state = "open"
		}
		return err
	}

	if cb.state == "half-open" {
		cb.state = "closed"
	}
	cb.failures = 0

	return nil
}