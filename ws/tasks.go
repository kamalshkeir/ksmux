package ws

import (
	"io"
	"time"
)

// WriteTask represents a write operation to be executed
type WriteTask struct {
	frames   [][]byte   // Frames to write
	deadline time.Time  // Write deadline
	done     chan error // Signals completion with error if any
	conn     *Conn      // Connection to write to (changed to *Conn for mutex access)
}

func NewWriteTask(frames [][]byte, deadline time.Time, done chan error, conn *Conn) *WriteTask {
	return &WriteTask{
		frames:   frames,
		deadline: deadline,
		done:     done,
		conn:     conn,
	}
}

func (t *WriteTask) Execute() error {
	if t.conn == nil {
		t.done <- errWriteClosed
		return errWriteClosed
	}

	// Set deadline
	if err := t.conn.conn.SetWriteDeadline(t.deadline); err != nil {
		t.done <- err
		return err
	}

	// Write all frames
	for _, frame := range t.frames {
		t.conn.writeMu.Lock()
		_, err := t.conn.conn.Write(frame)
		t.conn.writeMu.Unlock()
		if err != nil {
			t.done <- err
			return err
		}
	}

	t.done <- nil
	return nil
}

// ReadTask represents a read operation to be executed
type ReadTask struct {
	conn *Conn
	done chan readResult
}

func NewReadTask(conn *Conn, done chan readResult) *ReadTask {
	return &ReadTask{
		conn: conn,
		done: done,
	}
}

func (t *ReadTask) Execute() error {
	// Get buffer from pool
	buf := bufferPool.Get().([]byte)
	defer bufferPool.Put(buf)

	messageType, r, err := t.conn.NextReader()
	if err != nil {
		t.done <- readResult{messageType: messageType, err: err}
		return err
	}

	// Read into buffer
	data, err := io.ReadAll(r)
	if err != nil {
		t.done <- readResult{messageType: messageType, err: err}
		return err
	}

	// Copy data to avoid race conditions
	dataCopy := make([]byte, len(data))
	copy(dataCopy, data)

	select {
	case t.done <- readResult{
		messageType: messageType,
		data:        dataCopy,
		err:         err,
	}:
	default:
		// If we can't send the result immediately, keep trying
		go func(result readResult) {
			t.done <- result
		}(readResult{messageType: messageType, data: dataCopy, err: err})
	}

	return nil
}
