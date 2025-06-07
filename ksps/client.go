package ksps

import (
	"fmt"
	"net/url"
	"os"
	"os/signal"
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
	onDataWS     func(data map[string]any, conn *ws.Conn) error
	onId         func(data map[string]any, unsub ClientSubscriber)
	onClose      func()
	RestartEvery time.Duration
	Conn         *ws.Conn
	Autorestart  bool
	Done         chan struct{}

	// Optimisations pour pub/sub
	subscriptions map[string]*clientSubscription // topic -> subscription
	subMu         sync.RWMutex
	messageQueue  chan wsMessage // Queue pour messages sortants
	connected     atomic.Bool

	// ACK system côté client
	ackRequests map[string]*ClientAck // ackID -> ClientAck
	nextAckID   atomic.Uint64
}

type clientSubscription struct {
	topic    string
	callback func(data any, unsub func())
	active   atomic.Bool
}

type ClientConnectOptions struct {
	Id           string
	Address      string
	Secure       bool
	Path         string // default ksbus.ServerPath
	Autorestart  bool
	RestartEvery time.Duration
	OnDataWs     func(data map[string]any, conn *ws.Conn) error
	OnId         func(data map[string]any, unsub ClientSubscriber)
	OnClose      func()
}

type ClientSubscriber struct {
	client *Client
	Id     string
	Topic  string
	Ch     chan map[string]any
	Conn   *ws.Conn
}

func (subs ClientSubscriber) Unsubscribe() {
	subs.client.Unsubscribe(subs.Topic)
}

func NewClient(opts ClientConnectOptions) (*Client, error) {
	if opts.Autorestart && opts.RestartEvery == 0 {
		opts.RestartEvery = 10 * time.Second
	}
	if opts.OnDataWs == nil {
		opts.OnDataWs = func(data map[string]any, conn *ws.Conn) error { return nil }
	}
	cl := &Client{
		Id:            opts.Id,
		Autorestart:   opts.Autorestart,
		RestartEvery:  opts.RestartEvery,
		onDataWS:      opts.OnDataWs,
		onId:          opts.OnId,
		onClose:       opts.OnClose,
		Done:          make(chan struct{}),
		subscriptions: make(map[string]*clientSubscription),
		messageQueue:  make(chan wsMessage, 1024), // Buffer pour async
	}
	if cl.Id == "" {
		cl.Id = ksmux.GenerateID()
	}
	err := cl.connect(opts)
	if lg.CheckError(err) {
		return nil, err
	}
	return cl, nil
}

func (client *Client) connect(opts ClientConnectOptions) error {
	sch := "ws"
	if opts.Secure {
		sch = "wss"
	}
	spath := ""
	if opts.Path != "" {
		spath = opts.Path
	} else {
		spath = "/ws/bus"
	}
	u := url.URL{Scheme: sch, Host: opts.Address, Path: spath}
	client.ServerAddr = u.String()
	c, resp, err := ws.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		if client.Autorestart {
			lg.Info("Connection failed, retrying in", "seconds", client.RestartEvery.Seconds())
			time.Sleep(client.RestartEvery)
			return client.connect(opts)
		}
		if err == ws.ErrBadHandshake {
			lg.DebugC("handshake failed with status", "status", resp.StatusCode)
			return err
		}
		if err == ws.ErrCloseSent {
			lg.DebugC("server connection closed with status", "status", resp.StatusCode)
			return err
		} else {
			lg.DebugC("NewClient error", "err", err)
			return err
		}
	}
	client.Conn = c
	client.connected.Store(true)

	// Démarrer les goroutines de gestion
	go client.messageHandler()
	go client.messageSender()

	// Ping initial pour enregistrer le client
	client.sendMessage(wsMessage{
		Action: "ping",
		From:   client.Id,
	})

	lg.Printfs("client connected to %s\n", u.String())
	return nil
}

// messageHandler - Gère les messages entrants
func (client *Client) messageHandler() {
	for {
		select {
		case <-client.Done:
			return // Arrêter la goroutine

		default:
			if !client.connected.Load() {
				return
			}

			var m map[string]any
			err := client.Conn.ReadJSON(&m)
			if err != nil {
				lg.DebugC("WebSocket read error:", "error", err.Error())
				client.connected.Store(false)
				if client.Autorestart {
					go client.reconnect()
				}
				return
			}

			// Traiter le message
			client.handleMessage(m)
		}
	}
}

// messageSender - Gère l'envoi des messages en queue
func (client *Client) messageSender() {
	for {
		select {
		case <-client.Done:
			return // Arrêter la goroutine

		case msg, ok := <-client.messageQueue:
			if !ok {
				return // Channel fermé
			}

			if !client.connected.Load() {
				continue
			}

			if err := client.Conn.WriteJSON(msg); err != nil {
				lg.DebugC("WebSocket write error:", "error", err.Error())
				client.connected.Store(false)
				return
			}
		}
	}
}

// handleMessage - Traite les messages reçus du serveur
func (client *Client) handleMessage(data map[string]any) {
	action, ok := data["action"].(string)
	if !ok {
		return
	}

	switch action {
	case "pong":
		// Confirmation de connexion
		if client.onId != nil {
			client.onId(data, ClientSubscriber{client: client})
		}

	case "publish":
		// Message publié sur un topic
		client.handlePublishMessage(data)

	case "publish_ack":
		// Message publié avec demande d'ACK
		client.handlePublishAckMessage(data)

	case "direct_message":
		// Message direct vers ce client
		if client.onId != nil {
			payload := data["data"]
			client.onId(map[string]any{"data": payload}, ClientSubscriber{client: client})
		}

	case "subscribed", "unsubscribed", "published":
		// Confirmations - on peut les ignorer ou les logger
		lg.DebugC("Received confirmation:", "action", action)

	case "error":
		// Erreur du serveur
		if errMsg, ok := data["error"].(string); ok {
			lg.DebugC("Server error:", "error", errMsg)
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
		client.onDataWS(data, client.Conn)
	}
}

// handlePublishMessage - Traite les messages publiés
func (client *Client) handlePublishMessage(data map[string]any) {
	topic, ok := data["topic"].(string)
	if !ok {
		return
	}

	payload := data["data"]

	client.subMu.RLock()
	sub := client.subscriptions[topic]
	client.subMu.RUnlock()

	if sub != nil && sub.active.Load() {
		// Créer fonction d'unsubscribe
		unsubFn := func() {
			client.Unsubscribe(topic)
		}

		// Exécuter le callback
		go sub.callback(payload, unsubFn)
	}
}

// handlePublishAckMessage - Traite les messages publiés avec ACK
func (client *Client) handlePublishAckMessage(data map[string]any) {
	topic, ok := data["topic"].(string)
	if !ok {
		return
	}

	payload := data["data"]
	ackID, _ := data["ack_id"].(string)

	client.subMu.RLock()
	sub := client.subscriptions[topic]
	client.subMu.RUnlock()

	if sub != nil && sub.active.Load() {
		// Créer fonction d'unsubscribe
		unsubFn := func() {
			client.Unsubscribe(topic)
		}

		// Exécuter le callback
		go func() {
			var success = true
			var errorMsg = ""

			// Récupérer les panics
			defer func() {
				if r := recover(); r != nil {
					success = false
					errorMsg = fmt.Sprintf("panic: %v", r)
				}
				// Envoyer ACK après traitement (succès ou erreur)
				if ackID != "" {
					client.sendAck(ackID, success, errorMsg)
				}
			}()

			sub.callback(payload, unsubFn)
		}()
	} else if ackID != "" {
		// Pas de subscriber, envoyer ACK d'erreur
		client.sendAck(ackID, false, "no subscriber for topic")
	}
}

// sendAck - Envoie un acknowledgment
func (client *Client) sendAck(ackID string, success bool, errorMsg string) {
	ackMsg := wsMessage{
		Action: "ack",
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
func (client *Client) Subscribe(topic string, callback func(data any, unsub func())) func() {
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
	client.sendMessage(wsMessage{
		Action: "subscribe",
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
		client.sendMessage(wsMessage{
			Action: "unsubscribe",
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

	client.sendMessage(wsMessage{
		Action: "publish",
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

	client.sendMessage(wsMessage{
		Action: "direct_message",
		To:     targetID,
		Data:   data,
		From:   client.Id,
	})
}

// PublishToServer - Envoi de message vers un serveur distant via le serveur local
func (client *Client) PublishToServer(addr string, data any, secure ...bool) {
	if !client.connected.Load() {
		lg.DebugC("Cannot send to server: client not connected")
		return
	}

	client.sendMessage(wsMessage{
		Action: "publish_to_server",
		To:     addr,
		Data:   data,
		From:   client.Id,
	})
}

// PublishWithAck - Publication avec acknowledgment via le serveur
func (client *Client) PublishWithAck(topic string, data any, timeout time.Duration) *ClientAck {
	if !client.connected.Load() {
		lg.DebugC("Cannot publish with ACK: client not connected")
		return &ClientAck{ID: "disconnected", cancelled: atomic.Bool{}}
	}

	ackID := client.generateAckID()

	clientAck := &ClientAck{
		ID:        ackID,
		Client:    client,
		timeout:   timeout,
		responses: make(chan map[string]ackResponse, 1),
		status:    make(chan map[string]bool, 10),
		done:      make(chan struct{}),
	}

	// Enregistrer l'ACK localement pour recevoir les réponses
	client.subMu.Lock()
	if client.ackRequests == nil {
		client.ackRequests = make(map[string]*ClientAck)
	}
	client.ackRequests[ackID] = clientAck
	client.subMu.Unlock()

	// Envoyer la demande au serveur
	client.sendMessage(wsMessage{
		Action: "publish_with_ack",
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
		return &ClientAck{ID: "disconnected", cancelled: atomic.Bool{}}
	}

	ackID := client.generateAckID()

	clientAck := &ClientAck{
		ID:        ackID,
		Client:    client,
		timeout:   timeout,
		responses: make(chan map[string]ackResponse, 1),
		status:    make(chan map[string]bool, 10),
		done:      make(chan struct{}),
	}

	// Enregistrer l'ACK localement
	client.subMu.Lock()
	if client.ackRequests == nil {
		client.ackRequests = make(map[string]*ClientAck)
	}
	client.ackRequests[ackID] = clientAck
	client.subMu.Unlock()

	// Envoyer la demande au serveur
	client.sendMessage(wsMessage{
		Action: "publish_to_id_with_ack",
		To:     targetID,
		Data:   data,
		AckID:  ackID,
		From:   client.Id,
	})

	return clientAck
}

// sendMessage - Envoi non-bloquant de message
func (client *Client) sendMessage(msg wsMessage) {
	select {
	case client.messageQueue <- msg:
	default:
		// Queue pleine, on drop le message
		lg.DebugC("Message queue full, dropping message")
	}
}

// generateAckID - Génère un ID unique pour ACK côté client
func (client *Client) generateAckID() string {
	return fmt.Sprintf("client_ack_%s_%d", client.Id, client.nextAckID.Add(1))
}

// handleAckResponse - Traite les réponses ACK du serveur
func (client *Client) handleAckResponse(data map[string]any) {
	ackID, ok := data["ack_id"].(string)
	if !ok {
		return
	}

	client.subMu.RLock()
	clientAck := client.ackRequests[ackID]
	client.subMu.RUnlock()

	if clientAck == nil || clientAck.cancelled.Load() {
		return
	}

	// Extraire les réponses
	if responsesData, ok := data["responses"].(map[string]any); ok {
		responses := make(map[string]ackResponse)
		for clientID, respData := range responsesData {
			if respMap, ok := respData.(map[string]any); ok {
				resp := ackResponse{
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

		// Envoyer les réponses
		select {
		case clientAck.responses <- responses:
		default:
		}
	}
}

// handleAckStatus - Traite les statuts ACK du serveur
func (client *Client) handleAckStatus(data map[string]any) {
	ackID, ok := data["ack_id"].(string)
	if !ok {
		return
	}

	client.subMu.RLock()
	clientAck := client.ackRequests[ackID]
	client.subMu.RUnlock()

	if clientAck == nil || clientAck.cancelled.Load() {
		return
	}

	// Extraire le statut
	if statusData, ok := data["status"].(map[string]any); ok {
		status := make(map[string]bool)
		for clientID, received := range statusData {
			if receivedBool, ok := received.(bool); ok {
				status[clientID] = receivedBool
			}
		}

		// Envoyer le statut
		select {
		case clientAck.status <- status:
		default:
		}
	}
}

// reconnect - Reconnexion automatique
func (client *Client) reconnect() {
	if !client.Autorestart {
		return
	}

	time.Sleep(client.RestartEvery)

	// Essayer de se reconnecter
	opts := ClientConnectOptions{
		Id:           client.Id,
		Address:      client.ServerAddr,
		Autorestart:  client.Autorestart,
		RestartEvery: client.RestartEvery,
		OnDataWs:     client.onDataWS,
		OnId:         client.onId,
		OnClose:      client.onClose,
	}

	if err := client.connect(opts); err == nil {
		// Reconnexion réussie, re-souscrire aux topics
		client.resubscribeAll()
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
		client.sendMessage(wsMessage{
			Action: "subscribe",
			Topic:  topic,
			From:   client.Id,
		})
	}
}

func (client *Client) Close() error {
	client.connected.Store(false)

	if client.onClose != nil {
		client.onClose()
	}

	if client.Conn != nil {
		err := client.Conn.WriteMessage(ws.CloseMessage, ws.FormatCloseMessage(ws.CloseNormalClosure, ""))
		if err != nil {
			return err
		}
		err = client.Conn.Close()
		if err != nil {
			return err
		}
		client.Conn = nil
	}

	close(client.messageQueue)
	close(client.Done)
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

// Wait - Attend tous les acknowledgments avec timeout (ClientAck)
func (ack *ClientAck) Wait() map[string]ackResponse {
	if ack.cancelled.Load() {
		return make(map[string]ackResponse)
	}

	timeout := time.After(ack.timeout)

	select {
	case responses := <-ack.responses:
		return responses
	case <-timeout:
		ack.Cancel()
		return make(map[string]ackResponse)
	case <-ack.done:
		return make(map[string]ackResponse)
	}
}

// WaitAny - Attend au moins un acknowledgment (ClientAck)
func (ack *ClientAck) WaitAny() (ackResponse, bool) {
	if ack.cancelled.Load() {
		return ackResponse{}, false
	}

	timeout := time.After(ack.timeout)

	select {
	case responses := <-ack.responses:
		// Retourner le premier ACK reçu
		for _, resp := range responses {
			return resp, true
		}
		return ackResponse{}, false
	case <-timeout:
		ack.Cancel()
		return ackResponse{}, false
	case <-ack.done:
		return ackResponse{}, false
	}
}

// GetStatus - Retourne le statut actuel des acknowledgments (ClientAck)
func (ack *ClientAck) GetStatus() map[string]bool {
	if ack.cancelled.Load() {
		return make(map[string]bool)
	}

	// Demander le statut au serveur
	ack.Client.sendMessage(wsMessage{
		Action: "get_ack_status",
		AckID:  ack.ID,
		From:   ack.Client.Id,
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
	ack.Client.sendMessage(wsMessage{
		Action: "cancel_ack",
		AckID:  ack.ID,
		From:   ack.Client.Id,
	})

	// Nettoyer localement
	ack.Client.subMu.Lock()
	delete(ack.Client.ackRequests, ack.ID)
	ack.Client.subMu.Unlock()

	close(ack.done)
}
