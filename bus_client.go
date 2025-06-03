package ksmux

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kamalshkeir/ksmux/jsonencdec"
	"github.com/kamalshkeir/ksmux/ws"
)

// GoClient représente un client Go pour le pubsub
type GoClient struct {
	url                  string
	clientID             string // ID unique du client
	conn                 *ws.Conn
	isConnected          int32
	subscriptions        sync.Map // map[string]func(interface{}, *Subscription)
	messageQueue         []Message
	queueMutex           sync.RWMutex
	reconnectInterval    time.Duration
	maxReconnectAttempts int
	reconnectAttempts    int32
	autoReconnect        bool
	writeMutex           sync.Mutex // Nouveau: protection contre les écritures concurrentes

	// Callbacks
	OnConnect    func()
	OnDisconnect func()
	OnError      func(error)
	OnMessage    func(*Message)
	OnIDMessage  func(string, interface{}, *Message)

	// Channels internes
	sendCh  chan []byte
	closeCh chan struct{}
	ctx     context.Context
	cancel  context.CancelFunc
}

// Subscription représente un abonnement côté client
type ClientSubscription struct {
	Topic       string
	Unsubscribe func()
}

// NewGoClient crée un nouveau client Go
func NewGoClient(serverURL string, options ...func(*GoClient)) *GoClient {
	ctx, cancel := context.WithCancel(context.Background())

	client := &GoClient{
		url:                  serverURL,
		clientID:             fmt.Sprintf("client-%d", time.Now().UnixNano()), // ID auto-généré
		reconnectInterval:    3 * time.Second,
		maxReconnectAttempts: 10,
		autoReconnect:        true,
		sendCh:               make(chan []byte, 1024),
		closeCh:              make(chan struct{}),
		ctx:                  ctx,
		cancel:               cancel,
		OnConnect:            func() {},
		OnDisconnect:         func() {},
		OnError:              func(err error) { log.Printf("Client error: %v", err) },
		OnMessage:            func(msg *Message) {},
	}

	// Appliquer les options
	for _, option := range options {
		option(client)
	}

	return client
}

// Options pour le client
func WithAutoReconnect(enabled bool) func(*GoClient) {
	return func(c *GoClient) {
		c.autoReconnect = enabled
	}
}

func WithReconnectInterval(interval time.Duration) func(*GoClient) {
	return func(c *GoClient) {
		c.reconnectInterval = interval
	}
}

func WithMaxReconnectAttempts(max int) func(*GoClient) {
	return func(c *GoClient) {
		c.maxReconnectAttempts = max
	}
}

func WithOnConnect(callback func()) func(*GoClient) {
	return func(c *GoClient) {
		c.OnConnect = callback
	}
}

func WithOnDisconnect(callback func()) func(*GoClient) {
	return func(c *GoClient) {
		c.OnDisconnect = callback
	}
}

func WithOnError(callback func(error)) func(*GoClient) {
	return func(c *GoClient) {
		c.OnError = callback
	}
}

// Ajouter l'option WithOnMessage
func WithOnMessage(callback func(*Message)) func(*GoClient) {
	return func(c *GoClient) {
		c.OnMessage = callback
	}
}

// Ajouter l'option WithClientID
func WithClientID(clientID string) func(*GoClient) {
	return func(c *GoClient) {
		c.clientID = clientID
	}
}

// Ajouter l'option WithOnIDMessage
func WithOnIDMessage(callback func(string, interface{}, *Message)) func(*GoClient) {
	return func(c *GoClient) {
		c.OnIDMessage = callback
	}
}

// Connect établit la connexion WebSocket
func (c *GoClient) Connect() error {
	u, err := url.Parse(c.url)
	if err != nil {
		return err
	}

	dialer := ws.Dialer{
		HandshakeTimeout:  10 * time.Second,
		Subprotocols:      []string{"pubsub"},
		EnableCompression: true,
	}

	conn, _, err := dialer.Dial(u.String(), nil)
	if err != nil {
		return err
	}

	c.conn = conn
	atomic.StoreInt32(&c.isConnected, 1)
	atomic.StoreInt32(&c.reconnectAttempts, 0)

	// Démarrer les goroutines de gestion
	go c.readPump()
	go c.writePump()

	// Renvoyer les abonnements existants
	c.subscriptions.Range(func(key, value interface{}) bool {
		topic := key.(string)
		c.sendMessage(Message{
			Topic: "subscribe",
			Data:  topic,
		})
		return true
	})

	// Vider la queue de messages
	c.flushMessageQueue()

	c.OnConnect()
	log.Printf("Client connected to %s", c.url)

	return nil
}

// readPump gère la lecture des messages
func (c *GoClient) readPump() {
	defer func() {
		c.disconnect()
	}()

	c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
			_, messageBytes, err := c.conn.ReadMessage()
			if err != nil {
				if ws.IsUnexpectedCloseError(err, ws.CloseGoingAway, ws.CloseAbnormalClosure) {
					c.OnError(err)
				}
				return
			}

			var msg Message
			if err := jsonencdec.DefaultUnmarshal(messageBytes, &msg); err != nil {
				c.OnError(err)
				continue
			}

			c.handleMessage(&msg)
		}
	}
}

// writePump gère l'envoi des messages
func (c *GoClient) writePump() {
	ticker := time.NewTicker(54 * time.Second)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.sendCh:
			if !ok {
				c.writeMutex.Lock()
				c.conn.WriteMessage(ws.CloseMessage, []byte{})
				c.writeMutex.Unlock()
				return
			}

			c.writeMutex.Lock()
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			err := c.conn.WriteMessage(ws.TextMessage, message)
			c.writeMutex.Unlock()

			if err != nil {
				c.OnError(err)
				return
			}

		case <-ticker.C:
			c.writeMutex.Lock()
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			err := c.conn.WriteMessage(ws.PingMessage, nil)
			c.writeMutex.Unlock()

			if err != nil {
				return
			}

		case <-c.ctx.Done():
			return
		}
	}
}

// handleMessage traite un message reçu
func (c *GoClient) handleMessage(msg *Message) {
	// Gérer les messages directs
	if msg.Topic == "__direct__" {
		if directData, ok := msg.Data.(map[string]interface{}); ok {
			if fromID, ok := directData["fromID"].(string); ok {
				if data, ok := directData["data"]; ok {
					if c.OnIDMessage != nil {
						c.OnIDMessage(fromID, data, msg)
					}
					return
				}
			}
		}
	}

	// Callback global
	c.OnMessage(msg)

	// Callback spécifique au topic
	if callback, ok := c.subscriptions.Load(msg.Topic); ok {
		if cb, ok := callback.(func(interface{}, *Message)); ok {
			cb(msg.Data, msg)
		}
	}
}

// sendMessage envoie un message
func (c *GoClient) sendMessage(msg Message) {
	if atomic.LoadInt32(&c.isConnected) == 0 {
		c.queueMutex.Lock()
		c.messageQueue = append(c.messageQueue, msg)
		c.queueMutex.Unlock()
		return
	}

	data, err := jsonencdec.DefaultMarshal(msg)
	if err != nil {
		c.OnError(err)
		return
	}

	select {
	case c.sendCh <- data:
	case <-c.ctx.Done():
		return
	default:
		// Canal plein, on met en queue au lieu de bloquer
		c.queueMutex.Lock()
		c.messageQueue = append(c.messageQueue, msg)
		c.queueMutex.Unlock()
	}
}

// flushMessageQueue vide la queue des messages
func (c *GoClient) flushMessageQueue() {
	c.queueMutex.Lock()
	queue := c.messageQueue
	c.messageQueue = nil
	c.queueMutex.Unlock()

	for _, msg := range queue {
		// Vérifier si le contexte n'est pas annulé avant d'envoyer
		select {
		case <-c.ctx.Done():
			return
		default:
			c.sendMessage(msg)
		}
	}
}

// Subscribe s'abonne à un topic avec la même API que kactor
func (c *GoClient) Subscribe(topic string, callback func(interface{}, *ClientSubscription)) func() {
	// Créer la fonction de désabonnement
	unsubscribeFunc := func() {
		c.Unsubscribe(topic)
	}

	// Créer la subscription
	subscription := &ClientSubscription{
		Topic:       topic,
		Unsubscribe: unsubscribeFunc,
	}

	// Wrapper pour adapter l'ancienne signature à la nouvelle
	wrappedCallback := func(data interface{}, msg *Message) {
		callback(data, subscription)
	}

	c.subscriptions.Store(topic, wrappedCallback)

	c.sendMessage(Message{
		Topic: "subscribe",
		Data:  topic,
	})

	// Retourner une fonction de désabonnement
	return unsubscribeFunc
}

// SubscribeWithID s'abonne à un topic avec callback pour messages directs
func (c *GoClient) SubscribeWithID(topic string, callback func(interface{}, *ClientSubscription), onIDMessage func(string, interface{}, *Message)) func() {
	// Créer la fonction de désabonnement
	unsubscribeFunc := func() {
		c.Unsubscribe(topic)
	}

	// Créer la subscription
	subscription := &ClientSubscription{
		Topic:       topic,
		Unsubscribe: unsubscribeFunc,
	}

	// Wrapper pour adapter l'ancienne signature à la nouvelle
	wrappedCallback := func(data interface{}, msg *Message) {
		callback(data, subscription)
	}

	// Stocker le callback principal
	c.subscriptions.Store(topic, wrappedCallback)

	// Configurer le callback pour messages directs si fourni
	if onIDMessage != nil {
		c.OnIDMessage = onIDMessage
	}

	c.sendMessage(Message{
		Topic: "subscribe",
		Data:  topic,
	})

	// Retourner une fonction de désabonnement
	return unsubscribeFunc
}

// Unsubscribe se désabonne d'un topic
func (c *GoClient) Unsubscribe(topic string) {
	c.subscriptions.Delete(topic)

	c.sendMessage(Message{
		Topic: "unsubscribe",
		Data:  topic,
	})
}

// Publish publie un message (comme kactor)
func (c *GoClient) Publish(topic string, data interface{}) {
	c.sendMessage(Message{
		Topic: topic,
		Data:  data,
		Time:  time.Now().Unix(),
		From:  c.clientID,
	})
}

// PublishBatch publie plusieurs messages en lot
func (c *GoClient) PublishBatch(messages []Message) {
	for _, msg := range messages {
		if msg.Time == 0 {
			msg.Time = time.Now().Unix()
		}
		c.sendMessage(msg)
	}
}

// PublishToID publie un message directement à un client/serveur par ID
func (c *GoClient) PublishToID(targetID string, data interface{}) {
	c.sendMessage(Message{
		Topic: "__direct__",
		Data: map[string]interface{}{
			"targetID": targetID,
			"data":     data,
		},
		Time: time.Now().Unix(),
		From: c.clientID,
	})
}

// PublishToIDAndWait publie un message directement à un client et attend une réponse
func (c *GoClient) PublishToIDAndWait(targetID string, data interface{}, timeout time.Duration) (interface{}, error) {
	msgID := time.Now().UnixNano() // ID unique pour cette requête
	responseChan := make(chan interface{}, 1)

	// Envoyer le message avec demande de réponse
	c.sendMessage(Message{
		Topic: "__direct__",
		Data: map[string]interface{}{
			"targetID":   targetID,
			"data":       data,
			"needsReply": true,
			"replyToID":  msgID,
		},
		Time: time.Now().Unix(),
		From: c.clientID,
	})

	// Attendre la réponse ou timeout
	timeoutTimer := time.NewTimer(timeout)
	defer timeoutTimer.Stop()

	select {
	case response := <-responseChan:
		return response, nil
	case <-timeoutTimer.C:
		return nil, fmt.Errorf("timeout: aucune réponse de %s après %v", targetID, timeout)
	}
}

// PublishAndWait publie un message et attend une réponse
func (c *GoClient) PublishAndWait(topic string, data interface{}, timeout time.Duration) (*Message, error) {
	msgID := uint64(time.Now().UnixNano())
	responseChan := make(chan *Message, 1)

	// Créer un callback temporaire pour cette réponse
	tempCallback := func(responseData interface{}, sub *ClientSubscription) {
		msg := &Message{
			Topic: topic,
			Data:  responseData,
			ID:    msgID,
			Time:  time.Now().Unix(),
		}
		select {
		case responseChan <- msg:
		default:
		}
	}

	// S'abonner temporairement au topic
	unsubscribe := c.Subscribe(topic, tempCallback)
	defer unsubscribe()

	// Attendre un peu pour s'assurer que l'abonnement est actif
	time.Sleep(50 * time.Millisecond)

	// Publier le message
	c.sendMessage(Message{
		Topic: topic,
		Data:  data,
		ID:    msgID,
		Time:  time.Now().Unix(),
		From:  c.clientID,
	})

	// Attendre la réponse ou timeout
	timeoutTimer := time.NewTimer(timeout)
	defer timeoutTimer.Stop()

	select {
	case response := <-responseChan:
		return response, nil
	case <-timeoutTimer.C:
		return nil, fmt.Errorf("timeout: aucune réponse après %v", timeout)
	}
}

// PublishToServer publie un message vers un serveur distant
func (c *GoClient) PublishToServer(serverAddr string, secure bool, topic string, data interface{}) error {
	// Construire l'URL WebSocket
	protocol := "ws"
	if secure {
		protocol = "wss"
	}
	url := fmt.Sprintf("%s://%s/ws", protocol, serverAddr)

	// Créer un client temporaire pour cette publication
	tempClient := NewGoClient(url,
		WithAutoReconnect(false),
		WithClientID(c.clientID+"-temp"),
		WithOnError(func(err error) {
			log.Printf("Erreur publication vers %s: %v", serverAddr, err)
		}),
	)

	// Se connecter
	if err := tempClient.Connect(); err != nil {
		return fmt.Errorf("impossible de se connecter à %s: %v", serverAddr, err)
	}
	defer tempClient.Close()

	// Attendre que la connexion soit établie
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()

	for !tempClient.IsConnected() {
		select {
		case <-timeout.C:
			return fmt.Errorf("timeout de connexion vers %s", serverAddr)
		case <-time.After(10 * time.Millisecond):
			// Continuer à attendre
		}
	}

	// Publier le message
	tempClient.Publish(topic, data)

	// Attendre un peu pour s'assurer que le message est envoyé
	time.Sleep(100 * time.Millisecond)

	log.Printf("Message publié vers serveur %s sur topic %s", serverAddr, topic)
	return nil
}

// PublishToServerAndWait publie un message vers un serveur distant et attend une réponse
func (c *GoClient) PublishToServerAndWait(serverAddr string, secure bool, topic string, data interface{}, timeout time.Duration) (*Message, error) {
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
	tempClient := NewGoClient(url,
		WithAutoReconnect(false),
		WithClientID(c.clientID+"-temp"),
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
	if err := tempClient.Connect(); err != nil {
		return nil, fmt.Errorf("impossible de se connecter à %s: %v", serverAddr, err)
	}
	defer tempClient.Close()

	// Attendre que la connexion soit établie
	connectionTimeout := time.NewTimer(5 * time.Second)
	defer connectionTimeout.Stop()

	for !tempClient.IsConnected() {
		select {
		case <-connectionTimeout.C:
			return nil, fmt.Errorf("timeout de connexion vers %s", serverAddr)
		case <-time.After(10 * time.Millisecond):
			// Continuer à attendre
		}
	}

	// S'abonner au topic pour recevoir la réponse
	unsubscribe := tempClient.Subscribe(topic, func(responseData interface{}, sub *ClientSubscription) {
		responseMutex.Lock()
		if !responseReceived {
			responseReceived = true
			msg := &Message{Topic: topic, Data: responseData}
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

	// Publier le message
	tempClient.Publish(topic, data)

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
func (c *GoClient) PublishToMultipleServers(servers []string, secure bool, topic string, data interface{}) map[string]error {
	results := make(map[string]error)
	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, serverAddr := range servers {
		wg.Add(1)
		go func(addr string) {
			defer wg.Done()
			err := c.PublishToServer(addr, secure, topic, data)
			mu.Lock()
			results[addr] = err
			mu.Unlock()
		}(serverAddr)
	}

	wg.Wait()
	return results
}

// PublishToMultipleServersAndWait publie vers plusieurs serveurs et attend toutes les réponses
func (c *GoClient) PublishToMultipleServersAndWait(servers []string, secure bool, topic string, data interface{}, timeout time.Duration) map[string]*Message {
	results := make(map[string]*Message)
	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, serverAddr := range servers {
		wg.Add(1)
		go func(addr string) {
			defer wg.Done()
			response, err := c.PublishToServerAndWait(addr, secure, topic, data, timeout)
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

// disconnect ferme la connexion
func (c *GoClient) disconnect() {
	if atomic.CompareAndSwapInt32(&c.isConnected, 1, 0) {
		// Fermer les canaux de manière sécurisée
		select {
		case <-c.closeCh:
			// Déjà fermé
		default:
			close(c.closeCh)
		}

		// Ne pas fermer sendCh ici car il peut être recréé
		c.OnDisconnect()

		// Tentative de reconnexion automatique
		if c.autoReconnect && atomic.LoadInt32(&c.reconnectAttempts) < int32(c.maxReconnectAttempts) {
			go c.attemptReconnect()
		}
	}
}

// attemptReconnect tente de se reconnecter
func (c *GoClient) attemptReconnect() {
	attempts := atomic.AddInt32(&c.reconnectAttempts, 1)

	if int(attempts) > c.maxReconnectAttempts {
		log.Printf("Max reconnection attempts reached")
		return
	}

	log.Printf("Attempting to reconnect... (%d/%d)", attempts, c.maxReconnectAttempts)

	time.Sleep(c.reconnectInterval)

	// Vérifier si le contexte n'est pas annulé
	select {
	case <-c.ctx.Done():
		return
	default:
	}

	// Recréer les channels seulement si nécessaire
	select {
	case <-c.sendCh:
		// Canal fermé, le recréer
		c.sendCh = make(chan []byte, 1024)
	default:
		// Canal encore ouvert, le vider
		for len(c.sendCh) > 0 {
			<-c.sendCh
		}
	}

	c.closeCh = make(chan struct{})

	if err := c.Connect(); err != nil {
		c.OnError(err)
		// Nouvelle tentative
		go c.attemptReconnect()
	}
}

// Close ferme la connexion proprement
func (c *GoClient) Close() {
	c.autoReconnect = false
	c.cancel()

	if c.conn != nil {
		c.conn.Close()
	}
}

// IsConnected retourne l'état de la connexion
func (c *GoClient) IsConnected() bool {
	return atomic.LoadInt32(&c.isConnected) == 1
}

// GetClientID retourne l'ID unique du client
func (c *GoClient) GetClientID() string {
	return c.clientID
}

// GetStats retourne les statistiques du client
func (c *GoClient) GetStats() map[string]interface{} {
	subscriptionCount := 0
	c.subscriptions.Range(func(key, value interface{}) bool {
		subscriptionCount++
		return true
	})

	c.queueMutex.RLock()
	queueSize := len(c.messageQueue)
	c.queueMutex.RUnlock()

	return map[string]interface{}{
		"connected":          c.IsConnected(),
		"reconnect_attempts": atomic.LoadInt32(&c.reconnectAttempts),
		"subscriptions":      subscriptionCount,
		"queued_messages":    queueSize,
		"url":                c.url,
	}
}
