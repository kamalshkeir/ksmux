package ksps

import (
	"sync"
	"sync/atomic"
	"time"
	"unique"
	"weak"

	"github.com/kamalshkeir/ksmux/ws"
)

// Bus - Le pub/sub le plus rapide possible en Go 1.24
type Bus struct {
	// Swiss Tables (Go 1.24) + unique handles pour performance maximale
	topics map[unique.Handle[string]]*dataTopic

	// Weak pointers pour auto-cleanup
	subscribers map[uint64]weak.Pointer[subscriber]

	// WebSocket subscribers par topic (optimisé)
	wsSubscribers map[unique.Handle[string]]*wsTopicData

	// Atomic ID generator
	nextID atomic.Uint64

	// ACK system
	ackRequests map[string]*ackRequest // ackID -> request
	ackMu       sync.RWMutex

	// RWMutex pour lectures concurrentes optimales
	mu sync.RWMutex

	// Worker pool avec work stealing
	workers *workerPool

	// Done channel pour arrêter les goroutines
	done chan struct{}
}

// dataTopic - Optimisé pour cache locality
type dataTopic struct {
	// Hot path data (première cache line - 64 bytes)
	active atomic.Bool // 1 byte
	_      [63]byte    // padding pour cache line alignment

	// Cold path data (deuxième cache line)
	eventCh     chan any
	subscribers []weak.Pointer[subscriber]
	mu          sync.RWMutex // Pour subscribers slice
}

// wsTopicData - Données WebSocket par topic (cache-aligned)
type wsTopicData struct {
	// Hot path (première cache line)
	active atomic.Bool // 1 byte
	_      [63]byte    // padding

	// Cold path
	connections map[string]*wsConnection // clientID -> connection
	mu          sync.RWMutex
}

// wsConnection - Connection WebSocket optimisée
type wsConnection struct {
	id     string
	conn   *ws.Conn
	topics map[unique.Handle[string]]bool // topics subscribed
	sendCh chan wsMessage                 // buffered channel pour async send
	active atomic.Bool
	mu     sync.RWMutex
}

// wsMessage - Message WebSocket optimisé
type wsMessage struct {
	Action string `json:"action"`
	Topic  string `json:"topic,omitempty"`
	Data   any    `json:"data,omitempty"`
	From   string `json:"from,omitempty"`
	To     string `json:"to,omitempty"`
	AckID  string `json:"ack_id,omitempty"`
}

// ackRequest - Requête d'acknowledgment
type ackRequest struct {
	id        string
	timestamp time.Time
	timeout   time.Duration
	ackCh     chan ackResponse
	clientIDs []string        // clients qui doivent répondre
	received  map[string]bool // clientID -> ack reçu
	mu        sync.RWMutex
}

// ackResponse - Réponse d'acknowledgment
type ackResponse struct {
	AckID    string `json:"ack_id"`
	ClientID string `json:"client_id"`
	Success  bool   `json:"success"`
	Error    string `json:"error,omitempty"`
}

// Ack - Handle pour attendre les acknowledgments (serveur)
type Ack struct {
	ID      string
	Request *ackRequest
	Bus     *Bus
}

// ClientAck - Handle pour attendre les acknowledgments côté client
type ClientAck struct {
	ID        string
	Client    *Client
	timeout   time.Duration
	responses chan map[string]ackResponse // Réponses du serveur
	status    chan map[string]bool        // Statut du serveur
	cancelled atomic.Bool
	done      chan struct{}
}

// subscriber - Structure optimisée
type subscriber struct {
	id       uint64
	callback func(any, func())
	topic    unique.Handle[string]
	active   atomic.Bool
}

// workerPool - Pool de workers avec work stealing
type workerPool struct {
	workers []chan func()
	next    atomic.Uint64
	size    int
	done    chan struct{}
}
