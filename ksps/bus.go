package ksps

import (
	"fmt"
	"runtime"
	"sync"
	"time"
	"unique"
	"weak"

	"github.com/kamalshkeir/ksmux/ws"
)

type writePoolData struct {
	buf []byte
}

func init() {
	ws.DefaultUpgraderKSMUX = ws.Upgrader{
		EnableCompression: false, // Disable compression for lower latency (JSON is already compact)
		ReadBufferSize:    8192,  // 8KB - optimal for most messages
		WriteBufferSize:   8192,  // 8KB - matches read buffer
		HandshakeTimeout:  10 * time.Second,
		WriteBufferPool:   &sync.Pool{New: func() interface{} { return writePoolData{buf: make([]byte, 8192)} }},
	}
}

// New - Constructeur optimisé
func New() *Bus {
	numWorkers := runtime.GOMAXPROCS(0) * 2 // 2x CPU cores pour I/O overlap

	ps := &Bus{
		topics:        make(map[unique.Handle[string]]*dataTopic),
		subscribers:   make(map[uint64]weak.Pointer[subscriber]),
		wsSubscribers: make(map[unique.Handle[string]]*wsTopicData),
		wsConns:       make(map[string]*wsConnection),
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
			eventCh:     make(chan any, 1024),
			stopCh:      make(chan struct{}),
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
		sub.active.Store(false)
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
		// Supprimer d'abord de la map pour que plus rien ne soit ajouté au channel (Lock ps.mu est déjà tenu)
		delete(ps.topics, topicHandle)

		// Nettoyer le registre global des abonnés pour ce topic (Fix RAM Leak)
		topicData.mu.RLock()
		subsCount := len(topicData.subscribers)
		if subsCount > 0 {
			// On capture les IDs
			ids := make([]uint64, 0, subsCount)
			for _, ws := range topicData.subscribers {
				if s := ws.Value(); s != nil {
					ids = append(ids, s.id)
				}
			}
			topicData.mu.RUnlock()

			// On retire massivement du registre global (ps.mu est DÉJÀ tenu, donc pas de Lock() supplémentaire)
			for _, id := range ids {
				delete(ps.subscribers, id)
			}
		} else {
			topicData.mu.RUnlock()
		}

		// Signaler l'arrêt
		close(topicData.stopCh)
	}

	if wsTopicData != nil {
		// Marquer les WS comme inactifs
		wsTopicData.active.Store(false)
		delete(ps.wsSubscribers, topicHandle)
	}
	ps.mu.Unlock()

	if wsTopicData != nil {
		// Retirer ce topic de toutes les connexions associées
		wsTopicData.mu.Lock()
		for _, wsConn := range wsTopicData.connections {
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

			// Vérifier si tous les ACK sont reçus (Optimisation O(1))
			if ack.Request.remaining.Load() <= 0 {
				// Plus besoin de garder l'ID en mémoire, on libère le bus
				ack.Bus.cancelAck(ack.ID)
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
	ack.Bus.cancelAck(ack.ID)
}

// close - Ferme le worker pool
func (wp *workerPool) close() {
	close(wp.done)
	for i := 0; i < wp.size; i++ {
		close(wp.workers[i])
	}
}

// publishToWebSocket - Publication optimisée vers WebSocket
func (ps *Bus) publishToWebSocket(topic string, data any, wsTopicData *wsTopicData) {
	msg := WsMessage{
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
func (ps *Bus) sendToWebSocket(conn *wsConnection, msg WsMessage) {
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

// subscribeWebSocket - Souscription WebSocket optimisée
func (ps *Bus) subscribeWebSocket(clientID, topic string, wsConn *ws.Conn) {
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

	// Enregistrer d'abord le client dans le registre global (Fix Invisible Client)
	ps.registerWebSocket(clientID, wsConn)

	// ORDRE DE LOCK CRITIQUE : wsConnsMu AVANT wsTopic.mu
	ps.wsConnsMu.RLock()
	conn := ps.wsConns[clientID]
	ps.wsConnsMu.RUnlock()

	if conn == nil {
		return // Ne devrait pas arriver après Register
	}

	wsTopic.mu.Lock()
	wsTopic.connections[clientID] = conn
	wsTopic.mu.Unlock()

	// Enregistrer le topic dans la connexion
	conn.mu.Lock()
	conn.topics[topicHandle] = true
	conn.mu.Unlock()
}

// registerWebSocket - Enregistre une connexion et retourne l'objet wsConnection (Atomicity++)
func (ps *Bus) registerWebSocket(clientID string, wsConn *ws.Conn) *wsConnection {
	ps.wsConnsMu.Lock()
	defer ps.wsConnsMu.Unlock()

	conn := ps.wsConns[clientID]
	if conn == nil {
		conn = &wsConnection{
			id:     clientID,
			conn:   wsConn,
			topics: make(map[unique.Handle[string]]bool),
			sendCh: make(chan WsMessage, 1024),
			stopCh: make(chan struct{}),
		}
		conn.active.Store(true)
		ps.wsConns[clientID] = conn
		go ps.wsConnectionSender(conn)
	} else if conn.conn != wsConn {
		// Reconnexion éclair
		conn.mu.Lock()
		oldSocket := conn.conn
		conn.conn = wsConn
		if !conn.active.Load() {
			conn.active.Store(true)
			conn.sendCh = make(chan WsMessage, 1024)
			conn.stopCh = make(chan struct{})
			go ps.wsConnectionSender(conn)
		}
		conn.mu.Unlock()
		if oldSocket != nil {
			// Fermeture asynchrone sécurisée
			go func(s *ws.Conn) {
				defer func() { recover() }()
				if s != nil {
					_ = s.Close()
				}
			}(oldSocket)
		}
	}
	return conn
}

// unsubscribeWebSocket - Désabonnement WebSocket optimisé (O(1)) et sans deadlock
func (ps *Bus) unsubscribeWebSocket(clientID, topic string) {
	topicHandle := unique.Make(topic)

	ps.mu.RLock()
	wsTopicData := ps.wsSubscribers[topicHandle]
	ps.mu.RUnlock()

	if wsTopicData == nil {
		return
	}

	// 1. Enlever le topic de la connexion du client
	ps.wsConnsMu.RLock()
	conn := ps.wsConns[clientID]
	ps.wsConnsMu.RUnlock()

	if conn != nil {
		conn.mu.Lock()
		delete(conn.topics, topicHandle)
		conn.mu.Unlock()
	}

	// 2. Enlever le client du topic
	wsTopicData.mu.Lock()
	delete(wsTopicData.connections, clientID)
	isEmpty := len(wsTopicData.connections) == 0
	wsTopicData.mu.Unlock()

	// 3. Si le topic est vide, on le supprime (Locking order correct)
	if isEmpty {
		ps.mu.Lock()
		// Re-vérifier après avoir pris le lock
		if wsTopic, ok := ps.wsSubscribers[topicHandle]; ok {
			wsTopic.mu.RLock()
			if len(wsTopic.connections) == 0 {
				wsTopic.active.Store(false)
				delete(ps.wsSubscribers, topicHandle)
			}
			wsTopic.mu.RUnlock()
		}
		ps.mu.Unlock()
	}
}

// wsConnectionSender - Drain final avant fermeture
func (ps *Bus) wsConnectionSender(conn *wsConnection) {
	defer func() {
		// DRAINAGE FINAL : envoyer les messages restants dans le buffer
		ticker := time.NewTicker(1000 * time.Millisecond) // Max 1s pour vider le buffer
		defer ticker.Stop()
	loop:
		for {
			select {
			case msg, ok := <-conn.sendCh:
				if !ok {
					break loop
				}
				ps.internalSendMessage(conn, msg)
			case <-ticker.C:
				break loop
			default:
				break loop
			}
		}
	}()

	for {
		select {
		case <-ps.done:
			return
		case <-conn.stopCh:
			return
		case msg, ok := <-conn.sendCh:
			if !ok {
				return
			}
			ps.internalSendMessage(conn, msg)
		}
	}
}

func (ps *Bus) internalSendMessage(conn *wsConnection, msg WsMessage) {
	defer func() { recover() }() // Protection contre socket corrompue/mockée sans deadlines

	conn.mu.RLock()
	socket := conn.conn
	conn.mu.RUnlock()

	if socket == nil {
		return
	}

	socket.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err := socket.WriteJSON(msg); err != nil {
		conn.mu.RLock()
		isSameSocket := conn.conn == socket
		conn.mu.RUnlock()

		if isSameSocket {
			go ps.cleanupWebSocketClient(conn.id, socket)
		}
	}
}

// processTopicEvents - Drainage final garanti
func (ps *Bus) processTopicEvents(topicData *dataTopic) {
	events := make([]any, 0, 32)

	closeOnce := sync.Once{}
	stopProcessor := func() {
		closeOnce.Do(func() {
			// Capturer les derniers abonnés AVANT qu'ils ne soient potentiellement vidés
			topicData.mu.RLock()
			lastSubscribers := make([]weak.Pointer[subscriber], len(topicData.subscribers))
			copy(lastSubscribers, topicData.subscribers)
			topicData.mu.RUnlock()

			topicData.active.Store(false)
			// Drainage final synchrone avec les derniers abonnés connus
			for {
				select {
				case event, ok := <-topicData.eventCh:
					if !ok {
						return
					}
					ps.dispatchBatchWithSubs(topicData, []any{event}, lastSubscribers)
				default:
					return
				}
			}
		})
	}
	defer stopProcessor()

	for {
		select {
		case <-ps.done:
			if len(events) > 0 {
				ps.dispatchBatch(topicData, events)
			}
			return
		case <-topicData.stopCh:
			if len(events) > 0 {
				ps.dispatchBatch(topicData, events)
			}
			return

		case event, ok := <-topicData.eventCh:
			if !ok {
				return
			}
			events = append(events, event)

			// Drain le channel jusqu'à la taille du batch
			for len(events) < cap(events) {
				select {
				case <-ps.done:
					goto process
				case <-topicData.stopCh:
					goto process
				case e, ok := <-topicData.eventCh:
					if !ok {
						goto process
					}
					events = append(events, e)
				default:
					goto process
				}
			}

		process:
			ps.dispatchBatch(topicData, events)
			events = events[:0]
		}
	}
}

// dispatchBatch - Dispatch standard (utilise les abonnés actuels)
func (ps *Bus) dispatchBatch(topicData *dataTopic, events []any) {
	topicData.mu.RLock()
	subscribers := make([]weak.Pointer[subscriber], len(topicData.subscribers))
	copy(subscribers, topicData.subscribers)
	topicData.mu.RUnlock()

	ps.dispatchBatchWithSubs(topicData, events, subscribers)
}

// dispatchBatchWithSubs - Dispatch avec une liste d'abonnés spécifique (utilisé pour drainage)
func (ps *Bus) dispatchBatchWithSubs(topicData *dataTopic, events []any, subscribers []weak.Pointer[subscriber]) {
	if len(subscribers) == 0 {
		return
	}

	var activeCount int
	for _, event := range events {
		for _, weakSub := range subscribers {
			if sub := weakSub.Value(); sub != nil && sub.active.Load() {
				if activeCount == 0 {
					activeCount++
				}
				ps.workers.submit(func() {
					ps.executeCallback(sub, event)
				})
			}
		}
	}

	if len(subscribers) > 64 {
		realActive := 0
		for _, ws := range subscribers {
			if s := ws.Value(); s != nil && s.active.Load() {
				realActive++
			}
		}
		if realActive < len(subscribers)/2 {
			if topicData.compacting.CompareAndSwap(false, true) {
				go ps.compactSubscribers(topicData)
			}
		}
	}
}

// compactSubscribers - Supprime les weak pointers morts de la slice du topic
func (ps *Bus) compactSubscribers(topicData *dataTopic) {
	defer topicData.compacting.Store(false)

	topicData.mu.Lock()
	defer topicData.mu.Unlock()

	newSubs := make([]weak.Pointer[subscriber], 0, len(topicData.subscribers))
	for _, ws := range topicData.subscribers {
		if s := ws.Value(); s != nil && s.active.Load() {
			newSubs = append(newSubs, ws)
		}
	}
	topicData.subscribers = newSubs
}

// executeCallback - Exécute le callback pour un subscriber
func (ps *Bus) executeCallback(sub *subscriber, data any) {
	if !sub.active.Load() {
		return
	}

	// Créer la fonction d'unsubscribe (Correctif de fuite mémoire)
	unsubFn := func() {
		sub.active.Store(false)
		ps.unsubscribe(sub.id)
	}

	// Exécuter le callback
	sub.callback(data, unsubFn)
}

// unsubscribe - Désabonnement optimisé avec Auto-Cleanup de Topic
func (ps *Bus) unsubscribe(id uint64) {
	ps.mu.Lock()
	sub, ok := ps.subscribers[id]
	if !ok {
		ps.mu.Unlock()
		return
	}

	// Récupérer le topic avant de supprimer l'ID
	var topicHandle unique.Handle[string]
	if s := sub.Value(); s != nil {
		topicHandle = s.topic
	}

	delete(ps.subscribers, id)
	ps.mu.Unlock()

	// Auto-cleanup du topic si nécessaire
	if topicHandle != (unique.Handle[string]{}) {
		ps.checkTopicEmpty(topicHandle)
	}
}

// checkTopicEmpty - Vérifie et nettoie un topic s'il n'a plus d'abonnés
func (ps *Bus) checkTopicEmpty(topicHandle unique.Handle[string]) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	// 1. Vérifier les abonnés internes
	topicData := ps.topics[topicHandle]
	if topicData != nil {
		topicData.mu.RLock()
		activeCount := 0
		for _, ws := range topicData.subscribers {
			if s := ws.Value(); s != nil && s.active.Load() {
				activeCount++
				break
			}
		}
		topicData.mu.RUnlock()

		if activeCount == 0 {
			// 2. Vérifier les abonnés WebSocket
			wsTopic := ps.wsSubscribers[topicHandle]
			if wsTopic == nil || !wsTopic.active.Load() {
				// Plus personne du tout ! Autodestruction.
				delete(ps.topics, topicHandle)
				close(topicData.stopCh)
			}
		}
	}
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
			// Sécuriser l'exécution des fonctions (panics)
			func() {
				defer func() {
					if r := recover(); r != nil {
						fmt.Printf("⚠️ Worker %d recovered from panic: %v\n", id, r)
					}
				}()
				fn()
			}()
		}
	}
}

// submit - Soumet une tâche au pool avec work stealing et sécurité shutdown
func (wp *workerPool) submit(fn func()) {
	// Vérifier si le pool est en vie
	select {
	case <-wp.done:
		return // Rejeter la tâche si on ferme
	default:
	}

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
		// Fallback: exécuter dans une nouvelle goroutine uniquement si peu de goroutines sont en cours
		// Sinon on attend un petit peu ou on exécute de manière synchrone (backpressure)
		if runtime.NumGoroutine() < 10000 {
			go fn()
		} else {
			// Backpressure : exécution synchrone par le thread appelant si trop de charge
			fn()
		}
	}
}

// cleanupWebSocketClient - Nettoie proprement une connexion spécifique (Évite les race-conditions de reconnexion)
func (ps *Bus) cleanupWebSocketClient(clientID string, socket *ws.Conn) {
	ps.wsConnsMu.Lock()
	conn, exists := ps.wsConns[clientID]
	// On ne nettoie QUE si c'est bien la même socket qui a généré l'erreur
	var isSameSocket bool
	if exists {
		conn.mu.RLock()
		isSameSocket = (socket == nil || conn.conn == socket)
		conn.mu.RUnlock()
	}

	if exists && isSameSocket {
		conn.active.Store(false)
		if conn.stopCh != nil {
			select {
			case <-conn.stopCh:
			default:
				close(conn.stopCh)
			}
		}
		delete(ps.wsConns, clientID)
	} else {
		ps.wsConnsMu.Unlock()
		return
	}
	ps.wsConnsMu.Unlock()

	if !exists {
		return
	}

	// Nettoyage des références dans les topics (O(K) au lieu de O(N))
	// On ne parcourt que les topics auxquels le client était VRAIMENT abonné
	conn.mu.RLock()
	clientTopics := make([]unique.Handle[string], 0, len(conn.topics))
	for t := range conn.topics {
		clientTopics = append(clientTopics, t)
	}
	conn.mu.RUnlock()

	for _, topicHandle := range clientTopics {
		ps.mu.RLock()
		wsTopicData := ps.wsSubscribers[topicHandle]
		ps.mu.RUnlock()

		if wsTopicData != nil {
			wsTopicData.mu.Lock()
			delete(wsTopicData.connections, clientID)
			isEmpty := len(wsTopicData.connections) == 0
			wsTopicData.mu.Unlock()

			// Nettoyage du topic vide (Anti-Deadlock: on a relâché le lock topic avant de prendre ps.mu)
			if isEmpty {
				ps.mu.Lock()
				// Double-check sous lock global
				if wsTopic, ok := ps.wsSubscribers[topicHandle]; ok {
					wsTopic.mu.Lock() // Re-lock safe car on a ps.mu
					if len(wsTopic.connections) == 0 {
						wsTopic.active.Store(false)
						delete(ps.wsSubscribers, topicHandle)
					}
					wsTopic.mu.Unlock()
				}
				ps.mu.Unlock()
			}
		}
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
		ackReq.remaining.Store(int32(len(wsTopicData.connections)))
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
	msg := WsMessage{
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

// handleAck - Traite un acknowledgment reçu
func (ps *Bus) handleAck(ackResp ackResponse) {
	ps.ackMu.RLock()
	ackReq := ps.ackRequests[ackResp.AckID]
	ps.ackMu.RUnlock()

	if ackReq == nil {
		return // ACK expiré ou inexistant
	}

	ackReq.mu.Lock()
	if !ackReq.received[ackResp.ClientID] {
		ackReq.received[ackResp.ClientID] = true
		ackReq.remaining.Add(-1)
	}
	ackReq.mu.Unlock()

	// Envoyer la réponse dans le channel de manière sécurisée (contre channel fermé)
	func() {
		defer func() { recover() }()
		select {
		case ackReq.ackCh <- ackResp:
		default:
			// Channel plein, on drop
		}
	}()
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
					delete(ps.ackRequests, ackID)
					close(ackReq.ackCh)
				}
			}

			ps.ackMu.Unlock()
		}
	}
}

// cancelAck - Annule un ACK par son ID de manière sécurisée
func (ps *Bus) cancelAck(ackID string) {
	ps.ackMu.Lock()
	if ackReq, ok := ps.ackRequests[ackID]; ok {
		delete(ps.ackRequests, ackID)
		close(ackReq.ackCh)
	}
	ps.ackMu.Unlock()
}

// closeAllWebSocketConnections - Ferme toutes les connexions WebSocket et leurs goroutines
func (ps *Bus) closeAllWebSocketConnections() {
	ps.wsConnsMu.Lock()
	defer ps.wsConnsMu.Unlock()

	for clientID, conn := range ps.wsConns {
		conn.active.Store(false)
		if conn.stopCh != nil {
			select {
			case <-conn.stopCh:
			default:
				close(conn.stopCh)
			}
		}
		delete(ps.wsConns, clientID)
	}
}
