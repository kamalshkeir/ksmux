package ksps

import (
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kamalshkeir/ksmux"
	"github.com/kamalshkeir/ksmux/ws"
	"github.com/kamalshkeir/lg"
)

type Client struct {
	Id           string
	ServerAddr   string
	onDataWS     func(data WsMessage, conn *ws.Conn) error
	onId         func(data Message)
	onClose      func()
	RestartEvery time.Duration
	Conn         *ws.Conn
	Autorestart  bool
	Done         chan struct{}

	// Optimisations pour pub/sub
	subscriptions map[string]*clientSubscription // topic -> subscription
	subMu         sync.RWMutex
	messageQueue  chan WsMessage // Queue pour messages sortants
	connected     atomic.Bool

	// ACK system côté client
	ackRequests  map[string]*ClientAck // ackID -> ClientAck
	nextAckID    atomic.Uint64
	reconnecting atomic.Bool // Protection contre les tempêtes
}

type clientSubscription struct {
	topic    string
	callback func(data Message, unsub func())
	active   atomic.Bool
}

type ClientOptions struct {
	Id           string
	Address      string
	Secure       bool
	Path         string // default ksbus.ServerPath
	Autorestart  bool
	RestartEvery time.Duration
	OnDataWs     func(data WsMessage, conn *ws.Conn) error
	OnID         func(data Message)
	OnClose      func()
}

func NewClient(opts ClientOptions) (*Client, error) {
	if opts.Autorestart && opts.RestartEvery == 0 {
		opts.RestartEvery = 10 * time.Second
	}
	if opts.OnDataWs == nil {
		opts.OnDataWs = func(data WsMessage, conn *ws.Conn) error { return nil }
	}
	cl := &Client{
		Id:            opts.Id,
		Autorestart:   opts.Autorestart,
		RestartEvery:  opts.RestartEvery,
		onDataWS:      opts.OnDataWs,
		onId:          opts.OnID,
		onClose:       opts.OnClose,
		Done:          make(chan struct{}),
		subscriptions: make(map[string]*clientSubscription),
		ackRequests:   make(map[string]*ClientAck),
		messageQueue:  make(chan WsMessage, 1024),
	}
	if cl.Id == "" {
		cl.Id = ksmux.GenerateID()
	}
	err := cl.connect(opts)
	if err != nil && !cl.Autorestart {
		return nil, err
	}
	if err != nil && cl.Autorestart {
		// Pas d'erreur fatale si autorestart est activé, on laisse la boucle faire
		go cl.reconnect()
	}
	return cl, nil
}

func (client *Client) connect(opts ClientOptions) error {
	sch := "ws"
	if opts.Secure {
		sch = "wss"
	}
	// Handle Path with potential query params
	if opts.Path == "" {
		opts.Path = "/ws/bus"
	}

	rawUrl := fmt.Sprintf("%s://%s%s", sch, opts.Address, opts.Path)
	// Validate URL
	_, err := url.Parse(rawUrl)
	if err != nil {
		return err
	}
	client.ServerAddr = rawUrl
	c, resp, err := ws.DefaultDialer.Dial(rawUrl, nil)
	if err != nil {
		if err == ws.ErrBadHandshake && resp != nil {
			lg.DebugC("handshake failed with status", "status", resp.StatusCode)
			return err
		}
		if err == ws.ErrCloseSent && resp != nil {
			lg.DebugC("server connection closed with status", "status", resp.StatusCode)
			return err
		} else {
			lg.DebugC("NewClient error", "err", err)
			return err
		}
	}
	client.Conn = c
	client.connected.Store(true)

	// Créer un canal stop local pour synchroniser Handler/Sender de cette session
	stopCh := make(chan struct{})

	// Démarrer les goroutines de gestion
	go client.messageHandler(stopCh)
	go client.messageSender(stopCh)

	// Ping initial pour enregistrer le client
	client.sendMessage(WsMessage{
		Action: ping,
		From:   client.Id,
	})

	lg.Printfs("client connected to %s\n", client.ServerAddr)
	return nil
}

// messageHandler - Gère les messages entrants
func (client *Client) messageHandler(stopCh chan struct{}) {
	defer close(stopCh) // Arrête le sender si le handler meurt
	for {
		select {
		case <-client.Done:
			return
		case <-stopCh:
			return
		default:
			if !client.connected.Load() {
				return
			}

			// Get connection safely
			client.subMu.RLock()
			conn := client.Conn
			client.subMu.RUnlock()

			if conn == nil {
				return
			}

			var m WsMessage
			err := conn.ReadJSON(&m)
			if err != nil {
				lg.DebugC("WebSocket read error:", "error", err.Error())
				client.connected.Store(false)
				if client.Autorestart {
					go client.reconnect()
				} else if client.onClose != nil {
					client.onClose()
				}
				return
			}

			// Traiter le message
			client.handleMessage(m)
		}
	}
}

// messageSender - Gère l'envoi des messages en queue
func (client *Client) messageSender(stopCh chan struct{}) {
	for {
		select {
		case <-client.Done:
			return
		case <-stopCh:
			return // Session terminée

		case msg, ok := <-client.messageQueue:
			if !ok {
				return
			}

			if !client.connected.Load() {
				continue
			}

			// Get connection safely
			client.subMu.RLock()
			conn := client.Conn
			client.subMu.RUnlock()

			if conn == nil {
				continue
			}

			if err := conn.WriteJSON(msg); err != nil {
				lg.DebugC("WebSocket write error:", "error", err.Error())
				client.connected.Store(false)
				return
			}
		}
	}
}

// handleMessage - Traite les messages reçus du serveur
func (client *Client) handleMessage(data WsMessage) {
	if data.Action == "" {
		return
	}

	switch data.Action {
	case "pong":
	case publish:
		// Message publié sur un topic
		client.handlePublishMessage(data)

	case "publish_ack":
		// Message publié avec demande d'ACK
		client.handlePublishAckMessage(data)

	case direct_message:
		// Message direct vers ce client
		if client.onId != nil {
			msgData := data.Data
			msgFrom := data.From
			if m, ok := data.Data.(map[string]any); ok {
				if d, ok := m["data"]; ok {
					msgData = d
					if f, ok := m["from"].(string); ok && f != "" {
						msgFrom = f
					}
				}
			}
			client.onId(Message{Data: msgData, From: msgFrom})
		}

	case "subscribed", "unsubscribed", "published":
		// Confirmations - on peut les ignorer ou les logger
	case "error":
		// Erreur du serveur
		errMsg := data.Error
		if errMsg == "" {
			if s, ok := data.Data.(string); ok {
				errMsg = s
			}
		}
		if errMsg != "" {
			lg.ErrorC("Server error:", "error", errMsg)
		}

	case "ack_response":
		// Réponse ACK du serveur
		client.handleAckResponse(data)

	case "ack_status":
		// Statut ACK du serveur
		client.handleAckStatus(data)
	}

	// Callback utilisateur pour tous les messages
	if client.onDataWS != nil {
		client.subMu.RLock()
		conn := client.Conn
		client.subMu.RUnlock()
		if conn != nil {
			client.onDataWS(data, conn)
		}
	}
}

// handlePublishMessage - Traite les messages publiés
func (client *Client) handlePublishMessage(data WsMessage) {
	if data.Topic == "" {
		return
	}

	client.subMu.RLock()
	sub := client.subscriptions[data.Topic]
	client.subMu.RUnlock()

	if sub != nil && sub.active.Load() {
		// Créer fonction d'unsubscribe
		unsubFn := func() {
			client.Unsubscribe(data.Topic)
		}

		// Exécuter le callback inline (pas de goroutine pour éviter les allocations)
		// Le message handler tourne déjà dans sa propre goroutine

		// Déballer le message si nécessaire (aplatir la structure JSON reçue)
		msgData := data.Data
		msgFrom := data.From

		if m, ok := data.Data.(map[string]any); ok {
			// Vérifier si c'est une structure Message sérialisée
			if d, ok := m["data"]; ok {
				msgData = d
				// Essayer de récupérer le from imbriqué
				if f, ok := m["from"].(string); ok && f != "" {
					msgFrom = f
				}
			}
		}

		sub.callback(Message{
			Data: msgData,
			From: msgFrom,
		}, unsubFn)
	}
}

// handlePublishAckMessage - Traite les messages publiés avec ACK
func (client *Client) handlePublishAckMessage(data WsMessage) {
	if data.Topic == "" {
		return
	}

	client.subMu.RLock()
	sub := client.subscriptions[data.Topic]
	client.subMu.RUnlock()

	// Support pour PublishToIDWithAck : rediriger vers OnId si c'est un message direct avec ACK
	isDirectAck := strings.HasPrefix(data.Topic, "__direct_"+client.Id)

	if sub == nil && isDirectAck && client.onId != nil {
		// Créer un subscriber temporaire mappé sur OnId
		sub = &clientSubscription{
			topic: data.Topic,
			callback: func(msg Message, unsub func()) {
				client.onId(msg)
			},
		}
		sub.active.Store(true)
	}

	if sub != nil && sub.active.Load() {
		// Créer fonction d'unsubscribe
		unsubFn := func() {
			client.Unsubscribe(data.Topic)
		}

		// Exécuter le callback
		go func() {
			var success = true
			var errorMsg = ""

			// Déballer le message si nécessaire
			msgData := data.Data
			msgFrom := data.From

			if m, ok := data.Data.(map[string]any); ok {
				if d, ok := m["data"]; ok {
					msgData = d
					if f, ok := m["from"].(string); ok && f != "" {
						msgFrom = f
					}
				}
			}

			// Récupérer les panics
			defer func() {
				if r := recover(); r != nil {
					success = false
					errorMsg = fmt.Sprintf("panic: %v", r)
				}
				// Envoyer ACK après traitement (succès ou erreur)
				if data.AckID != "" {
					client.sendAck(data.AckID, success, errorMsg)
				}
			}()

			sub.callback(Message{
				Data: msgData,
				From: msgFrom,
			}, unsubFn)
		}()
	} else if data.AckID != "" {
		// Pas de subscriber, envoyer ACK d'erreur
		client.sendAck(data.AckID, false, "no subscriber for topic")
	}
}

// sendAck - Envoie un acknowledgment
func (client *Client) sendAck(ackID string, success bool, errorMsg string) {
	ackMsg := WsMessage{
		Action: ack,
		AckID:  ackID,
		From:   client.Id,
	}

	// Ajouter les données d'ACK dans le champ Data
	ackData := map[string]any{
		"ack_id":    ackID,
		"client_id": client.Id,
		"success":   success,
	}
	if errorMsg != "" {
		ackData["error"] = errorMsg
	}
	ackMsg.Data = ackData

	client.sendMessage(ackMsg)
}

// Subscribe - Souscription WebSocket optimisée
func (client *Client) Subscribe(topic string, callback func(data Message, unsub func())) (unsub func()) {
	if !client.connected.Load() {
		lg.DebugC("Cannot subscribe: client not connected")
		return func() {}
	}

	// Créer la subscription locale
	sub := &clientSubscription{
		topic:    topic,
		callback: callback,
	}
	sub.active.Store(true)

	client.subMu.Lock()
	client.subscriptions[topic] = sub
	client.subMu.Unlock()

	// Envoyer la demande de souscription au serveur
	client.sendMessage(WsMessage{
		Action: subscribe,
		Topic:  topic,
		From:   client.Id,
	})

	// Retourner fonction d'unsubscribe
	return func() {
		client.Unsubscribe(topic)
	}
}

// Unsubscribe - Désabonnement WebSocket
func (client *Client) Unsubscribe(topic string) {
	client.subMu.Lock()
	if sub := client.subscriptions[topic]; sub != nil {
		sub.active.Store(false)
		delete(client.subscriptions, topic)
	}
	client.subMu.Unlock()

	if client.connected.Load() {
		// Envoyer la demande de désabonnement au serveur
		client.sendMessage(WsMessage{
			Action: unsubscribe,
			Topic:  topic,
			From:   client.Id,
		})
	}
}

// Publish - Publication WebSocket optimisée
func (client *Client) Publish(topic string, data any) {
	if !client.connected.Load() {
		lg.DebugC("Cannot publish: client not connected")
		return
	}

	client.sendMessage(WsMessage{
		Action: publish,
		Topic:  topic,
		Data:   data,
		From:   client.Id,
	})
}

// PublishToID - Envoi de message direct vers un ID (client ou serveur)
func (client *Client) PublishToID(targetID string, data any) {
	if !client.connected.Load() {
		lg.DebugC("Cannot send direct message: client not connected")
		return
	}

	client.sendMessage(WsMessage{
		Action: direct_message,
		To:     targetID,
		Data:   data,
		From:   client.Id,
	})
}

// serverToserver - envoie message d'un autre serveur vers serveur actuel
func (client *Client) serverToserver(toServer, proxyAddr string, data any) {
	if !client.connected.Load() {
		lg.DebugC("Cannot send serverToserver: client not connected")
		return
	}
	client.sendMessage(WsMessage{
		Action: server_to_server,
		To:     toServer,
		Data:   data,
		ID:     proxyAddr,
	})
}

// PublishToServer - Envoi de message vers un serveur distant via le serveur local
// if toServer behind proxy 'bus.example.com'
//
//	address on server localhost:9999, addrWithPath="bus.example.com/ws/bus::localhost:9999"
//	                               OR addrWithPath="bus.example.com::localhost:9999/ws/bus"
func (client *Client) PublishToServer(addrWithPath string, data any, secure ...bool) {
	if !client.connected.Load() {
		lg.WarnC("Cannot send to server: client not connected")
		return
	}
	var issecure bool
	if len(secure) > 0 {
		issecure = secure[0]
	}

	client.sendMessage(WsMessage{
		Action: publish_to_server,
		To:     addrWithPath,
		Data:   data,
		From:   client.Id,
		Status: map[string]bool{
			"is_secure": issecure,
		},
	})
}

// PublishWithAck - Publication avec acknowledgment via le serveur
func (client *Client) PublishWithAck(topic string, data any, timeout time.Duration) *ClientAck {
	if !client.connected.Load() {
		lg.DebugC("Cannot publish with ACK: client not connected")
		return &ClientAck{id: "disconnected", cancelled: atomic.Bool{}}
	}

	ackID := client.generateAckID()

	clientAck := &ClientAck{
		id:        ackID,
		client:    client,
		timeout:   timeout,
		responses: make(chan map[string]AckResponse, 1),
		status:    make(chan map[string]bool, 10),
		done:      make(chan struct{}),
	}

	// Enregistrer l'ACK localement pour recevoir les réponses
	client.subMu.Lock()
	client.ackRequests[ackID] = clientAck
	client.subMu.Unlock()

	// Envoyer la demande au serveur
	client.sendMessage(WsMessage{
		Action: publish_with_ack,
		Topic:  topic,
		Data:   data,
		AckID:  ackID,
		From:   client.Id,
	})

	return clientAck
}

// PublishToIDWithAck - Envoi de message direct avec acknowledgment
func (client *Client) PublishToIDWithAck(targetID string, data any, timeout time.Duration) *ClientAck {
	if !client.connected.Load() {
		lg.DebugC("Cannot send direct message with ACK: client not connected")
		return &ClientAck{id: "disconnected", cancelled: atomic.Bool{}}
	}

	ackID := client.generateAckID()

	clientAck := &ClientAck{
		id:        ackID,
		client:    client,
		timeout:   timeout,
		responses: make(chan map[string]AckResponse, 1),
		status:    make(chan map[string]bool, 10),
		done:      make(chan struct{}),
	}

	// Enregistrer l'ACK localement
	client.subMu.Lock()
	client.ackRequests[ackID] = clientAck
	client.subMu.Unlock()

	// Envoyer la demande au serveur
	client.sendMessage(WsMessage{
		Action: publish_to_id_with_ack,
		To:     targetID,
		Data:   data,
		AckID:  ackID,
		From:   client.Id,
	})

	return clientAck
}

// sendMessage - Envoi non-bloquant de message
func (client *Client) sendMessage(msg WsMessage) {
	if client.Done == nil {
		return
	}
	select {
	case <-client.Done:
		return
	case client.messageQueue <- msg:
		// Message envoyé à la queue
	}
}

// generateAckID - Génère un ID unique pour ACK côté client
func (client *Client) generateAckID() string {
	return fmt.Sprintf("client_ack_%s_%d", client.Id, client.nextAckID.Add(1))
}

// handleAckResponse - Traite les réponses ACK du serveur
func (client *Client) handleAckResponse(data WsMessage) {
	if data.AckID == "" {
		return
	}

	client.subMu.RLock()
	clientAck := client.ackRequests[data.AckID]
	client.subMu.RUnlock()

	if clientAck == nil || clientAck.cancelled.Load() {
		return
	}

	// Extraire les réponses
	if data.Responses != nil {
		// Envoyer les réponses
		select {
		case clientAck.responses <- data.Responses:
		default:
		}
	} else if data.Data != nil {
		// Fallback si Responses n'est pas utilisé (compatibilité)
		if payload, ok := data.Data.(map[string]any); ok {
			if responsesData, ok := payload["responses"].(map[string]any); ok {
				responses := make(map[string]AckResponse)
				for clientID, respData := range responsesData {
					if respMap, ok := respData.(map[string]any); ok {
						resp := AckResponse{
							ClientID: clientID,
						}
						if success, ok := respMap["success"].(bool); ok {
							resp.Success = success
						}
						if errorMsg, ok := respMap["error"].(string); ok {
							resp.Error = errorMsg
						}
						responses[clientID] = resp
					}
				}
				select {
				case clientAck.responses <- responses:
				default:
				}
			}
		}
	}
}

// handleAckStatus - Traite les statuts ACK du serveur
func (client *Client) handleAckStatus(data WsMessage) {
	if data.AckID == "" {
		return
	}

	client.subMu.RLock()
	clientAck := client.ackRequests[data.AckID]
	client.subMu.RUnlock()

	if clientAck == nil || clientAck.cancelled.Load() {
		return
	}

	// Extraire le statut
	if data.Status != nil {
		// Envoyer le statut
		select {
		case clientAck.status <- data.Status:
		default:
		}
	} else if data.Data != nil {
		// Fallback compatibilité
		if payload, ok := data.Data.(map[string]any); ok {
			if statusData, ok := payload["status"].(map[string]any); ok {
				status := make(map[string]bool)
				for clientID, received := range statusData {
					if receivedBool, ok := received.(bool); ok {
						status[clientID] = receivedBool
					}
				}
				select {
				case clientAck.status <- status:
				default:
				}
			}
		}
	}
}

// reconnect - Reconnexion automatique sécurisée
func (client *Client) reconnect() {
	if !client.Autorestart || !client.reconnecting.CompareAndSwap(false, true) {
		return
	}
	defer client.reconnecting.Store(false)

	for {
		select {
		case <-client.Done:
			return
		default:
			if client.connected.Load() {
				return
			}

			time.Sleep(client.RestartEvery)
			lg.Info("Attempting to reconnect...", "id", client.Id)

			// Extraire l'adresse host et le path propres de ServerAddr
			addr := client.ServerAddr
			path := ""
			if u, err := url.Parse(client.ServerAddr); err == nil {
				addr = u.Host
				path = u.Path
			}

			opts := ClientOptions{
				Id:           client.Id,
				Address:      addr,
				Path:         path,
				Autorestart:  client.Autorestart,
				RestartEvery: client.RestartEvery,
				OnDataWs:     client.onDataWS,
				OnID:         client.onId,
				OnClose:      client.onClose,
			}

			if err := client.connect(opts); err == nil {
				client.resubscribeAll()
				return
			}
		}
	}
}

// resubscribeAll - Re-souscrit à tous les topics après reconnexion
func (client *Client) resubscribeAll() {
	client.subMu.RLock()
	topics := make([]string, 0, len(client.subscriptions))
	for topic := range client.subscriptions {
		topics = append(topics, topic)
	}
	client.subMu.RUnlock()

	// Re-souscrire à tous les topics
	for _, topic := range topics {
		client.sendMessage(WsMessage{
			Action: subscribe,
			Topic:  topic,
			From:   client.Id,
		})
	}
}

func (client *Client) Close() error {
	if client == nil {
		return nil
	}

	// First, signal all goroutines to stop
	client.connected.Store(false)

	// Signal Done channel to stop messageHandler and messageSender
	select {
	case <-client.Done:
		// Already closed
	default:
		close(client.Done)
	}

	// Give goroutines a moment to exit their read/write operations
	// This prevents the race between Close() and ReadJSON()
	time.Sleep(10 * time.Millisecond)

	if client.onClose != nil {
		client.onClose()
	}

	// Now safe to close the connection
	client.subMu.Lock()
	if client.Conn != nil {
		func() {
			defer func() { recover() }()
			_ = client.Conn.WriteMessage(ws.CloseMessage, ws.FormatCloseMessage(ws.CloseNormalClosure, ""))
			_ = client.Conn.Close()
		}()
		client.Conn = nil
	}
	client.subMu.Unlock()

	return nil
}

func (client *Client) Stop() error {
	if client == nil {
		return nil
	}

	// First, signal all goroutines to stop
	client.connected.Store(false)

	// Signal Done channel to stop messageHandler and messageSender
	select {
	case <-client.Done:
		// Already closed
	default:
		close(client.Done)
	}

	// Give goroutines a moment to exit their read/write operations
	// This prevents the race between Close() and ReadJSON()
	time.Sleep(10 * time.Millisecond)

	if client.onClose != nil {
		client.onClose()
	}

	// Now safe to close the connection
	client.subMu.Lock()
	if client.Conn != nil {
		func() {
			defer func() { recover() }()
			_ = client.Conn.WriteMessage(ws.CloseMessage, ws.FormatCloseMessage(ws.CloseNormalClosure, ""))
			_ = client.Conn.Close()
		}()
		client.Conn = nil
	}
	client.subMu.Unlock()

	return nil
}

func (client *Client) Shutdown() error {
	if client == nil {
		return nil
	}

	// First, signal all goroutines to stop
	client.connected.Store(false)

	// Signal Done channel to stop messageHandler and messageSender
	select {
	case <-client.Done:
		// Already closed
	default:
		close(client.Done)
	}

	// Give goroutines a moment to exit their read/write operations
	// This prevents the race between Close() and ReadJSON()
	time.Sleep(10 * time.Millisecond)

	if client.onClose != nil {
		client.onClose()
	}

	// Now safe to close the connection
	client.subMu.Lock()
	if client.Conn != nil {
		func() {
			defer func() { recover() }()
			_ = client.Conn.WriteMessage(ws.CloseMessage, ws.FormatCloseMessage(ws.CloseNormalClosure, ""))
			_ = client.Conn.Close()
		}()
		client.Conn = nil
	}
	client.subMu.Unlock()

	return nil
}

func (client *Client) Run() {
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	for {
		select {
		case <-client.Done:
			return
		case <-interrupt:
			lg.Info("Closed")
			client.Close()
			return
		}
	}
}

func (client *Client) OnClose(fn func()) {
	client.onClose = fn
}

// Wait - Attend tous les acknowledgments avec timeout (ClientAck)(clientID - Response)
func (ack *ClientAck) Wait() map[string]AckResponse {
	if ack.cancelled.Load() {
		return make(map[string]AckResponse)
	}

	timeout := time.After(ack.timeout)

	select {
	case responses := <-ack.responses:
		return responses
	case <-timeout:
		ack.Cancel()
		return make(map[string]AckResponse)
	case <-ack.done:
		return make(map[string]AckResponse)
	}
}

// WaitAny - Attend au moins un acknowledgment (ClientAck)
func (ack *ClientAck) WaitAny() (AckResponse, bool) {
	if ack.cancelled.Load() {
		return AckResponse{}, false
	}

	timeout := time.After(ack.timeout)

	select {
	case responses := <-ack.responses:
		// Retourner le premier ACK reçu
		for _, resp := range responses {
			return resp, true
		}
		return AckResponse{}, false
	case <-timeout:
		ack.Cancel()
		return AckResponse{}, false
	case <-ack.done:
		return AckResponse{}, false
	}
}

// GetStatus - Retourne le statut actuel des acknowledgments (ClientAck)
func (ack *ClientAck) GetStatus() map[string]bool {
	if ack.cancelled.Load() {
		return make(map[string]bool)
	}

	// Demander le statut au serveur
	ack.client.sendMessage(WsMessage{
		Action: get_ack_status,
		AckID:  ack.id,
		From:   ack.client.Id,
	})

	// Attendre la réponse du serveur
	timeout := time.After(2 * time.Second)
	select {
	case status := <-ack.status:
		return status
	case <-timeout:
		return make(map[string]bool)
	case <-ack.done:
		return make(map[string]bool)
	}
}

// IsComplete - Vérifie si tous les ACK sont reçus (ClientAck)
func (ack *ClientAck) IsComplete() bool {
	status := ack.GetStatus()
	for _, received := range status {
		if !received {
			return false
		}
	}
	return len(status) > 0
}

// Cancel - Annule l'attente des acknowledgments (ClientAck)
func (ack *ClientAck) Cancel() {
	if ack.cancelled.Load() {
		return
	}

	ack.cancelled.Store(true)

	// Envoyer la demande d'annulation au serveur
	ack.client.sendMessage(WsMessage{
		Action: cancel_ack,
		AckID:  ack.id,
		From:   ack.client.Id,
	})

	// Nettoyer localement
	ack.client.subMu.Lock()
	delete(ack.client.ackRequests, ack.id)
	ack.client.subMu.Unlock()

	close(ack.done)
}
