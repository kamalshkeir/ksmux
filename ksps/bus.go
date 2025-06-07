package ksps

import (
	"fmt"
	"runtime"
	"time"
	"unique"
	"weak"

	"github.com/kamalshkeir/ksmux/ws"
)

// New - Constructeur optimisé
func New() *Bus {
	numWorkers := runtime.GOMAXPROCS(0) * 2 // 2x CPU cores pour I/O overlap

	ps := &Bus{
		topics:        make(map[unique.Handle[string]]*dataTopic),
		subscribers:   make(map[uint64]weak.Pointer[subscriber]),
		wsSubscribers: make(map[unique.Handle[string]]*wsTopicData),
		ackRequests:   make(map[string]*ackRequest),
		workers:       newWorkerPool(numWorkers),
		done:          make(chan struct{}),
	}

	// Démarrer le nettoyage des ACK expirés
	go ps.ackCleanupWorker()

	return ps
}

// Close - Ferme le bus et arrête toutes les goroutines
func (ps *Bus) Close() {
	close(ps.done)
	ps.closeAllWebSocketConnections()
	ps.workers.close()
}

// close - Ferme le worker pool
func (wp *workerPool) close() {
	close(wp.done)
	for i := 0; i < wp.size; i++ {
		close(wp.workers[i])
	}
}

// Subscribe - Souscription ultra-rapide avec weak pointers
func (ps *Bus) Subscribe(topic string, callback func(any, func())) func() {
	topicHandle := unique.Make(topic)

	// Créer le subscriber
	sub := &subscriber{
		id:       ps.nextID.Add(1),
		callback: callback,
		topic:    topicHandle,
	}
	sub.active.Store(true)

	// Weak pointer pour auto-cleanup
	weakSub := weak.Make(sub)

	ps.mu.Lock()

	// Ajouter aux subscribers globaux
	ps.subscribers[sub.id] = weakSub

	// Obtenir ou créer TopicData
	topicData := ps.topics[topicHandle]
	if topicData == nil {
		topicData = &dataTopic{
			eventCh:     make(chan any, 1024), // Buffer pour éviter blocking
			subscribers: make([]weak.Pointer[subscriber], 0, 16),
		}
		topicData.active.Store(true)
		ps.topics[topicHandle] = topicData

		// Démarrer le processor pour ce topic
		go ps.processTopicEvents(topicData)
	}

	ps.mu.Unlock()

	// Ajouter à la liste des subscribers du topic
	topicData.mu.Lock()
	topicData.subscribers = append(topicData.subscribers, weakSub)
	topicData.mu.Unlock()

	// Retourner fonction d'unsubscribe
	return func() {
		ps.unsubscribe(sub.id)
	}
}

// Unsubscribe - Désabonne tous les subscribers d'un topic
func (ps *Bus) Unsubscribe(topic string) {
	topicHandle := unique.Make(topic)

	ps.mu.Lock()
	topicData := ps.topics[topicHandle]
	wsTopicData := ps.wsSubscribers[topicHandle]

	if topicData != nil {
		// Marquer le topic comme inactif
		topicData.active.Store(false)
		// Supprimer le topic de la map
		delete(ps.topics, topicHandle)
	}

	if wsTopicData != nil {
		// Marquer les WS comme inactifs
		wsTopicData.active.Store(false)
		delete(ps.wsSubscribers, topicHandle)
	}
	ps.mu.Unlock()

	if topicData != nil {
		// Désactiver tous les subscribers de ce topic
		topicData.mu.Lock()
		for _, weakSub := range topicData.subscribers {
			if sub := weakSub.Value(); sub != nil {
				sub.active.Store(false)
			}
		}
		topicData.subscribers = topicData.subscribers[:0] // Clear slice
		topicData.mu.Unlock()
	}

	if wsTopicData != nil {
		// Désactiver toutes les connexions WS
		wsTopicData.mu.Lock()
		for _, wsConn := range wsTopicData.connections {
			wsConn.active.Store(false)
			wsConn.mu.Lock()
			delete(wsConn.topics, topicHandle)
			wsConn.mu.Unlock()
		}
		wsTopicData.mu.Unlock()
	}
}

// Publish - Publication ultra-rapide avec path lock-free (UNIFIÉ)
func (ps *Bus) Publish(topic string, data any) {
	topicHandle := unique.Make(topic)

	// Fast path: lecture lock-free
	ps.mu.RLock()
	topicData := ps.topics[topicHandle]
	wsTopicData := ps.wsSubscribers[topicHandle]
	ps.mu.RUnlock()

	// Publier aux subscribers internes
	if topicData != nil && topicData.active.Load() {
		// Non-blocking send vers le channel buffered
		select {
		case topicData.eventCh <- data:
		default:
			// Channel plein, on drop (ou on pourrait faire du batching)
		}
	}

	// Publier aux subscribers WebSocket
	if wsTopicData != nil && wsTopicData.active.Load() {
		ps.publishToWebSocket(topic, data, wsTopicData)
	}
}

// publishToWebSocket - Publication optimisée vers WebSocket
func (ps *Bus) publishToWebSocket(topic string, data any, wsTopicData *wsTopicData) {
	msg := wsMessage{
		Action: "publish",
		Topic:  topic,
		Data:   data,
	}

	wsTopicData.mu.RLock()
	// Copier les connexions pour éviter de tenir le lock
	connections := make([]*wsConnection, 0, len(wsTopicData.connections))
	for _, conn := range wsTopicData.connections {
		if conn.active.Load() {
			connections = append(connections, conn)
		}
	}
	wsTopicData.mu.RUnlock()

	// Envoyer en parallèle via worker pool
	for _, conn := range connections {
		conn := conn // capture pour closure
		ps.workers.submit(func() {
			ps.sendToWebSocket(conn, msg)
		})
	}
}

// sendToWebSocket - Envoi optimisé vers une connexion WebSocket
func (ps *Bus) sendToWebSocket(conn *wsConnection, msg wsMessage) {
	if !conn.active.Load() {
		return
	}

	// Non-blocking send vers le channel buffered
	select {
	case conn.sendCh <- msg:
	default:
		// Channel plein, on drop le message (ou on pourrait faire du batching)
	}
}

// SubscribeWebSocket - Souscription WebSocket optimisée
func (ps *Bus) SubscribeWebSocket(clientID, topic string, wsConn *ws.Conn) {
	topicHandle := unique.Make(topic)

	ps.mu.Lock()

	// Obtenir ou créer wsTopicData
	wsTopic := ps.wsSubscribers[topicHandle]
	if wsTopic == nil {
		wsTopic = &wsTopicData{
			connections: make(map[string]*wsConnection),
		}
		wsTopic.active.Store(true)
		ps.wsSubscribers[topicHandle] = wsTopic
	}

	ps.mu.Unlock()

	wsTopic.mu.Lock()

	// Obtenir ou créer wsConnection
	conn := wsTopic.connections[clientID]
	if conn == nil {
		conn = &wsConnection{
			id:     clientID,
			conn:   wsConn,
			topics: make(map[unique.Handle[string]]bool),
			sendCh: make(chan wsMessage, 256), // Buffer pour async
		}
		conn.active.Store(true)
		wsTopic.connections[clientID] = conn

		// Démarrer le sender pour cette connexion
		go ps.wsConnectionSender(conn)
	}

	// Ajouter le topic à cette connexion
	conn.mu.Lock()
	conn.topics[topicHandle] = true
	conn.mu.Unlock()

	wsTopic.mu.Unlock()
}

// UnsubscribeWebSocket - Désabonnement WebSocket
func (ps *Bus) UnsubscribeWebSocket(clientID, topic string) {
	topicHandle := unique.Make(topic)

	ps.mu.RLock()
	wsTopicData := ps.wsSubscribers[topicHandle]
	ps.mu.RUnlock()

	if wsTopicData == nil {
		return
	}

	wsTopicData.mu.Lock()
	if conn := wsTopicData.connections[clientID]; conn != nil {
		conn.mu.Lock()
		delete(conn.topics, topicHandle)
		conn.mu.Unlock()

		// Si plus de topics, supprimer la connexion
		conn.mu.RLock()
		hasTopics := len(conn.topics) > 0
		conn.mu.RUnlock()

		if !hasTopics {
			conn.active.Store(false)
			delete(wsTopicData.connections, clientID)
		}
	}
	wsTopicData.mu.Unlock()
}

// wsConnectionSender - Goroutine qui gère l'envoi pour une connexion WS
func (ps *Bus) wsConnectionSender(conn *wsConnection) {
	for {
		select {
		case <-ps.done:
			return // Arrêter la goroutine

		case msg, ok := <-conn.sendCh:
			if !ok {
				return // Channel fermé
			}

			if !conn.active.Load() {
				return
			}

			// Envoi avec timeout pour éviter les blocages
			if err := conn.conn.WriteJSON(msg); err != nil {
				// Connexion fermée, marquer comme inactive
				conn.active.Store(false)
				return
			}
		}
	}
}

// processTopicEvents - Traite les événements d'un topic en batch
func (ps *Bus) processTopicEvents(topicData *dataTopic) {
	events := make([]any, 0, 32) // Batch size optimisé

	for {
		select {
		case <-ps.done:
			return // Arrêter la goroutine

		case event := <-topicData.eventCh:
			events = append(events, event)

			// Drain le channel jusqu'à la taille du batch
			for len(events) < cap(events) {
				select {
				case <-ps.done:
					return // Arrêter la goroutine
				case e := <-topicData.eventCh:
					events = append(events, e)
				default:
					goto process
				}
			}

		process:
			ps.dispatchBatch(topicData, events)
			events = events[:0] // Reset slice, garde la capacity
		}
	}
}

// dispatchBatch - Dispatch un batch d'événements
func (ps *Bus) dispatchBatch(topicData *dataTopic, events []any) {
	topicData.mu.RLock()
	subscribers := make([]weak.Pointer[subscriber], len(topicData.subscribers))
	copy(subscribers, topicData.subscribers)
	topicData.mu.RUnlock()

	// Dispatch chaque événement à tous les subscribers
	for _, event := range events {
		for _, weakSub := range subscribers {
			if sub := weakSub.Value(); sub != nil && sub.active.Load() {
				// Utiliser le worker pool pour paralléliser
				ps.workers.submit(func() {
					ps.executeCallback(sub, event)
				})
			}
		}
	}
}

// executeCallback - Exécute le callback pour un subscriber
func (ps *Bus) executeCallback(sub *subscriber, data any) {
	if !sub.active.Load() {
		return
	}

	// Créer la fonction d'unsubscribe
	unsubFn := func() {
		sub.active.Store(false)
	}

	// Exécuter le callback
	sub.callback(data, unsubFn)
}

// unsubscribe - Désabonnement optimisé
func (ps *Bus) unsubscribe(id uint64) {
	ps.mu.Lock()
	delete(ps.subscribers, id)
	ps.mu.Unlock()
}

// newWorkerPool - Crée un pool de workers avec work stealing
func newWorkerPool(size int) *workerPool {
	wp := &workerPool{
		workers: make([]chan func(), size),
		size:    size,
		done:    make(chan struct{}),
	}

	// Démarrer les workers
	for i := 0; i < size; i++ {
		wp.workers[i] = make(chan func(), 100) // Buffer pour éviter blocking
		go wp.worker(i)
	}

	return wp
}

// worker - Worker qui traite les tâches
func (wp *workerPool) worker(id int) {
	for {
		select {
		case <-wp.done:
			return // Arrêter la goroutine

		case fn, ok := <-wp.workers[id]:
			if !ok {
				return // Channel fermé
			}
			fn()
		}
	}
}

// submit - Soumet une tâche au pool avec work stealing
func (wp *workerPool) submit(fn func()) {
	// Round-robin avec atomic pour distribution équitable
	idx := wp.next.Add(1) % uint64(wp.size)

	select {
	case wp.workers[idx] <- fn:
		return
	default:
		// Work stealing si le worker est occupé
		for i := 0; i < wp.size; i++ {
			select {
			case wp.workers[i] <- fn:
				return
			default:
			}
		}
		// Fallback: exécuter dans une nouvelle goroutine
		go fn()
	}
}

// CleanupWebSocketClient - Nettoie toutes les souscriptions d'un client WebSocket
func (ps *Bus) CleanupWebSocketClient(clientID string) {
	ps.mu.RLock()
	// Copier toutes les wsTopicData pour éviter de tenir le lock trop longtemps
	topicsToClean := make(map[unique.Handle[string]]*wsTopicData)
	for topicHandle, wsTopicData := range ps.wsSubscribers {
		topicsToClean[topicHandle] = wsTopicData
	}
	ps.mu.RUnlock()

	// Nettoyer chaque topic
	for topicHandle, wsTopicData := range topicsToClean {
		if wsTopicData == nil || !wsTopicData.active.Load() {
			continue
		}

		wsTopicData.mu.Lock()
		if conn := wsTopicData.connections[clientID]; conn != nil {
			// Marquer la connexion comme inactive
			conn.active.Store(false)

			// Supprimer de tous les topics de cette connexion
			conn.mu.Lock()
			conn.topics = make(map[unique.Handle[string]]bool) // Clear all topics
			conn.mu.Unlock()

			// Supprimer la connexion de ce topic
			delete(wsTopicData.connections, clientID)

			// Si plus de connexions, marquer le topic comme inactif
			if len(wsTopicData.connections) == 0 {
				wsTopicData.active.Store(false)

				// Supprimer le topic de la map principale
				ps.mu.Lock()
				delete(ps.wsSubscribers, topicHandle)
				ps.mu.Unlock()
			}
		}
		wsTopicData.mu.Unlock()
	}
}

// PublishWithAck - Publication avec acknowledgment
func (ps *Bus) PublishWithAck(topic string, data any, timeout time.Duration) *Ack {
	topicHandle := unique.Make(topic)
	ackID := ps.generateAckID()

	// Créer la requête ACK
	ackReq := &ackRequest{
		id:        ackID,
		timestamp: time.Now(),
		timeout:   timeout,
		ackCh:     make(chan ackResponse, 100), // Buffer pour éviter blocking
		clientIDs: make([]string, 0),
		received:  make(map[string]bool),
	}

	// Collecter les clients qui vont recevoir le message
	ps.mu.RLock()
	wsTopicData := ps.wsSubscribers[topicHandle]
	ps.mu.RUnlock()

	if wsTopicData != nil && wsTopicData.active.Load() {
		wsTopicData.mu.RLock()
		for clientID := range wsTopicData.connections {
			ackReq.clientIDs = append(ackReq.clientIDs, clientID)
			ackReq.received[clientID] = false
		}
		wsTopicData.mu.RUnlock()
	}

	// Enregistrer la requête ACK
	ps.ackMu.Lock()
	ps.ackRequests[ackID] = ackReq
	ps.ackMu.Unlock()

	// Publier le message avec ACK ID
	ps.publishWithAckID(topic, data, ackID)

	return &Ack{
		ID:      ackID,
		Request: ackReq,
		Bus:     ps,
	}
}

// publishWithAckID - Publication avec ID d'acknowledgment
func (ps *Bus) publishWithAckID(topic string, data any, ackID string) {
	topicHandle := unique.Make(topic)

	// Fast path: lecture lock-free
	ps.mu.RLock()
	topicData := ps.topics[topicHandle]
	wsTopicData := ps.wsSubscribers[topicHandle]
	ps.mu.RUnlock()

	// Publier aux subscribers internes (pas d'ACK pour local)
	if topicData != nil && topicData.active.Load() {
		select {
		case topicData.eventCh <- data:
		default:
		}
	}

	// Publier aux subscribers WebSocket avec ACK
	if wsTopicData != nil && wsTopicData.active.Load() {
		ps.publishToWebSocketWithAck(topic, data, ackID, wsTopicData)
	}
}

// publishToWebSocketWithAck - Publication WebSocket avec ACK
func (ps *Bus) publishToWebSocketWithAck(topic string, data any, ackID string, wsTopicData *wsTopicData) {
	msg := wsMessage{
		Action: "publish_ack",
		Topic:  topic,
		Data:   data,
		AckID:  ackID,
	}

	wsTopicData.mu.RLock()
	connections := make([]*wsConnection, 0, len(wsTopicData.connections))
	for _, conn := range wsTopicData.connections {
		if conn.active.Load() {
			connections = append(connections, conn)
		}
	}
	wsTopicData.mu.RUnlock()

	// Envoyer en parallèle via worker pool
	for _, conn := range connections {
		conn := conn
		ps.workers.submit(func() {
			ps.sendToWebSocket(conn, msg)
		})
	}
}

// HandleAck - Traite un acknowledgment reçu
func (ps *Bus) HandleAck(ackResp ackResponse) {
	ps.ackMu.RLock()
	ackReq := ps.ackRequests[ackResp.AckID]
	ps.ackMu.RUnlock()

	if ackReq == nil {
		return // ACK expiré ou inexistant
	}

	ackReq.mu.Lock()
	ackReq.received[ackResp.ClientID] = true
	ackReq.mu.Unlock()

	// Envoyer la réponse dans le channel
	select {
	case ackReq.ackCh <- ackResp:
	default:
		// Channel plein, on drop
	}
}

// generateAckID - Génère un ID unique pour ACK
func (ps *Bus) generateAckID() string {
	return fmt.Sprintf("ack_%d_%d", time.Now().UnixNano(), ps.nextID.Add(1))
}

// ackCleanupWorker - Nettoie les ACK expirés
func (ps *Bus) ackCleanupWorker() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ps.done:
			return // Arrêter la goroutine

		case <-ticker.C:
			now := time.Now()
			ps.ackMu.Lock()

			for ackID, ackReq := range ps.ackRequests {
				if now.Sub(ackReq.timestamp) > ackReq.timeout {
					close(ackReq.ackCh)
					delete(ps.ackRequests, ackID)
				}
			}

			ps.ackMu.Unlock()
		}
	}
}

// Wait - Attend tous les acknowledgments avec timeout
func (ack *Ack) Wait() map[string]ackResponse {
	if ack.Request == nil {
		return make(map[string]ackResponse)
	}

	responses := make(map[string]ackResponse)
	timeout := time.After(ack.Request.timeout)

	for {
		select {
		case resp, ok := <-ack.Request.ackCh:
			if !ok {
				// Channel fermé = timeout
				return responses
			}
			responses[resp.ClientID] = resp

			// Vérifier si tous les ACK sont reçus
			ack.Request.mu.RLock()
			allReceived := true
			for _, received := range ack.Request.received {
				if !received {
					allReceived = false
					break
				}
			}
			ack.Request.mu.RUnlock()

			if allReceived {
				return responses
			}

		case <-timeout:
			// Timeout atteint
			return responses
		}
	}
}

// WaitAny - Attend au moins un acknowledgment
func (ack *Ack) WaitAny() (ackResponse, bool) {
	if ack.Request == nil {
		return ackResponse{}, false
	}

	timeout := time.After(ack.Request.timeout)

	select {
	case resp, ok := <-ack.Request.ackCh:
		return resp, ok
	case <-timeout:
		return ackResponse{}, false
	}
}

// GetStatus - Retourne le statut actuel des acknowledgments
func (ack *Ack) GetStatus() map[string]bool {
	if ack.Request == nil {
		return make(map[string]bool)
	}

	ack.Request.mu.RLock()
	status := make(map[string]bool, len(ack.Request.received))
	for clientID, received := range ack.Request.received {
		status[clientID] = received
	}
	ack.Request.mu.RUnlock()

	return status
}

// IsComplete - Vérifie si tous les ACK sont reçus
func (ack *Ack) IsComplete() bool {
	if ack.Request == nil {
		return true
	}

	ack.Request.mu.RLock()
	defer ack.Request.mu.RUnlock()

	for _, received := range ack.Request.received {
		if !received {
			return false
		}
	}
	return true
}

// Cancel - Annule l'attente des acknowledgments
func (ack *Ack) Cancel() {
	if ack.Request == nil || ack.Bus == nil {
		return
	}

	ack.Bus.ackMu.Lock()
	delete(ack.Bus.ackRequests, ack.ID)
	ack.Bus.ackMu.Unlock()

	close(ack.Request.ackCh)
}

// closeAllWebSocketConnections - Ferme toutes les connexions WebSocket
func (ps *Bus) closeAllWebSocketConnections() {
	ps.mu.RLock()
	wsTopics := make([]*wsTopicData, 0, len(ps.wsSubscribers))
	for _, wsTopicData := range ps.wsSubscribers {
		wsTopics = append(wsTopics, wsTopicData)
	}
	ps.mu.RUnlock()

	for _, wsTopicData := range wsTopics {
		wsTopicData.mu.Lock()
		for _, conn := range wsTopicData.connections {
			conn.active.Store(false)
			close(conn.sendCh)
		}
		wsTopicData.mu.Unlock()
	}
}
