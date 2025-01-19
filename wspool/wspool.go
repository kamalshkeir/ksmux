package wspool

import (
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kamalshkeir/kmap"
	"github.com/kamalshkeir/ksmux/ws"
)

func init() {
	// Configure default dialer for clients
	ws.DefaultDialer.HandshakeTimeout = HandshakeTimeout
	ws.DefaultDialer.ReadBufferSize = ReadBufferSize
	ws.DefaultDialer.WriteBufferSize = WriteBufferSize
}

var (
	DefaultMaxConns        = 10000 // Support up to 10k connections
	NumShards              = 64    // More shards for better distribution
	ReadBufferSize         = 4096  // 4KB read buffer
	WriteBufferSize        = 4096  // 4KB write buffer
	ReadTimeout            = 60 * time.Second
	WriteTimeout           = 10 * time.Second
	HandshakeTimeout       = 10 * time.Second
	SessionTimeout         = 30 * time.Minute // Session expires after 30 minutes of inactivity
	CleanupInterval        = 5 * time.Minute  // Run cleanup every 5 minutes
	defaultPool            = NewPool()        // Global pool for default upgrades
	DefaultUpgraderWsactor = ws.Upgrader{
		EnableCompression: true,
		ReadBufferSize:    ReadBufferSize,
		WriteBufferSize:   WriteBufferSize,
		HandshakeTimeout:  HandshakeTimeout,
		WriteBufferPool:   &sync.Pool{New: func() interface{} { return writePoolData{buf: make([]byte, WriteBufferSize)} }},
	}
)

type writePoolData struct {
	buf []byte
}

func UpgradeConnection(w http.ResponseWriter, r *http.Request, responseHeader http.Header) (*ws.Conn, error) {
	wsConn, err := DefaultUpgraderWsactor.Upgrade(w, r, responseHeader)
	if err != nil {
		return nil, err
	}
	return wsConn, nil
}

// SessionState represents the current state of a client session
type SessionState struct {
	Data       map[string]interface{}
	CreatedAt  time.Time
	LastActive time.Time
	mu         sync.RWMutex
}

// Client represents a WebSocket client with multiple possible connections
type Client struct {
	id          string
	connections *kmap.SafeMap[*ws.Conn, *Conn] // Maps *ws.Conn to *Conn
	lastActive  time.Time
	session     *SessionState
	mu          sync.RWMutex
}

func (cl *Client) Connections() *kmap.SafeMap[*ws.Conn, *Conn] {
	return cl.connections
}

// Pool manages WebSocket connections with client identification
type Pool struct {
	shards    []*shard
	clients   *kmap.SafeMap[string, *Client] // Maps client ID to *Client
	connCount int32
	maxConns  int32
	counter   uint32 // For round-robin sharding
	stopCh    chan struct{}
}

// shard manages a subset of connections with optimized locking
type shard struct {
	connections *kmap.SafeMap[*ws.Conn, bool]
}

// NewPool creates a new connection pool optimized for high concurrency
func NewPool() *Pool {
	p := &Pool{
		clients:  kmap.New[string, *Client](100),
		shards:   make([]*shard, NumShards),
		maxConns: int32(DefaultMaxConns),
		stopCh:   make(chan struct{}),
	}
	for i := 0; i < NumShards; i++ {
		p.shards[i] = &shard{
			connections: kmap.New[*ws.Conn, bool](),
		}
	}

	// Start session cleanup goroutine
	go p.cleanupSessions()

	return p
}

// cleanupSessions periodically removes expired sessions
func (p *Pool) cleanupSessions() {
	ticker := time.NewTicker(CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			now := time.Now()
			p.clients.Range(func(key string, client *Client) bool {
				client.mu.Lock()
				if client.connections == nil {
					client.connections = kmap.New[*ws.Conn, *Conn](100)
				}
				if now.Sub(client.lastActive) > SessionTimeout {
					// Close all client connections
					client.connections.Range(func(_ *ws.Conn, value *Conn) bool {
						if value != nil {
							value.Close()
						}
						return true
					})
					// Remove client from pool
					p.clients.Delete(key)
				}
				client.mu.Unlock()
				return true
			})
		case <-p.stopCh:
			return
		}
	}
}

// getShard returns the next shard in round-robin fashion
func (p *Pool) getShard() *shard {
	count := atomic.AddUint32(&p.counter, 1)
	return p.shards[count%uint32(NumShards)]
}

// Add adds a WebSocket connection with proper configuration
func (p *Pool) Add(conn *ws.Conn) *Conn {
	if atomic.LoadInt32(&p.connCount) >= p.maxConns {
		return nil
	}

	// Configure timeouts
	conn.SetReadDeadline(time.Now().Add(ReadTimeout))
	conn.SetWriteDeadline(time.Now().Add(WriteTimeout))

	s := p.getShard()
	if s.connections == nil {
		s.connections = kmap.New[*ws.Conn, bool](100)
	}
	if _, loaded := s.connections.GetOrSet(conn, true); !loaded {
		atomic.AddInt32(&p.connCount, 1)
	}

	return &Conn{
		conn:  conn,
		pool:  p,
		shard: s,
	}
}

// AddFromHeaders creates a new connection with client identification from headers
func (p *Pool) AddFromHeaders(conn *ws.Conn, headers http.Header) (*Conn, string) {
	clientID := headers.Get("X-Client-ID")
	if clientID == "" {
		clientID = fmt.Sprintf("client-%d", time.Now().UnixNano())
	}

	// Get or create client
	client, _ := p.clients.GetOrSet(clientID, &Client{
		id:         clientID,
		lastActive: time.Now(),
	})

	s := p.getShard()
	if s.connections == nil {
		s.connections = kmap.New[*ws.Conn, bool](100)
	}

	// Create connection with client ID
	c := &Conn{
		conn:     conn,
		clientID: clientID,
		mu:       sync.RWMutex{},
		pool:     p,
		shard:    s,
	}

	if client.connections == nil {
		client.connections = kmap.New[*ws.Conn, *Conn](100)
	}

	// Store in client's connections
	client.connections.Set(conn, c)

	// Update shard tracking
	if _, loaded := s.connections.GetOrSet(conn, true); !loaded {
		atomic.AddInt32(&p.connCount, 1)
	}

	return c, clientID
}

func (p *Pool) GetClientFromID(clientID string) *Client {
	client, ok := p.clients.Get(clientID)
	if !ok {
		return nil
	}
	return client
}

func (p *Pool) AddClient(conn *ws.Conn, clientID string) *Conn {
	// Get or create client
	client, _ := p.clients.GetOrSet(clientID, &Client{
		id:         clientID,
		lastActive: time.Now(),
	})

	s := p.getShard()

	if s.connections == nil {
		s.connections = kmap.New[*ws.Conn, bool](100)
	}

	// Create connection with client ID
	c := &Conn{
		conn:     conn,
		clientID: clientID,
		mu:       sync.RWMutex{},
		pool:     p,
		shard:    s,
	}

	if client.connections == nil {
		client.connections = kmap.New[*ws.Conn, *Conn](100)
	}

	// Store in client's connections
	client.connections.Set(conn, c)

	// Update shard tracking
	if _, loaded := s.connections.GetOrSet(conn, true); !loaded {
		atomic.AddInt32(&p.connCount, 1)
	}
	return c
}

// BroadcastText sends a message to all connections of a specific client
func (p *Pool) BroadcastText(clientID string, messageType int, data []byte) error {
	if client, ok := p.clients.Get(clientID); ok {
		if client.connections == nil {
			client.connections = kmap.New[*ws.Conn, *Conn](100)
			return fmt.Errorf("no connection found")
		}
		client.connections.Range(func(key *ws.Conn, conn *Conn) bool {
			err := conn.WriteMessage(messageType, data)
			if err != nil {
				fmt.Printf("error broadcasting text to connection: %v\n", err)
				p.RemoveConnection(conn)
			}
			return true
		})
		return nil
	}
	return fmt.Errorf("client %s not found", clientID)
}

// BroadcastJson sends a message to all connections of a specific client
func (p *Pool) BroadcastJson(clientID string, value any) error {
	if client, ok := p.clients.Get(clientID); ok {
		var lastErr error
		if client.connections == nil {
			client.connections = kmap.New[*ws.Conn, *Conn](100)
			return fmt.Errorf("no connection found")
		}
		client.connections.Range(func(key *ws.Conn, conn *Conn) bool {
			if err := conn.WriteJSON(value); err != nil {
				fmt.Printf("error broadcasting to connection: %v\n", err)
				lastErr = err
				p.RemoveConnection(conn)
				// Continue ranging even if one connection fails
				return true
			}
			return true
		})
		if lastErr != nil {
			return fmt.Errorf("broadcast had errors: %v", lastErr)
		}
		return nil
	}
	return fmt.Errorf("client %s not found", clientID)
}

// GetClientConnectionCount returns the number of active connections for a client
func (p *Pool) GetClientConnectionCount(clientID string) int {
	count := 0

	if client, ok := p.clients.Get(clientID); ok {
		if client.connections == nil {
			client.connections = kmap.New[*ws.Conn, *Conn](100)
			return 0
		}
		count = client.connections.Len()
	}
	return count
}

// UpdateClientActivity updates the last active timestamp for a client
func (p *Pool) UpdateClientActivity(clientID string) {
	if client, ok := p.clients.Get(clientID); ok {
		client.lastActive = time.Now()
	}
}

// GetSession returns the session state for a client
func (p *Pool) GetSession(clientID string) *SessionState {
	if client, ok := p.clients.Get(clientID); ok {
		return client.session
	}
	return nil
}

// SetSessionData sets a key-value pair in the client's session
func (p *Pool) SetSessionData(clientID string, key string, value interface{}) error {
	if client, ok := p.clients.Get(clientID); ok {
		client.mu.Lock()
		defer client.mu.Unlock()

		if client.session == nil {
			client.session = &SessionState{
				Data:      make(map[string]interface{}),
				CreatedAt: time.Now(),
			}
		}

		client.session.mu.Lock()
		client.session.Data[key] = value
		client.session.LastActive = time.Now()
		client.session.mu.Unlock()

		return nil
	}
	return fmt.Errorf("client %s not found", clientID)
}

// GetSessionData retrieves a value from the client's session
func (p *Pool) GetSessionData(clientID string, key string) (interface{}, error) {
	if client, ok := p.clients.Get(clientID); ok {
		client.mu.RLock()
		defer client.mu.RUnlock()

		if client.session == nil {
			return nil, fmt.Errorf("no session for client %s", clientID)
		}

		client.session.mu.RLock()
		defer client.session.mu.RUnlock()

		if value, exists := client.session.Data[key]; exists {
			return value, nil
		}
		return nil, fmt.Errorf("key %s not found in session", key)
	}
	return nil, fmt.Errorf("client %s not found", clientID)
}

// Close closes all connections and stops the cleanup goroutine
func (p *Pool) Close() {
	close(p.stopCh)
	for _, s := range p.shards {
		if s.connections == nil {
			s.connections = kmap.New[*ws.Conn, bool](100)
			continue
		}
		connections := s.connections.Keys()
		for _, conn := range connections {
			if conn != nil {
				_ = conn.WriteControl(
					ws.CloseMessage,
					ws.FormatCloseMessage(ws.CloseNormalClosure, "server shutdown"),
					time.Now().Add(WriteTimeout),
				)
				conn.Close()
			}
		}
	}
}

// Conn represents a WebSocket connection with client identification
type Conn struct {
	conn     *ws.Conn
	clientID string
	mu       sync.RWMutex
	pool     *Pool
	shard    *shard
}

func (c *Conn) WriteJSON(v any) error {
	return c.conn.WriteJSON(v)
}

func (c *Conn) ReadJSON(v interface{}) error {
	return c.conn.ReadJSON(v)
}

// WriteMessage sends a message using read-write mutex for better concurrency
func (c *Conn) WriteMessage(messageType int, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Reset write deadline
	c.conn.SetWriteDeadline(time.Now().Add(WriteTimeout))
	return c.conn.WriteMessage(messageType, data)
}

// ReadMessage reads a message with proper locking
func (c *Conn) ReadMessage() (messageType int, p []byte, err error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	// Reset read deadline
	c.conn.SetReadDeadline(time.Now().Add(ReadTimeout))
	return c.conn.ReadMessage()
}

// Close performs a clean WebSocket close handshake
func (c *Conn) Close() error {
	// Remove from pool first
	c.pool.RemoveConnection(c)

	// Send close frame with timeout
	_ = c.conn.WriteControl(
		ws.CloseMessage,
		ws.FormatCloseMessage(ws.CloseNormalClosure, ""),
		time.Now().Add(WriteTimeout),
	)

	return c.conn.Close()
}

func DefaultPool() *Pool {
	return defaultPool
}

// GetClientID returns the client identifier for this connection
func (c *Conn) GetClientID() string {
	return c.clientID
}

func (c *Conn) SetClientID(clientID string) {
	c.clientID = clientID
}

func (c *Conn) WSConn() *ws.Conn {
	return c.conn
}

func (c *Conn) Pool() *Pool {
	return c.pool
}

func (c *Conn) MutexLock() {
	c.mu.Lock()
}

func (c *Conn) MutexRLock() {
	c.mu.RLock()
}

func (c *Conn) MutexUnlock() {
	c.mu.Unlock()
}

func (c *Conn) MutexRUnlock() {
	c.mu.RUnlock()
}

// RemoveConnection removes a connection from both the shard and client maps
func (p *Pool) RemoveConnection(conn *Conn) {
	if client, ok := p.clients.Get(conn.clientID); ok {
		if client.connections == nil {
			client.connections = kmap.New[*ws.Conn, *Conn](100)
			return
		}
		client.connections.Delete(conn.conn)
		// If this was the last connection for this client, remove the client
		count := client.connections.Len()
		if count == 0 {
			p.clients.Delete(conn.clientID)
		}
	}
	if conn.shard.connections == nil {
		conn.shard.connections = kmap.New[*ws.Conn, bool](100)
		return
	}
	conn.shard.connections.Delete(conn.conn)
	atomic.AddInt32(&p.connCount, -1)
}
