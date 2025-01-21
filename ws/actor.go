package ws

import (
	"math/bits"
	"math/rand"
	"runtime"
	"sync"
	"sync/atomic"
)

type MessageOP interface{}

type messageQueueOP struct {
	_      [8]uint64
	buffer []MessageOP
	head   int32
	tail   int32
	_      [8]uint64
}

type ActorOP struct {
	queues      []messageQueueOP
	done        chan struct{}
	handler     func(msgs []MessageOP)
	batchSize   int
	processWg   sync.WaitGroup
	workerCount int32
	mask        int32
	mu          sync.Mutex
	stopped     bool
}

func NewActorOP(queueSize, batchSize int, handler func(msgs []MessageOP)) *ActorOP {
	if queueSize <= 0 {
		queueSize = 1 << 21
	}
	if batchSize <= 0 {
		batchSize = 8192
	}

	queueSize = 1 << uint(32-bits.LeadingZeros32(uint32(queueSize-1)))
	workerCount := int32(runtime.NumCPU())
	queues := make([]messageQueueOP, workerCount)
	for i := range queues {
		queues[i] = messageQueueOP{
			buffer: make([]MessageOP, queueSize),
		}
	}

	return &ActorOP{
		queues:      queues,
		done:        make(chan struct{}),
		handler:     handler,
		batchSize:   batchSize,
		workerCount: workerCount,
		mask:        int32(queueSize - 1),
		stopped:     false,
	}
}

func (a *ActorOP) Start() {
	workerCount := atomic.LoadInt32(&a.workerCount)
	a.processWg.Add(int(workerCount))

	for i := 0; i < int(workerCount); i++ {
		go func(id int) {
			runtime.LockOSThread()
			a.processBatch(id)
		}(i)
	}
}

func (a *ActorOP) Stop() {
	a.mu.Lock()
	if !a.stopped {
		a.stopped = true
		close(a.done)
	}
	a.mu.Unlock()
	a.processWg.Wait()
}

//go:nosplit
//go:noinline
func (a *ActorOP) Send(msg MessageOP, workerID int) bool {
	if workerID == -1 {
		// Try all workers in sequence starting from a random one
		startWorker := int(rand.Int31n(a.workerCount))
		for i := 0; i < int(a.workerCount); i++ {
			workerID = (startWorker + i) % int(a.workerCount)
			q := &a.queues[workerID]
			tail := atomic.LoadInt32(&q.tail)
			nextTail := (tail + 1) & a.mask
			if nextTail != atomic.LoadInt32(&q.head) {
				q.buffer[tail] = msg
				atomic.StoreInt32(&q.tail, nextTail)
				return true
			}
		}
		return false
	}

	// Direct worker selection
	q := &a.queues[workerID]
	tail := atomic.LoadInt32(&q.tail)
	nextTail := (tail + 1) & a.mask
	if nextTail == atomic.LoadInt32(&q.head) {
		return false
	}
	q.buffer[tail] = msg
	atomic.StoreInt32(&q.tail, nextTail)
	return true
}

//go:nosplit
func (a *ActorOP) processBatch(workerID int) {
	defer a.processWg.Done()

	q := &a.queues[workerID]
	batch := make([]MessageOP, a.batchSize)
	localHead := atomic.LoadInt32(&q.head)

	for {
		select {
		case <-a.done:
			// Process any remaining messages before exiting
			tail := atomic.LoadInt32(&q.tail)
			if localHead != tail {
				available := int((tail - localHead) & a.mask)
				if available > a.batchSize {
					available = a.batchSize
				}
				idx := localHead & a.mask
				for i := 0; i < available; i++ {
					batch[i] = q.buffer[idx]
					idx = (idx + 1) & a.mask
				}
				a.handler(batch[:available])
			}
			return
		default:
			tail := atomic.LoadInt32(&q.tail)
			if localHead == tail {
				// No messages, check done channel more frequently
				runtime.Gosched()
				continue
			}

			// Process all available messages in smaller batches
			for {
				available := int((tail - localHead) & a.mask)
				if available == 0 {
					break
				}

				// Process in smaller chunks to avoid holding up other operations
				toProcess := available
				if toProcess > a.batchSize {
					toProcess = a.batchSize
				}

				idx := localHead & a.mask
				for i := 0; i < toProcess; i++ {
					batch[i] = q.buffer[idx]
					idx = (idx + 1) & a.mask
				}

				a.handler(batch[:toProcess])
				localHead = (localHead + int32(toProcess)) & a.mask
				atomic.StoreInt32(&q.head, localHead)

				// Check if more messages arrived
				tail = atomic.LoadInt32(&q.tail)
			}
		}
	}
}
