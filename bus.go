package ksmux

import (
	"context"
	"fmt"
	"log"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kamalshkeir/ksmux/jsonencdec"
	"github.com/kamalshkeir/ksmux/ws"
	"github.com/kamalshkeir/lg"
)

// Message représente un message dans le pubsub
type Message struct {
	Topic    string `json:"topic"`
	Data     any    `json:"data"`
	ID       uint64 `json:"id,omitempty"`
	Time     int64  `json:"time,omitempty"`
	NeedsAck bool   `json:"needsAck,omitempty"`
	IsAck    bool   `json:"isAck,omitempty"`
	AckFor   uint64 `json:"ackFor,omitempty"`
	From     string `json:"from,omitempty"`
}

// Subscription représente un abonnement avec fonction de désabonnement
type Subscription struct {
	ID          uint64
	Topic       string
	Unsubscribe func()
}

// Client représente une connexion client
type Client struct {
	ID       uint64
	ClientID string // ID unique du client (pour messaging direct)
	Conn     *ws.Conn
	Topics   map[string]bool
	SendCh   chan []byte
	CloseCh  chan struct{}
	IsActive int32
	mu       sync.RWMutex
}

// InternalSubscriber représente un abonné interne (sans WebSocket)
type InternalSubscriber struct {
	ID          uint64
	ClientID    string                      // ID unique pour messaging direct
	Callback    func(any, *Subscription)    // Modifié: inclut Subscription avec Unsubscribe
	onIDMessage func(string, any, *Message) // callback pour messages directs
	Topics      map[string]bool
	mu          sync.RWMutex
}

// AckWaiter gère l'attente des ACKs
type AckWaiter struct {
	MessageID    uint64
	Topic        string
	ExpectedAcks int
	ReceivedAcks int
	AckChan      chan *Message
	TimeoutChan  chan struct{}
	Responses    []*Message
	mu           sync.Mutex
}

// BusServer est le serveur pubsub ultra-rapide
type BusServer struct {
	addr           string
	router         *Router
	clients        map[uint64]*Client
	topics         map[string]map[uint64]*Client
	internalTopics map[string]map[uint64]*InternalSubscriber
	internalSubs   map[uint64]*InternalSubscriber
	pendingAcks    map[uint64]*AckWaiter          // gestion des ACKs
	clientsByID    map[string]*Client             // mapping ID -> Client pour messaging direct
	internalsByID  map[string]*InternalSubscriber // mapping ID -> InternalSubscriber

	// Optimisation: utiliser des RWMutex séparés pour réduire la contention
	clientsMu        sync.RWMutex
	topicsMu         sync.RWMutex
	internalTopicsMu sync.RWMutex
	internalSubsMu   sync.RWMutex
	pendingAcksMu    sync.RWMutex // mutex pour ACKs
	clientsByIDMu    sync.RWMutex // mutex pour mapping ID

	nextID         uint64
	nextMsgID      uint64
	nextInternalID uint64
	stats          ServerStats
	ctx            context.Context
	cancel         context.CancelFunc
	serverID       string                      // ID unique du serveur
	onIDMessage    func(string, any, *Message) // callback pour messages directs

	// Message réutilisable pour zéro allocation
	reusableMsg *Message
}

type ServerStats struct {
	Connections   int64
	Messages      int64
	BytesSent     int64
	BytesReceived int64
}

// NewBusServer crée une nouvelle instance du serveur pubsub
func NewBusServer(config Config) *BusServer {
	ctx, cancel := context.WithCancel(context.Background())
	if config.Address == "" {
		if config.Domain != "" {
			if !strings.Contains(config.Domain, ":") {
				config.Address = config.Domain + ":443"
				lg.WarnC("no port has been specified, using :443")
			} else {
				config.Address = config.Domain
			}
		}
	}
	ps := &BusServer{
		addr:           config.Address,
		router:         New(config),
		clients:        make(map[uint64]*Client),
		topics:         make(map[string]map[uint64]*Client),
		internalTopics: make(map[string]map[uint64]*InternalSubscriber),
		internalSubs:   make(map[uint64]*InternalSubscriber),
		pendingAcks:    make(map[uint64]*AckWaiter),
		clientsByID:    make(map[string]*Client),
		internalsByID:  make(map[string]*InternalSubscriber),
		ctx:            ctx,
		cancel:         cancel,
		serverID:       fmt.Sprintf("server-%d", time.Now().UnixNano()),
		reusableMsg:    &Message{},
	}

	// Configuration des routes
	ps.router.Get("/ws", ps.handleWebSocket)

	return ps
}

func (ps *BusServer) App() *Router {
	return ps.router
}

func (ps *BusServer) Router() *Router {
	return ps.router
}

func (ps *BusServer) Run() {
	ps.router.Run()
}
func (ps *BusServer) RunTLS() {
	ps.router.RunTLS()
}
func (ps *BusServer) RunAutoTLS() {
	ps.router.RunAutoTLS()
}

// Stop arrête le serveur
func (ps *BusServer) Stop() {
	ps.cancel()
	ps.App().Stop()
}

// handleWebSocket gère les connexions WebSocket
func (ps *BusServer) handleWebSocket(c *Context) {
	// Upgrade de la connexion HTTP vers WebSocket avec l'upgrader par défaut
	conn, err := ws.DefaultUpgraderKSMUX.Upgrade(c.ResponseWriter, c.Request, nil)
	if err != nil {
		log.Printf("Erreur upgrade WebSocket: %v", err)
		return
	}

	ps.handleConnection(conn)
}

// handleConnection gère une connexion client
func (ps *BusServer) handleConnection(conn *ws.Conn) {
	clientID := atomic.AddUint64(&ps.nextID, 1)
	atomic.AddInt64(&ps.stats.Connections, 1)

	client := &Client{
		ID:       clientID,
		ClientID: fmt.Sprintf("client-%d", clientID),
		Conn:     conn,
		Topics:   make(map[string]bool),
		SendCh:   make(chan []byte, 1024),
		CloseCh:  make(chan struct{}),
		IsActive: 1,
	}

	ps.clientsMu.Lock()
	ps.clients[clientID] = client
	ps.clientsMu.Unlock()

	// Enregistrer dans le mapping ID pour messaging direct
	ps.clientsByIDMu.Lock()
	ps.clientsByID[client.ClientID] = client
	ps.clientsByIDMu.Unlock()

	log.Printf("Client %d connecté (ID: %s)", clientID, client.ClientID)

	// Goroutine d'écriture
	go ps.clientWriter(client)

	// Goroutine de lecture (bloquante)
	ps.clientReader(client)
}

// clientReader lit les messages du client
func (ps *BusServer) clientReader(client *Client) {
	defer func() {
		ps.disconnectClient(client)
	}()

	for {
		if atomic.LoadInt32(&client.IsActive) == 0 {
			break
		}

		var msg Message
		err := client.Conn.ReadJSON(&msg)
		if err != nil {
			if !ws.IsCloseError(err, ws.CloseGoingAway, ws.CloseAbnormalClosure) {
				log.Printf("Erreur lecture client %d: %v", client.ID, err)
			}
			break
		}

		atomic.AddInt64(&ps.stats.Messages, 1)
		ps.handleClientMessage(client, &msg)
	}
}

// clientWriter écrit les messages vers le client
func (ps *BusServer) clientWriter(client *Client) {
	ticker := time.NewTicker(54 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-client.CloseCh:
			return
		case message, ok := <-client.SendCh:
			if !ok {
				return
			}

			client.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := client.Conn.WriteMessage(ws.TextMessage, message); err != nil {
				log.Printf("Erreur écriture client %d: %v", client.ID, err)
				return
			}
			atomic.AddInt64(&ps.stats.BytesSent, int64(len(message)))

		case <-ticker.C:
			client.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := client.Conn.WriteMessage(ws.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// handleClientMessage traite les messages reçus du client
func (ps *BusServer) handleClientMessage(client *Client, msg *Message) {
	switch msg.Topic {
	case "subscribe":
		if topic, ok := msg.Data.(string); ok {
			ps.subscribe(client, topic)
		}
	case "unsubscribe":
		if topic, ok := msg.Data.(string); ok {
			ps.unsubscribe(client, topic)
		}
	case "__direct__":
		// Message direct adressé à un ID spécifique
		if directData, ok := msg.Data.(map[string]any); ok {
			if targetID, ok := directData["targetID"].(string); ok {
				if data, ok := directData["data"]; ok {
					ps.handleDirectMessage(msg.From, targetID, data, msg)
					return
				}
			}
		}
	default:
		// Protection automatique contre les boucles infinies
		// Si le message vient de ce serveur, ne pas le republier
		if msg.From == ps.serverID {
			return
		}

		// Ajouter l'ID du client comme source si pas déjà défini
		if msg.From == "" {
			msg.From = fmt.Sprintf("client-%d", client.ID)
		}

		// Vérifier si c'est un ACK
		if msg.IsAck && msg.AckFor > 0 {
			ps.handleAck(msg)
			return
		}

		// Si le message nécessite un ACK, envoyer automatiquement
		if msg.NeedsAck && msg.ID > 0 {
			ps.sendAutoAck(client, msg)
		}

		// Republier le message vers tous les abonnés
		ps.Publish(msg.Topic, msg.Data)
	}
}

// subscribe abonne un client à un topic
func (ps *BusServer) subscribe(client *Client, topic string) {
	client.mu.Lock()
	client.Topics[topic] = true
	client.mu.Unlock()

	// Ajouter à la liste des abonnés du topic
	ps.topicsMu.Lock()
	if ps.topics[topic] == nil {
		ps.topics[topic] = make(map[uint64]*Client)
	}
	ps.topics[topic][client.ID] = client
	ps.topicsMu.Unlock()

	log.Printf("Client %d abonné au topic: %s", client.ID, topic)
}

// unsubscribe désabonne un client d'un topic
func (ps *BusServer) unsubscribe(client *Client, topic string) {
	client.mu.Lock()
	delete(client.Topics, topic)
	client.mu.Unlock()

	ps.topicsMu.Lock()
	if subscribers, ok := ps.topics[topic]; ok {
		delete(subscribers, client.ID)
		// Si plus d'abonnés, supprimer le topic
		if len(subscribers) == 0 {
			delete(ps.topics, topic)
		}
	}
	ps.topicsMu.Unlock()

	log.Printf("Client %d désabonné du topic: %s", client.ID, topic)
}

// Subscribe - API interne directe comme kactor (sans WebSocket) - OPTIMISÉ
func (ps *BusServer) Subscribe(topic string, callback func(any, *Subscription)) func() {
	subID := atomic.AddUint64(&ps.nextInternalID, 1)
	clientID := fmt.Sprintf("internal-%d", subID)

	// Créer la fonction de désabonnement
	unsubscribeFunc := func() {
		ps.unsubscribeInternalOptimized(subID, topic, nil)
	}

	// Utiliser le pool d'objets
	subscriber := &InternalSubscriber{
		ID:          subID,
		ClientID:    clientID,
		Callback:    callback,
		onIDMessage: nil,
		Topics:      make(map[string]bool, 4),
	}

	subscriber.Topics[topic] = true

	// Stocker l'abonné
	ps.internalSubsMu.Lock()
	ps.internalSubs[subID] = subscriber
	ps.internalSubsMu.Unlock()

	// Enregistrer dans le mapping ID pour messaging direct
	ps.clientsByIDMu.Lock()
	ps.internalsByID[clientID] = subscriber
	ps.clientsByIDMu.Unlock()

	// Ajouter au topic avec optimisation
	ps.internalTopicsMu.Lock()
	if ps.internalTopics[topic] == nil {
		ps.internalTopics[topic] = make(map[uint64]*InternalSubscriber, 8) // Pré-allouer
	}
	ps.internalTopics[topic][subID] = subscriber
	ps.internalTopicsMu.Unlock()

	// Retourner la fonction de désabonnement optimisée
	return unsubscribeFunc
}

// unsubscribeInternalOptimized - version optimisée du désabonnement
func (ps *BusServer) unsubscribeInternalOptimized(subID uint64, topic string, subscriber *InternalSubscriber) {
	// Si subscriber n'est pas fourni, le récupérer depuis la map
	if subscriber == nil {
		ps.internalSubsMu.RLock()
		subscriber = ps.internalSubs[subID]
		ps.internalSubsMu.RUnlock()

		if subscriber == nil {
			// Abonnement déjà supprimé
			return
		}
	}

	subscriber.mu.Lock()
	delete(subscriber.Topics, topic)
	hasTopics := len(subscriber.Topics) > 0
	subscriber.mu.Unlock()

	ps.internalTopicsMu.Lock()
	if subscribers, ok := ps.internalTopics[topic]; ok {
		delete(subscribers, subID)
		// Si plus d'abonnés, supprimer le topic
		if len(subscribers) == 0 {
			delete(ps.internalTopics, topic)
		}
	}
	ps.internalTopicsMu.Unlock()

	// Si plus d'abonnements, nettoyer et remettre dans le pool
	if !hasTopics {
		ps.internalSubsMu.Lock()
		delete(ps.internalSubs, subID)
		ps.internalSubsMu.Unlock()

		ps.clientsByIDMu.Lock()
		delete(ps.internalsByID, subscriber.ClientID)
		ps.clientsByIDMu.Unlock()

		// Remettre dans le pool pour réutilisation
		subscriber.Topics = nil
	}
}

// Publish modifié pour supporter les abonnés internes ET WebSocket
func (ps *BusServer) Publish(topic string, data any) {
	msgID := atomic.AddUint64(&ps.nextMsgID, 1)

	message := Message{
		Topic: topic,
		Data:  data,
		ID:    msgID,
		Time:  time.Now().Unix(),
	}

	// 1. Publier aux abonnés internes (ultra-rapide, pas de sérialisation)
	ps.internalTopicsMu.RLock()
	if subscribers, ok := ps.internalTopics[topic]; ok {
		for subID, subscriber := range subscribers {
			// Créer la Subscription avec la bonne fonction Unsubscribe
			subscription := &Subscription{
				ID:    subID,
				Topic: topic,
				Unsubscribe: func() {
					ps.unsubscribeInternalOptimized(subID, topic, subscriber)
				},
			}
			// Appel direct du callback - ultra-rapide !
			go subscriber.Callback(data, subscription)
		}
	}
	ps.internalTopicsMu.RUnlock()

	// 2. Publier aux clients WebSocket (pour compatibilité navigateur)
	messageBytes, err := jsonencdec.DefaultMarshal(message)
	if err != nil {
		log.Printf("Erreur sérialisation message: %v", err)
		return
	}

	ps.topicsMu.RLock()
	if subscribers, ok := ps.topics[topic]; ok {
		for _, client := range subscribers {
			if atomic.LoadInt32(&client.IsActive) == 1 {
				select {
				case client.SendCh <- messageBytes:
				default:
					// Canal plein, déconnecter le client lent
					log.Printf("Client %d lent, déconnexion", client.ID)
					go ps.disconnectClient(client)
				}
			}
		}
	}
	ps.topicsMu.RUnlock()

	atomic.AddInt64(&ps.stats.Messages, 1)
}

// PublishInternal - Version ultra-optimisée pour abonnés internes uniquement
func (ps *BusServer) PublishInternal(topic string, data any) {
	// Publier directement aux abonnés internes
	ps.internalTopicsMu.RLock()
	subscribers, exists := ps.internalTopics[topic]
	if !exists {
		ps.internalTopicsMu.RUnlock()
		return
	}

	for subID, subscriber := range subscribers {
		// Créer la Subscription avec la bonne fonction Unsubscribe
		subscription := &Subscription{
			ID:    subID,
			Topic: topic,
			Unsubscribe: func() {
				ps.unsubscribeInternalOptimized(subID, topic, subscriber)
			},
		}

		// Appel direct - pas de goroutine pour performance maximale
		subscriber.Callback(data, subscription)
	}
	ps.internalTopicsMu.RUnlock()

	atomic.AddInt64(&ps.stats.Messages, 1)
}

// PublishInternalBatch - Nouvelle méthode pour publier en lot (encore plus rapide)
func (ps *BusServer) PublishInternalBatch(topic string, dataSlice []any) {
	// Charger les abonnés
	ps.internalTopicsMu.RLock()
	subscribers, exists := ps.internalTopics[topic]
	if !exists {
		ps.internalTopicsMu.RUnlock()
		return
	}

	// Publier tous les messages en lot
	for _, data := range dataSlice {
		for subID, subscriber := range subscribers {
			// Créer la Subscription avec la bonne fonction Unsubscribe
			subscription := &Subscription{
				ID:    subID,
				Topic: topic,
				Unsubscribe: func() {
					ps.unsubscribeInternalOptimized(subID, topic, subscriber)
				},
			}

			// Appel direct
			subscriber.Callback(data, subscription)
		}
	}
	ps.internalTopicsMu.RUnlock()

	atomic.AddInt64(&ps.stats.Messages, int64(len(dataSlice)))
}

// GetInternalStats retourne les statistiques incluant les abonnés internes
func (ps *BusServer) GetInternalStats() map[string]any {
	ps.internalSubsMu.RLock()
	internalSubsCount := len(ps.internalSubs)
	ps.internalSubsMu.RUnlock()

	return map[string]any{
		"websocket_connections": atomic.LoadInt64(&ps.stats.Connections),
		"internal_subscribers":  internalSubsCount,
		"total_messages":        atomic.LoadInt64(&ps.stats.Messages),
		"bytes_sent":            atomic.LoadInt64(&ps.stats.BytesSent),
		"bytes_received":        atomic.LoadInt64(&ps.stats.BytesReceived),
	}
}

// disconnectClient déconnecte un client
func (ps *BusServer) disconnectClient(client *Client) {
	if !atomic.CompareAndSwapInt32(&client.IsActive, 1, 0) {
		return // Déjà déconnecté
	}

	// Supprimer de tous les topics
	client.mu.RLock()
	topics := make([]string, 0, len(client.Topics))
	for topic := range client.Topics {
		topics = append(topics, topic)
	}
	client.mu.RUnlock()

	for _, topic := range topics {
		ps.unsubscribe(client, topic)
	}

	// Nettoyer les mappings
	ps.clientsMu.Lock()
	delete(ps.clients, client.ID)
	ps.clientsMu.Unlock()

	// Nettoyer le mapping ID
	ps.clientsByIDMu.Lock()
	delete(ps.clientsByID, client.ClientID)
	ps.clientsByIDMu.Unlock()

	close(client.CloseCh)
	close(client.SendCh)
	client.Conn.Close()

	atomic.AddInt64(&ps.stats.Connections, -1)
	log.Printf("Client %d déconnecté (ID: %s)", client.ID, client.ClientID)
}

// GetStats retourne les statistiques du serveur
func (ps *BusServer) GetStats() ServerStats {
	return ServerStats{
		Connections:   atomic.LoadInt64(&ps.stats.Connections),
		Messages:      atomic.LoadInt64(&ps.stats.Messages),
		BytesSent:     atomic.LoadInt64(&ps.stats.BytesSent),
		BytesReceived: atomic.LoadInt64(&ps.stats.BytesReceived),
	}
}

// GetServerID retourne l'ID unique du serveur
func (ps *BusServer) GetServerID() string {
	return ps.serverID
}

// handleAck traite la réception d'un ACK
func (ps *BusServer) handleAck(ackMsg *Message) {
	ps.pendingAcksMu.Lock()
	waiter, exists := ps.pendingAcks[ackMsg.AckFor]
	ps.pendingAcksMu.Unlock()

	if !exists {
		return // ACK pour un message qui n'attend plus
	}

	waiter.mu.Lock()
	waiter.ReceivedAcks++
	waiter.Responses = append(waiter.Responses, ackMsg)

	// Envoyer l'ACK au canal
	select {
	case waiter.AckChan <- ackMsg:
	default:
	}

	// Si tous les ACKs sont reçus, fermer le canal
	if waiter.ReceivedAcks >= waiter.ExpectedAcks {
		close(waiter.AckChan)
	}
	waiter.mu.Unlock()
}

// sendAutoAck envoie automatiquement un ACK pour un message reçu
func (ps *BusServer) sendAutoAck(client *Client, originalMsg *Message) {
	ackMsg := &Message{
		Topic:  originalMsg.Topic,
		Data:   map[string]any{"status": "received", "timestamp": time.Now().Unix()},
		ID:     atomic.AddUint64(&ps.nextMsgID, 1),
		Time:   time.Now().Unix(),
		IsAck:  true,
		AckFor: originalMsg.ID,
		From:   ps.serverID,
	}

	// Envoyer l'ACK directement au client qui a envoyé le message
	ackBytes, err := jsonencdec.DefaultMarshal(ackMsg)
	if err != nil {
		log.Printf("Erreur sérialisation ACK: %v", err)
		return
	}

	select {
	case client.SendCh <- ackBytes:
	default:
		log.Printf("Canal client %d plein, ACK perdu", client.ID)
	}
}

// PublishAndWait publie un message et attend les ACKs de tous les abonnés
func (ps *BusServer) PublishAndWait(topic string, data any, timeout time.Duration) ([]*Message, error) {
	msgID := atomic.AddUint64(&ps.nextMsgID, 1)

	// Compter les abonnés (WebSocket + internes)
	expectedAcks := 0

	ps.topicsMu.RLock()
	if subscribers, ok := ps.topics[topic]; ok {
		expectedAcks += len(subscribers)
	}
	ps.topicsMu.RUnlock()

	ps.internalTopicsMu.RLock()
	if subscribers, ok := ps.internalTopics[topic]; ok {
		expectedAcks += len(subscribers)
	}
	ps.internalTopicsMu.RUnlock()

	if expectedAcks == 0 {
		return nil, fmt.Errorf("aucun abonné sur le topic %s", topic)
	}

	// Créer le waiter
	waiter := &AckWaiter{
		MessageID:    msgID,
		Topic:        topic,
		ExpectedAcks: expectedAcks,
		AckChan:      make(chan *Message, expectedAcks),
		TimeoutChan:  make(chan struct{}),
		Responses:    make([]*Message, 0, expectedAcks),
	}

	ps.pendingAcksMu.Lock()
	ps.pendingAcks[msgID] = waiter
	ps.pendingAcksMu.Unlock()

	// Publier le message avec needsAck=true
	message := Message{
		Topic:    topic,
		Data:     data,
		ID:       msgID,
		Time:     time.Now().Unix(),
		NeedsAck: true,
		From:     ps.serverID,
	}

	ps.publishWithMessage(&message)

	// Attendre les ACKs ou timeout
	timeoutTimer := time.NewTimer(timeout)
	defer timeoutTimer.Stop()

	responses := make([]*Message, 0, expectedAcks)

	for {
		select {
		case ack, ok := <-waiter.AckChan:
			if !ok {
				// Canal fermé, tous les ACKs reçus
				ps.pendingAcksMu.Lock()
				delete(ps.pendingAcks, msgID)
				ps.pendingAcksMu.Unlock()
				return waiter.Responses, nil
			}
			responses = append(responses, ack)

		case <-timeoutTimer.C:
			ps.pendingAcksMu.Lock()
			delete(ps.pendingAcks, msgID)
			ps.pendingAcksMu.Unlock()
			return responses, fmt.Errorf("timeout: reçu %d/%d ACKs", len(responses), expectedAcks)
		}
	}
}

// publishWithMessage publie un message déjà construit
func (ps *BusServer) publishWithMessage(message *Message) {
	// 1. Publier aux abonnés internes
	ps.internalTopicsMu.RLock()
	if subscribers, ok := ps.internalTopics[message.Topic]; ok {
		for subID, subscriber := range subscribers {
			go func(sub *InternalSubscriber, id uint64) {
				// Créer la Subscription avec la bonne fonction Unsubscribe
				subscription := &Subscription{
					ID:    id,
					Topic: message.Topic,
					Unsubscribe: func() {
						ps.unsubscribeInternalOptimized(id, message.Topic, sub)
					},
				}
				sub.Callback(message.Data, subscription)

				// Si ACK requis, envoyer automatiquement
				if message.NeedsAck {
					ps.sendInternalAck(sub, message)
				}
			}(subscriber, subID)
		}
	}
	ps.internalTopicsMu.RUnlock()

	// 2. Publier aux clients WebSocket
	messageBytes, err := jsonencdec.DefaultMarshal(message)
	if err != nil {
		log.Printf("Erreur sérialisation message: %v", err)
		return
	}

	ps.topicsMu.RLock()
	if subscribers, ok := ps.topics[message.Topic]; ok {
		for _, client := range subscribers {
			if atomic.LoadInt32(&client.IsActive) == 1 {
				select {
				case client.SendCh <- messageBytes:
				default:
					log.Printf("Client %d lent, déconnexion", client.ID)
					go ps.disconnectClient(client)
				}
			}
		}
	}
	ps.topicsMu.RUnlock()

	atomic.AddInt64(&ps.stats.Messages, 1)
}

// sendInternalAck envoie un ACK depuis un abonné interne
func (ps *BusServer) sendInternalAck(subscriber *InternalSubscriber, originalMsg *Message) {
	ackMsg := &Message{
		Topic:  originalMsg.Topic,
		Data:   map[string]any{"status": "received", "subscriber": subscriber.ID},
		ID:     atomic.AddUint64(&ps.nextMsgID, 1),
		Time:   time.Now().Unix(),
		IsAck:  true,
		AckFor: originalMsg.ID,
		From:   fmt.Sprintf("internal-%d", subscriber.ID),
	}

	ps.handleAck(ackMsg)
}

// PublishToServer publie un message vers un serveur distant
func (ps *BusServer) PublishToServer(serverAddr string, secure bool, topic string, data any) error {
	// Construire l'URL WebSocket
	protocol := "ws"
	if secure {
		protocol = "wss"
	}
	url := fmt.Sprintf("%s://%s/ws", protocol, serverAddr)

	// Créer un client temporaire pour cette publication
	client := NewGoClient(url,
		WithAutoReconnect(false),  // Pas de reconnexion automatique pour les publications ponctuelles
		WithClientID(ps.serverID), // Utiliser l'ID du serveur comme ID client
		WithOnError(func(err error) {
			log.Printf("Erreur publication vers %s: %v", serverAddr, err)
		}),
	)

	// Se connecter
	if err := client.Connect(); err != nil {
		return fmt.Errorf("impossible de se connecter à %s: %v", serverAddr, err)
	}
	defer client.Close()

	// Attendre que la connexion soit établie
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()

	for !client.IsConnected() {
		select {
		case <-timeout.C:
			return fmt.Errorf("timeout de connexion vers %s", serverAddr)
		case <-time.After(10 * time.Millisecond):
			// Continuer à attendre
		}
	}

	// Publier le message (l'ID du serveur sera automatiquement ajouté)
	client.Publish(topic, data)

	// Attendre un peu pour s'assurer que le message est envoyé
	time.Sleep(100 * time.Millisecond)

	log.Printf("Message publié vers serveur %s sur topic %s", serverAddr, topic)
	return nil
}

// PublishToServerAndWait publie un message vers un serveur distant et attend une réponse
func (ps *BusServer) PublishToServerAndWait(serverAddr string, secure bool, topic string, data any, timeout time.Duration) (*Message, error) {
	// Construire l'URL WebSocket
	protocol := "ws"
	if secure {
		protocol = "wss"
	}
	url := fmt.Sprintf("%s://%s/ws", protocol, serverAddr)

	// Canal pour recevoir la réponse
	responseChan := make(chan *Message, 1)
	var responseReceived bool
	var responseMutex sync.Mutex

	// Créer un client temporaire pour cette publication
	client := NewGoClient(url,
		WithAutoReconnect(false),
		WithClientID(ps.serverID), // Utiliser l'ID du serveur comme ID client
		WithOnError(func(err error) {
			log.Printf("Erreur publication vers %s: %v", serverAddr, err)
		}),
		WithOnMessage(func(msg *Message) {
			responseMutex.Lock()
			if !responseReceived && msg.Topic == topic {
				responseReceived = true
				select {
				case responseChan <- msg:
				default:
				}
			}
			responseMutex.Unlock()
		}),
	)

	// Se connecter
	if err := client.Connect(); err != nil {
		return nil, fmt.Errorf("impossible de se connecter à %s: %v", serverAddr, err)
	}
	defer client.Close()

	// Attendre que la connexion soit établie
	connectionTimeout := time.NewTimer(5 * time.Second)
	defer connectionTimeout.Stop()

	for !client.IsConnected() {
		select {
		case <-connectionTimeout.C:
			return nil, fmt.Errorf("timeout de connexion vers %s", serverAddr)
		case <-time.After(10 * time.Millisecond):
			// Continuer à attendre
		}
	}

	// S'abonner au topic pour recevoir la réponse
	unsubscribe := client.Subscribe(topic, func(data any, sub *ClientSubscription) {
		responseMutex.Lock()
		if !responseReceived {
			responseReceived = true
			// Créer un message factice pour la compatibilité
			msg := &Message{Topic: topic, Data: data}
			select {
			case responseChan <- msg:
			default:
			}
		}
		responseMutex.Unlock()
	})
	defer unsubscribe()

	// Attendre un peu pour s'assurer que l'abonnement est actif
	time.Sleep(100 * time.Millisecond)

	// Publier le message avec l'ID du serveur automatiquement
	client.Publish(topic, data)

	// Attendre la réponse ou timeout
	timeoutTimer := time.NewTimer(timeout)
	defer timeoutTimer.Stop()

	select {
	case response := <-responseChan:
		log.Printf("Réponse reçue du serveur %s sur topic %s", serverAddr, topic)
		return response, nil
	case <-timeoutTimer.C:
		return nil, fmt.Errorf("timeout: aucune réponse du serveur %s après %v", serverAddr, timeout)
	}
}

// PublishToMultipleServers publie un message vers plusieurs serveurs en parallèle
func (ps *BusServer) PublishToMultipleServers(servers []string, secure bool, topic string, data any) map[string]error {
	results := make(map[string]error)
	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, serverAddr := range servers {
		wg.Add(1)
		go func(addr string) {
			defer wg.Done()
			err := ps.PublishToServer(addr, secure, topic, data)
			mu.Lock()
			results[addr] = err
			mu.Unlock()
		}(serverAddr)
	}

	wg.Wait()
	return results
}

// PublishToMultipleServersAndWait publie vers plusieurs serveurs et attend toutes les réponses
func (ps *BusServer) PublishToMultipleServersAndWait(servers []string, secure bool, topic string, data any, timeout time.Duration) map[string]*Message {
	results := make(map[string]*Message)
	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, serverAddr := range servers {
		wg.Add(1)
		go func(addr string) {
			defer wg.Done()
			response, err := ps.PublishToServerAndWait(addr, secure, topic, data, timeout)
			mu.Lock()
			if err == nil {
				results[addr] = response
			} else {
				log.Printf("Erreur avec serveur %s: %v", addr, err)
			}
			mu.Unlock()
		}(serverAddr)
	}

	wg.Wait()
	return results
}

// OnIDMessage configure le callback pour recevoir les messages directs adressés au serveur
func (ps *BusServer) OnIDMessage(callback func(fromID string, data any, msg *Message)) {
	ps.onIDMessage = callback
}

// handleDirectMessage traite un message direct entre clients/serveurs
func (ps *BusServer) handleDirectMessage(fromID, targetID string, data any, originalMsg *Message) {
	// Si le message est adressé au serveur
	if targetID == ps.serverID {
		if ps.onIDMessage != nil {
			ps.onIDMessage(fromID, data, originalMsg)
		}
		return
	}

	// Chercher le client WebSocket cible
	ps.clientsByIDMu.RLock()
	targetClient, found := ps.clientsByID[targetID]
	ps.clientsByIDMu.RUnlock()

	if found && atomic.LoadInt32(&targetClient.IsActive) == 1 {
		// Envoyer le message direct au client WebSocket
		directMsg := Message{
			Topic: "__direct__",
			Data: map[string]any{
				"fromID": fromID,
				"data":   data,
			},
			ID:   atomic.AddUint64(&ps.nextMsgID, 1),
			Time: time.Now().Unix(),
			From: fromID,
		}

		msgBytes, err := jsonencdec.DefaultMarshal(directMsg)
		if err != nil {
			log.Printf("Erreur sérialisation message direct: %v", err)
			return
		}

		select {
		case targetClient.SendCh <- msgBytes:
		default:
			log.Printf("Client %s occupé, message direct perdu", targetID)
		}
		return
	}

	// Chercher l'abonné interne cible
	ps.clientsByIDMu.RLock()
	targetInternal, found := ps.internalsByID[targetID]
	ps.clientsByIDMu.RUnlock()

	if found {
		// Appeler directement le callback de l'abonné interne
		if targetInternal.onIDMessage != nil {
			go targetInternal.onIDMessage(fromID, data, originalMsg)
		}
	}
}

// PublishToID envoie un message direct à un client/serveur spécifique par son ID
func (ps *BusServer) PublishToID(targetID string, data any) error {
	ps.handleDirectMessage(ps.serverID, targetID, data, &Message{
		ID:   atomic.AddUint64(&ps.nextMsgID, 1),
		Time: time.Now().Unix(),
		From: ps.serverID,
	})
	return nil
}

// PublishToIDAndWait envoie un message direct et attend une réponse
func (ps *BusServer) PublishToIDAndWait(targetID string, data any, timeout time.Duration) (any, error) {
	msgID := atomic.AddUint64(&ps.nextMsgID, 1)

	// Créer un waiter temporaire pour cette réponse
	waiter := &AckWaiter{
		MessageID:    msgID,
		ExpectedAcks: 1,
		AckChan:      make(chan *Message, 1),
		Responses:    make([]*Message, 0, 1),
	}

	ps.pendingAcksMu.Lock()
	ps.pendingAcks[msgID] = waiter
	ps.pendingAcksMu.Unlock()

	// Envoyer le message avec demande de réponse
	directData := map[string]any{
		"data":       data,
		"needsReply": true,
		"replyToID":  msgID,
	}

	ps.handleDirectMessage(ps.serverID, targetID, directData, &Message{
		ID:   msgID,
		Time: time.Now().Unix(),
		From: ps.serverID,
	})

	// Attendre la réponse ou timeout
	timeoutTimer := time.NewTimer(timeout)
	defer timeoutTimer.Stop()

	select {
	case response := <-waiter.AckChan:
		ps.pendingAcksMu.Lock()
		delete(ps.pendingAcks, msgID)
		ps.pendingAcksMu.Unlock()

		if responseData, ok := response.Data.(map[string]any); ok {
			if data, ok := responseData["data"]; ok {
				return data, nil
			}
		}
		return response.Data, nil

	case <-timeoutTimer.C:
		ps.pendingAcksMu.Lock()
		delete(ps.pendingAcks, msgID)
		ps.pendingAcksMu.Unlock()
		return nil, fmt.Errorf("timeout: aucune réponse de %s après %v", targetID, timeout)
	}
}

// SubscribeWithID - API interne avec ID et callback pour messages directs - OPTIMISÉ
func (ps *BusServer) SubscribeWithID(topic string, callback func(any, *Subscription), onIDMessage func(string, any, *Message)) (string, func()) {
	subID := atomic.AddUint64(&ps.nextInternalID, 1)
	clientID := fmt.Sprintf("internal-%d", subID)

	// Utiliser le pool d'objets
	subscriber := &InternalSubscriber{
		ID:          subID,
		ClientID:    clientID,
		Callback:    callback,
		onIDMessage: onIDMessage,
		Topics:      make(map[string]bool, 4),
	}

	subscriber.Topics[topic] = true

	// Stocker l'abonné
	ps.internalSubsMu.Lock()
	ps.internalSubs[subID] = subscriber
	ps.internalSubsMu.Unlock()

	// Enregistrer dans le mapping ID pour messaging direct
	ps.clientsByIDMu.Lock()
	ps.internalsByID[clientID] = subscriber
	ps.clientsByIDMu.Unlock()

	// Ajouter au topic avec optimisation
	ps.internalTopicsMu.Lock()
	if ps.internalTopics[topic] == nil {
		ps.internalTopics[topic] = make(map[uint64]*InternalSubscriber, 8) // Pré-allouer
	}
	ps.internalTopics[topic][subID] = subscriber
	ps.internalTopicsMu.Unlock()

	// Retourner l'ID et la fonction de désabonnement optimisée
	return clientID, func() {
		ps.unsubscribeInternalOptimized(subID, topic, subscriber)
	}
}

func init() {
	// Optimisations runtime
	runtime.GOMAXPROCS(runtime.NumCPU())

	// Configuration des buffers WebSocket pour de meilleures performances
	ws.DefaultUpgraderKSMUX.ReadBufferSize = 4096  // 4KB au lieu de 1KB
	ws.DefaultUpgraderKSMUX.WriteBufferSize = 4096 // 4KB au lieu de 1KB
	ws.DefaultUpgraderKSMUX.EnableCompression = true
	ws.DefaultUpgraderKSMUX.HandshakeTimeout = 10 * time.Second
}
