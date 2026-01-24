package ksps

import (
	"math/bits"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kamalshkeir/ksmux/ws"
)

const (
	// Batching constants
	defaultBatchSize    = 64                   // Messages per batch
	defaultBatchTimeout = 1 * time.Millisecond // Max wait before flush
	maxSpinCount        = 30                   // Spins before yielding
	cacheLineSize       = 64                   // CPU cache line size
)

// BatchWriter is a high-performance lock-free batched writer for WebSocket
// Inspired by APE's FastChan with batching capabilities
type BatchWriter struct {
	_    [cacheLineSize]byte // Prevent false sharing
	ring *messageRing        // Lock-free ring buffer
	conn *ws.Conn            // Target WebSocket connection

	// Batch settings
	batchSize    int
	batchTimeout time.Duration

	// Control
	done   chan struct{}
	wg     sync.WaitGroup
	active atomic.Bool

	// Stats for monitoring
	sent    atomic.Uint64
	dropped atomic.Uint64
}

// messageRing is a lock-free MPSC (Multi-Producer Single-Consumer) ring buffer
type messageRing struct {
	_      [cacheLineSize]byte
	buffer []atomic.Pointer[WsMessage] // 24 bytes
	mask   uint64                      // 8 bytes
	_      [cacheLineSize - 32]byte    // Align to cache line (64 - 32 = 32 padding)

	// Producer (head) - multiple writers
	_    [cacheLineSize]byte
	head atomic.Uint64
	_    [cacheLineSize - 8]byte

	// Consumer (tail) - single reader
	_    [cacheLineSize]byte
	tail atomic.Uint64
	_    [cacheLineSize - 8]byte

	// Signaling for efficient waiting
	mu   sync.Mutex
	cond *sync.Cond
}

// NewBatchWriter creates a new high-performance batch writer
func NewBatchWriter(conn *ws.Conn, ringSize, batchSize int, batchTimeout time.Duration) *BatchWriter {
	if ringSize <= 0 {
		ringSize = 1 << 14 // 16384 slots
	}
	if batchSize <= 0 {
		batchSize = defaultBatchSize
	}
	if batchTimeout <= 0 {
		batchTimeout = defaultBatchTimeout
	}

	// Round up to power of 2
	ringSize = 1 << uint(bits.Len32(uint32(ringSize-1)))

	ring := &messageRing{
		buffer: make([]atomic.Pointer[WsMessage], ringSize),
		mask:   uint64(ringSize - 1),
	}
	ring.cond = sync.NewCond(&ring.mu)

	bw := &BatchWriter{
		ring:         ring,
		conn:         conn,
		batchSize:    batchSize,
		batchTimeout: batchTimeout,
		done:         make(chan struct{}),
	}
	bw.active.Store(true)

	// Start the batch processor
	bw.wg.Add(1)
	go bw.processBatches()

	return bw
}

// Send queues a message for batched sending with backpressure
// Returns true if queued, false if writer is closed
//
//go:nosplit
func (bw *BatchWriter) Send(msg WsMessage) bool {
	if !bw.active.Load() {
		return false
	}

	ring := bw.ring

	// Fast path: try to reserve a slot
	for spins := 0; ; spins++ {
		head := ring.head.Load()
		tail := ring.tail.Load()

		// Check if buffer is full
		nextHead := (head + 1) & ring.mask
		if nextHead == tail {
			// Buffer full - apply backpressure with exponential backoff
			if spins > maxSpinCount*4 {
				// Last resort: drop message after extensive retry
				bw.dropped.Add(1)
				return false
			}
			if spins > maxSpinCount {
				runtime.Gosched()
			}
			continue
		}

		// Try to claim the slot atomically
		if ring.head.CompareAndSwap(head, nextHead) {
			// Get pooled message and copy fields
			pooledMsg := AcquireWsMessage()
			pooledMsg.Action = msg.Action
			pooledMsg.Topic = msg.Topic
			pooledMsg.From = msg.From
			pooledMsg.To = msg.To
			pooledMsg.ID = msg.ID
			pooledMsg.Data = msg.Data
			pooledMsg.Error = msg.Error
			pooledMsg.AckID = msg.AckID
			pooledMsg.Status = msg.Status
			pooledMsg.Responses = msg.Responses

			ring.buffer[head].Store(pooledMsg)
			return true
		}
		// CAS failed, another producer won - retry
	}
}

// TrySend attempts to queue a message without blocking
// Returns true if queued successfully
//
//go:nosplit
func (bw *BatchWriter) TrySend(msg WsMessage) bool {
	if !bw.active.Load() {
		return false
	}

	ring := bw.ring
	head := ring.head.Load()
	tail := ring.tail.Load()

	nextHead := (head + 1) & ring.mask
	if nextHead == tail {
		return false // Buffer full
	}

	if ring.head.CompareAndSwap(head, nextHead) {
		// Get pooled message and copy fields
		pooledMsg := AcquireWsMessage()
		pooledMsg.Action = msg.Action
		pooledMsg.Topic = msg.Topic
		pooledMsg.From = msg.From
		pooledMsg.To = msg.To
		pooledMsg.ID = msg.ID
		pooledMsg.Data = msg.Data
		pooledMsg.Error = msg.Error
		pooledMsg.AckID = msg.AckID
		pooledMsg.Status = msg.Status
		pooledMsg.Responses = msg.Responses

		ring.buffer[head].Store(pooledMsg)
		return true
	}

	return false // CAS failed
}

// processBatches is the single consumer goroutine that batches and sends messages
func (bw *BatchWriter) processBatches() {
	defer bw.wg.Done()

	batch := make([]*WsMessage, 0, bw.batchSize)

	for {
		// Check if we should stop
		select {
		case <-bw.done:
			// Final drain
			bw.drainAndSend(batch[:0])
			return
		default:
		}

		// Try to collect a batch
		batch = bw.collectBatch(batch[:0])

		if len(batch) > 0 {
			bw.sendBatch(batch)
			batch = batch[:0]
			continue
		}

		// No messages - tiny yield to avoid busy spinning
		// Using Gosched instead of cond.Wait for better responsiveness
		runtime.Gosched()
	}
}

// collectBatch collects up to batchSize messages from the ring buffer
//
//go:nosplit
func (bw *BatchWriter) collectBatch(batch []*WsMessage) []*WsMessage {
	ring := bw.ring
	tail := ring.tail.Load()
	head := ring.head.Load()

	// Calculate available messages
	available := int((head - tail) & ring.mask)
	if head < tail {
		available = int(ring.mask + 1 - tail + head)
	}

	if available == 0 {
		return batch
	}

	// Limit to batch size
	if available > bw.batchSize {
		available = bw.batchSize
	}

	// Collect messages
	for i := 0; i < available; i++ {
		idx := (tail + uint64(i)) & ring.mask
		ptr := ring.buffer[idx].Load()
		if ptr != nil {
			batch = append(batch, ptr)
			ring.buffer[idx].Store(nil) // Clear for GC
		}
	}

	// Update tail
	if len(batch) > 0 {
		ring.tail.Store((tail + uint64(len(batch))) & ring.mask)
	}

	return batch
}

// sendBatch sends all messages in the batch to the WebSocket
// Uses zero-copy serialization with pooled buffer for maximum performance
func (bw *BatchWriter) sendBatch(batch []*WsMessage) {
	if len(batch) == 0 || bw.conn == nil {
		return
	}

	// Get a pooled buffer for zero-copy serialization
	bufPtr := kspsBufPool.Get().(*[]byte)
	buf := (*bufPtr)[:0]

	// Send each message using zero-copy serialization
	for _, msg := range batch {
		// Serialize directly to pooled buffer (zero-copy)
		buf = msg.MarshalKSMUXTo(buf[:0])

		// Write raw bytes directly to WebSocket using pooled frame buffer
		if err := bw.conn.WriteMessageDirect(1, buf); err != nil { // 1 = TextMessage
			// Return buffers to pool before returning
			*bufPtr = buf[:0]
			kspsBufPool.Put(bufPtr)
			// Also release any remaining pooled messages
			for _, m := range batch {
				ReleaseWsMessage(m)
			}
			return
		}
		bw.sent.Add(1)

		// Release the pooled message back to pool
		ReleaseWsMessage(msg)
	}

	// Return buffer to pool
	*bufPtr = buf[:0]
	kspsBufPool.Put(bufPtr)
}

// drainAndSend drains remaining messages and sends them
func (bw *BatchWriter) drainAndSend(batch []*WsMessage) {
	for {
		batch = bw.collectBatch(batch[:0])
		if len(batch) == 0 {
			break
		}
		bw.sendBatch(batch)
	}
}

// Close stops the batch writer and drains pending messages
func (bw *BatchWriter) Close() {
	if !bw.active.Swap(false) {
		return // Already closed
	}

	close(bw.done)

	// Wake up the processor
	bw.ring.mu.Lock()
	bw.ring.cond.Broadcast()
	bw.ring.mu.Unlock()

	bw.wg.Wait()
}

// Stats returns sent and dropped message counts
func (bw *BatchWriter) Stats() (sent, dropped uint64) {
	return bw.sent.Load(), bw.dropped.Load()
}

// Size returns the number of pending messages
func (bw *BatchWriter) Size() int {
	head := bw.ring.head.Load()
	tail := bw.ring.tail.Load()
	return int((head - tail) & bw.ring.mask)
}

// ======================================================================
// BatchedWSConnection - Replaces wsConnection with batched sending
// ======================================================================

// BatchedWSConnection is a high-performance WebSocket connection with batching
type BatchedWSConnection struct {
	id     string
	conn   *ws.Conn
	topics map[any]bool // Using any for unique.Handle compatibility
	writer *BatchWriter
	active atomic.Bool
	mu     sync.RWMutex
}

// NewBatchedWSConnection creates a new batched WebSocket connection
func NewBatchedWSConnection(id string, conn *ws.Conn) *BatchedWSConnection {
	bwc := &BatchedWSConnection{
		id:     id,
		conn:   conn,
		topics: make(map[any]bool),
		writer: NewBatchWriter(conn, 16384, 64, time.Millisecond),
	}
	bwc.active.Store(true)
	return bwc
}

// Send queues a message for batched sending
func (bwc *BatchedWSConnection) Send(msg WsMessage) bool {
	if !bwc.active.Load() {
		return false
	}
	return bwc.writer.Send(msg)
}

// TrySend tries to queue a message without blocking
func (bwc *BatchedWSConnection) TrySend(msg WsMessage) bool {
	if !bwc.active.Load() {
		return false
	}
	return bwc.writer.TrySend(msg)
}

// Close closes the batched connection
func (bwc *BatchedWSConnection) Close() {
	if !bwc.active.Swap(false) {
		return
	}
	if bwc.writer != nil {
		bwc.writer.Close()
	}
}

// IsActive returns whether the connection is active
func (bwc *BatchedWSConnection) IsActive() bool {
	return bwc.active.Load()
}

// ======================================================================
// Fast MPMC Channel for internal use
// ======================================================================

// FastMPMC is a lock-free multi-producer multi-consumer queue
type FastMPMC[T any] struct {
	_      [cacheLineSize]byte
	buffer []atomic.Pointer[T]
	mask   uint64
	_      [cacheLineSize - 8]byte

	_    [cacheLineSize]byte
	head atomic.Uint64
	_    [cacheLineSize - 8]byte

	_    [cacheLineSize]byte
	tail atomic.Uint64
	_    [cacheLineSize - 8]byte

	mu   sync.Mutex
	cond *sync.Cond
}

// NewFastMPMC creates a new lock-free MPMC queue
func NewFastMPMC[T any](size int) *FastMPMC[T] {
	if size <= 0 {
		size = 4096
	}
	size = 1 << uint(bits.Len32(uint32(size-1)))

	q := &FastMPMC[T]{
		buffer: make([]atomic.Pointer[T], size),
		mask:   uint64(size - 1),
	}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// Push adds an item to the queue with backpressure
func (q *FastMPMC[T]) Push(item T) bool {
	for spins := 0; ; spins++ {
		head := q.head.Load()
		tail := q.tail.Load()

		nextHead := (head + 1) & q.mask
		if nextHead == tail {
			if spins > maxSpinCount*4 {
				return false
			}
			if spins > maxSpinCount {
				runtime.Gosched()
			}
			continue
		}

		if q.head.CompareAndSwap(head, nextHead) {
			ptr := &item
			q.buffer[head].Store(ptr)
			q.mu.Lock()
			q.cond.Signal()
			q.mu.Unlock()
			return true
		}
	}
}

// TryPush attempts to add an item without blocking
func (q *FastMPMC[T]) TryPush(item T) bool {
	head := q.head.Load()
	tail := q.tail.Load()

	nextHead := (head + 1) & q.mask
	if nextHead == tail {
		return false
	}

	if q.head.CompareAndSwap(head, nextHead) {
		ptr := &item
		q.buffer[head].Store(ptr)
		q.mu.Lock()
		q.cond.Signal()
		q.mu.Unlock()
		return true
	}
	return false
}

// Pop removes and returns an item from the queue
func (q *FastMPMC[T]) Pop() (T, bool) {
	var zero T

	for spins := 0; ; spins++ {
		tail := q.tail.Load()
		head := q.head.Load()

		if tail == head {
			return zero, false
		}

		nextTail := (tail + 1) & q.mask
		if q.tail.CompareAndSwap(tail, nextTail) {
			ptr := q.buffer[tail].Load()
			if ptr != nil {
				q.buffer[tail].Store(nil)
				return *ptr, true
			}
		}

		if spins > maxSpinCount {
			runtime.Gosched()
			spins = 0
		}
	}
}

// PopBatch removes multiple items at once
func (q *FastMPMC[T]) PopBatch(dst []T) int {
	tail := q.tail.Load()
	head := q.head.Load()

	available := int((head - tail) & q.mask)
	if available == 0 {
		return 0
	}

	if available > len(dst) {
		available = len(dst)
	}

	count := 0
	for i := 0; i < available; i++ {
		idx := (tail + uint64(i)) & q.mask
		ptr := q.buffer[idx].Load()
		if ptr != nil {
			dst[count] = *ptr
			q.buffer[idx].Store(nil)
			count++
		}
	}

	if count > 0 {
		q.tail.Store((tail + uint64(count)) & q.mask)
	}

	return count
}

// Wait blocks until data is available or done
func (q *FastMPMC[T]) Wait(done <-chan struct{}) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	for q.head.Load() == q.tail.Load() {
		select {
		case <-done:
			return false
		default:
			q.cond.Wait()
		}
	}
	return true
}

// Signal wakes up waiting consumers
func (q *FastMPMC[T]) Signal() {
	q.mu.Lock()
	q.cond.Signal()
	q.mu.Unlock()
}

// Broadcast wakes up all waiting consumers
func (q *FastMPMC[T]) Broadcast() {
	q.mu.Lock()
	q.cond.Broadcast()
	q.mu.Unlock()
}

// Size returns the number of items in the queue
func (q *FastMPMC[T]) Size() int {
	return int((q.head.Load() - q.tail.Load()) & q.mask)
}
