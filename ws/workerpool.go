package ws

import (
	"runtime"
	"sync"
	"sync/atomic"
)

// Task represents a unit of work to be processed
type Task interface {
	Execute() error
}

// WorkerPool represents a pool of workers that can process tasks
type WorkerPool struct {
	tasks     chan Task      // Channel for tasks
	wg        sync.WaitGroup // WaitGroup for graceful shutdown
	shutdown  atomic.Bool    // Shutdown flag
	workerNum int32          // Number of active workers
}

// NewWorkerPool creates a new worker pool with the specified number of workers and queue size
func NewWorkerPool(numWorkers int, queueSize int) *WorkerPool {
	if numWorkers <= 0 {
		numWorkers = runtime.NumCPU()
	}
	if queueSize <= 0 {
		queueSize = 1000
	}

	pool := &WorkerPool{
		tasks:     make(chan Task, queueSize),
		workerNum: int32(numWorkers),
	}

	// Start workers
	pool.wg.Add(numWorkers)
	for i := 0; i < numWorkers; i++ {
		go pool.worker()
	}

	return pool
}

// worker processes tasks from the queue
func (p *WorkerPool) worker() {
	defer p.wg.Done()

	for {
		// Check shutdown flag first
		if p.shutdown.Load() {
			return
		}

		// Get task from queue
		task, ok := <-p.tasks
		if !ok {
			return
		}

		// Execute task
		_ = task.Execute() // Error handling should be done in the task
	}
}

// Submit adds a task to the pool
// Returns false if the pool is full or shutting down
func (p *WorkerPool) Submit(task Task) bool {
	if p.shutdown.Load() {
		return false
	}

	// Try to submit task
	select {
	case p.tasks <- task:
		return true
	default:
		return false
	}
}

// Shutdown gracefully shuts down the worker pool
func (p *WorkerPool) Shutdown() {
	// Set shutdown flag
	p.shutdown.Store(true)

	// Close task channel
	close(p.tasks)

	// Wait for all workers to finish
	p.wg.Wait()
}

// Len returns the current number of tasks in the queue
func (p *WorkerPool) Len() int {
	return len(p.tasks)
}

// Cap returns the capacity of the task queue
func (p *WorkerPool) Cap() int {
	return cap(p.tasks)
}

// WorkerCount returns the number of active workers
func (p *WorkerPool) WorkerCount() int32 {
	return atomic.LoadInt32(&p.workerNum)
}
