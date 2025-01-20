package ws

import (
	"sync"
	"sync/atomic"
)

// Frame size buckets in bytes (powers of 2 for efficient sizing)
const (
	minFrameSize   = 64
	maxFrameSize   = 1 << 16 // 64KB
	numSizeBuckets = 11      // 64B to 64KB
)

// FramePool manages pre-prepared WebSocket frames for efficient sending
type FramePool struct {
	pools    [numSizeBuckets]sync.Pool
	hits     atomic.Uint64
	misses   atomic.Uint64
	prepared atomic.Uint64
}

// frameBucket represents a cached frame of a specific size
type frameBucket struct {
	frame    *PreparedMessage
	size     int
	lastUsed int64 // unix nano
}

// NewFramePool creates a new frame pool
func NewFramePool() *FramePool {
	fp := &FramePool{}

	// Initialize pools for each size bucket
	for i := 0; i < numSizeBuckets; i++ {
		size := minFrameSize << i
		fp.pools[i] = sync.Pool{
			New: func() interface{} {
				fp.misses.Add(1)
				return &frameBucket{
					size: size,
				}
			},
		}
	}

	return fp
}

// getSizeBucket returns the appropriate bucket index for a given size
func (fp *FramePool) getSizeBucket(size int) int {
	if size <= minFrameSize {
		return 0
	}
	if size >= maxFrameSize {
		return numSizeBuckets - 1
	}

	// Find the smallest bucket that fits the size
	for i := 0; i < numSizeBuckets; i++ {
		if size <= minFrameSize<<i {
			return i
		}
	}
	return numSizeBuckets - 1
}

// GetPreparedFrame gets or creates a prepared frame for the given message
func (fp *FramePool) GetPreparedFrame(messageType int, data []byte) (*PreparedMessage, error) {
	size := len(data)
	bucket := fp.getSizeBucket(size)

	// Try to get from pool
	fb := fp.pools[bucket].Get().(*frameBucket)

	// Check if we can reuse existing frame
	if fb.frame != nil && len(fb.frame.data) >= size {
		fp.hits.Add(1)
		return fb.frame, nil
	}

	// Create new prepared frame with actual data
	frame, err := NewPreparedMessage(messageType, data)
	if err != nil {
		return nil, err
	}

	fb.frame = frame
	fp.prepared.Add(1)
	fp.pools[bucket].Put(fb)

	return frame, nil
}

// Put returns a frame to the pool
func (fp *FramePool) Put(frame *PreparedMessage) {
	if frame == nil {
		return
	}

	size := len(frame.data)
	bucket := fp.getSizeBucket(size)

	fb := &frameBucket{
		frame:    frame,
		size:     size,
		lastUsed: 0, // Reset usage counter
	}

	fp.pools[bucket].Put(fb)
}

// Stats returns pool statistics
func (fp *FramePool) Stats() (hits, misses, prepared uint64) {
	return fp.hits.Load(), fp.misses.Load(), fp.prepared.Load()
}

// Clear empties all pools
func (fp *FramePool) Clear() {
	for i := range fp.pools {
		fp.pools[i] = sync.Pool{}
	}
	fp.hits.Store(0)
	fp.misses.Store(0)
	fp.prepared.Store(0)
}
