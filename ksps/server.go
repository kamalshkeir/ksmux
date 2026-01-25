package ksps

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/kamalshkeir/ksmux"
	"github.com/kamalshkeir/ksmux/ws"
	"github.com/kamalshkeir/lg"
)

type ServerBus struct {
	ID           string
	Path         string
	Bus          *Bus
	WsMidws      []func(ksmux.Handler) ksmux.Handler
	onUpgradeWS  func(r *http.Request) bool
	onId         func(data Message)
	onServerData []func(data Message)
	router       *ksmux.Router

	// WebSocket connections management
	connections map[string]*ws.Conn // clientID -> connection
	connMu      sync.RWMutex
	Secure      bool // if run on https
}

var DefaultConfig = ksmux.Config{
	Address: "localhost:9313",
}

func NewServer(config ...ksmux.Config) *ServerBus {
	var router *ksmux.Router
	if len(config) > 0 {
		router = ksmux.New(config...)
	} else {
		router = ksmux.New(DefaultConfig)
	}
	return &ServerBus{
		ID:          ksmux.GenerateID(),
		Path:        "/ws/bus",
		Bus:         New(),
		WsMidws:     make([]func(ksmux.Handler) ksmux.Handler, 0),
		router:      router,
		connections: make(map[string]*ws.Conn),
		onUpgradeWS: func(r *http.Request) bool {
			return true
		},
		onServerData: make([]func(data Message), 0),
	}
}

func (sb *ServerBus) App() *ksmux.Router {
	return sb.router
}

func (sb *ServerBus) Router() *ksmux.Router {
	return sb.router
}

func (sb *ServerBus) OnUpgradeWS(fn func(r *http.Request) bool) {
	sb.onUpgradeWS = fn
}

func (sb *ServerBus) OnID(fn func(data Message)) {
	sb.onId = fn
}

func (sb *ServerBus) OnServerData(fn func(data Message)) {
	sb.onServerData = append(sb.onServerData, fn)
}

// Subscribe - Souscription locale (délègue au bus)
func (sb *ServerBus) Subscribe(topic string, fn func(data Message, unsub func())) (unsub func()) {
	return sb.Bus.Subscribe(topic, fn)
}

// Unsubscribe - Désabonnement local (délègue au bus)
func (sb *ServerBus) Unsubscribe(topic string) {
	sb.Bus.Unsubscribe(topic)
}

// Publish - Publication unifiée (délègue au bus)
func (sb *ServerBus) Publish(topic string, data any) {
	sb.Bus.Publish(topic, Message{
		Data: data,
		From: sb.ID,
	})
}

// PublishToID - Publication directe ultra-rapide via le registre du bus
func (sb *ServerBus) PublishToID(clientID string, data any) {
	sb.Bus.wsConnsMu.RLock()
	wsConn := sb.Bus.wsConns[clientID]
	sb.Bus.wsConnsMu.RUnlock()

	if clientID == sb.ID && sb.onId != nil {
		if m, ok := data.(Message); ok {
			sb.onId(m)
		} else {
			sb.onId(Message{
				Data: data,
				From: sb.ID,
			})
		}
	}

	if wsConn != nil {
		msgToSend := WsMessage{
			Action: direct_message,
			Data:   data,
			From:   sb.ID,
		}

		if m, ok := data.(Message); ok {
			msgToSend = WsMessage{
				Action: direct_message,
				Data:   m.Data,
				From:   m.From,
			}
		}
		sb.Bus.sendToWebSocket(wsConn, msgToSend)
	}
}

func (sb *ServerBus) PublishWithAck(topic string, data any, timeout time.Duration) *Ack {
	return sb.Bus.publishWithAck(topic, Message{
		Data: data,
		From: sb.ID,
	}, timeout)
}

func (sb *ServerBus) PublishToIDWithAck(clientID string, data any, timeout time.Duration) *Ack {
	// Ne pas ré-envelopper si c'est déjà un Message
	var msgToSend any = Message{
		Data: data,
		From: sb.ID,
	}
	if m, ok := data.(Message); ok {
		msgToSend = m
	}

	// 1. Cas où la cible est le serveur lui-même
	if clientID == sb.ID {
		tempTopic := fmt.Sprintf("__direct_%s_%d", clientID, time.Now().UnixNano())
		// Souscription interne temporaire
		sb.Bus.Subscribe(tempTopic, func(m Message, unsub func()) {
			if sb.onId != nil {
				sb.onId(m)
			}
			unsub()
		})
		// Publier avec ACK
		return sb.Bus.publishWithAck(tempTopic, msgToSend, timeout)
	}

	// 2. Cas où la cible est un client WebSocket
	sb.connMu.RLock()
	conn := sb.connections[clientID]
	sb.connMu.RUnlock()

	if conn == nil {
		// Retourner un ACK vide si pas de connexion
		return &Ack{
			id:      "no-connection",
			request: nil,
			bus:     sb.Bus,
		}
	}

	// Pour PublishToID avec ACK, on crée un topic temporaire unique
	tempTopic := fmt.Sprintf("__direct_%s_%d", clientID, time.Now().UnixNano())

	// Souscrire temporairement le client au topic
	sb.Bus.subscribeWebSocket(clientID, tempTopic, conn)

	// Publier avec ACK
	ack := sb.Bus.publishWithAck(tempTopic, msgToSend, timeout)

	// Nettoyer après timeout
	go func() {
		time.Sleep(timeout + time.Second)
		sb.Bus.unsubscribeWebSocket(clientID, tempTopic)
	}()

	return ack
}

func (sb *ServerBus) PublishToServer(addrWithPath string, data any, secure ...bool) error {
	return sb.publishToServerWithFrom(addrWithPath, data, "", secure...)
}

func (sb *ServerBus) publishToServerWithFrom(addrWithPath string, data any, fromClient string, secure ...bool) error {
	// Créer un client pour se connecter au serveur distant
	toAddress := addrWithPath
	sp := strings.Split(addrWithPath, "::")
	proxyAddr := ""
	if len(sp) > 1 {
		addrWithPath = sp[0]
		proxyAddr = sp[1]
	}
	path := ""

	if strings.Contains(addrWithPath, "/") {
		u, err := url.Parse("ws://" + addrWithPath)
		if err == nil {
			toAddress = u.Host
			path = u.Path
		}
	}
	if proxyAddr != "" && strings.Contains(proxyAddr, "/") {
		u, err := url.Parse("ws://" + proxyAddr)
		if err == nil {
			path = u.Path
		}
	}

	client, err := NewClient(ClientOptions{
		Id:      "to-" + toAddress,
		Address: toAddress,
		Path:    path,
		Secure:  len(secure) > 0 && secure[0],
	})
	if err != nil {
		return err
	}
	defer client.Close()

	currentCompleteAddress := "ws://"
	if sb.Secure {
		currentCompleteAddress = "wss://"
	}
	currentCompleteAddress += sb.router.Address() + sb.Path
	if fromClient != "" {
		currentCompleteAddress = fromClient + "---" + currentCompleteAddress
	}
	currentCompleteAddress = strings.ReplaceAll(currentCompleteAddress, ":443", "")
	currentCompleteAddress = strings.ReplaceAll(currentCompleteAddress, ":80", "")
	// Publier direct messsage on any topic, will be intercepted before actions on destination server

	client.serverToserver(toAddress, proxyAddr, Message{
		Data: data,
		From: currentCompleteAddress,
	})

	// ATTENDRE que le message soit envoyé avant de Close (puisque c'est une connexion éphémère)
	timeout := time.After(3 * time.Second)
drain:
	for {
		select {
		case <-timeout:
			break drain
		default:
			if len(client.messageQueue) == 0 {
				time.Sleep(200 * time.Millisecond) // Un petit peu plus pour l'OS
				break drain
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	return nil
}

func (sb *ServerBus) WithPprof(path ...string) {
	sb.router.WithPprof(path...)
}

func (sb *ServerBus) Run() {
	sb.handleWS()
	sb.router.Run()
}

func (sb *ServerBus) RunTLS() {
	sb.handleWS()
	sb.router.RunTLS()
}

func (sb *ServerBus) RunAutoTLS() {
	sb.handleWS()
	sb.router.RunAutoTLS()
}

// Stop stop the server and close all connection
func (sb *ServerBus) Stop() {
	sb.closeAllConnections() // Fermer toutes les connexions WebSocket
	sb.Bus.Close()           // Fermer le bus et arrêter toutes les goroutines
	sb.router.Stop()
}

// Close alias to Stop
func (sb *ServerBus) Close() {
	sb.closeAllConnections() // Fermer toutes les connexions WebSocket
	sb.Bus.Close()           // Fermer le bus et arrêter toutes les goroutines
	sb.router.Stop()
}

// Shutdown alias to Stop
func (sb *ServerBus) Shutdown() {
	sb.closeAllConnections() // Fermer toutes les connexions WebSocket
	sb.Bus.Close()           // Fermer le bus et arrêter toutes les goroutines
	sb.router.Stop()
}

func (sb *ServerBus) CountClients() int {
	sb.connMu.RLock()
	defer sb.connMu.RUnlock()
	return len(sb.connections)
}

func (sb *ServerBus) handleWS() {
	handler := sb.handlerBusWs()
	for _, h := range sb.WsMidws {
		handler = h(handler)
	}
	sb.router.Get(sb.Path, handler)
}

func (sb *ServerBus) handlerBusWs() ksmux.Handler {
	return func(c *ksmux.Context) {
		if !sb.onUpgradeWS(c.Request) {
			c.Status(403).Text("Forbidden")
			return
		}

		conn, err := c.UpgradeConnection()
		if lg.CheckError(err) {
			return
		}
		var clientID string

		defer func() {
			conn.Close()
			if clientID != "" {
				sb.cleanupConnection(clientID, conn)
			} else {
				// Fallback : si on n'a pas d'ID, on cherche par connexion (au cas où)
				sb.cleanupConnectionByConn(conn)
			}
		}()

		for {
			var m WsMessage
			err := conn.ReadJSON(&m)
			if err != nil {
				lg.DebugC("WebSocket read error:", "error", err.Error())
				break
			}

			// messages coming from another server to current server
			if m.Action == server_to_server {
				isForMe := m.To == sb.router.Address() || m.To == sb.router.Config.Domain || m.ID == sb.router.Address() || m.To == sb.router.Host() || m.ID == sb.router.Host()

				if isForMe {
					// 1. Tenter de rediffuser sur le bus si un topic est présent dans les données
					if dataMap, ok := m.Data.(map[string]any); ok {
						var userPayload any = dataMap
						var from string = m.From

						// Si c'est encapsulé dans Message, on déballe
						if d, ok := dataMap["data"]; ok {
							userPayload = d
							if f, ok := dataMap["from"].(string); ok {
								from = f
							}
						}

						// Vérifier si le payload utilisateur contient un topic pour publication
						// Le format attendu pour une republication automatique est { "topic": "...", "data": ... }
						if payloadMap, ok := userPayload.(map[string]any); ok {
							if topic, ok := payloadMap["topic"].(string); ok {
								dataContent := payloadMap["data"]

								// Publier sur le bus local
								sb.Bus.Publish(topic, Message{
									Data: dataContent,
									From: from,
								})
							}
						}
					}

					// 2. Notifier les callbacks 'onServerData'
					msg := Message{From: m.From}
					if m.Data != nil {
						msg.Data = m.Data
					}
					// Si encapsulé
					if dataMap, ok := m.Data.(map[string]any); ok {
						if d, ok := dataMap["data"]; ok {
							msg.Data = d
							if f, ok := dataMap["from"].(string); ok {
								msg.From = f
							}
						}
					}

					for _, callback := range sb.onServerData {
						callback(msg)
					}
					continue
				}
			}

			// Traiter les actions du bus
			sb.handleBusActions(m, conn, &clientID)
		}
	}
}

// handleBusActions - Gère les actions du bus WebSocket
func (sb *ServerBus) handleBusActions(data WsMessage, conn *ws.Conn, clientID *string) {
	if data.Action == "" {
		sb.sendError(conn, *clientID, "missing or invalid action")
		return
	}

	switch data.Action {
	case ping:
		sb.handlePing(data, conn, clientID)

	case subscribe:
		sb.handleSubscribe(data, conn, *clientID)

	case unsubscribe:
		sb.handleUnsubscribe(data, conn, *clientID)

	case publish:
		sb.handlePublish(data, conn, *clientID)

	case direct_message:
		sb.handleDirectMessage(data, conn, *clientID)

	case publish_to_server:
		sb.handlePublishToServer(data, conn, *clientID)

	case ack:
		sb.handleAck(data, conn, *clientID)

	case publish_with_ack:
		sb.handlePublishWithAck(data, conn, *clientID)

	case publish_to_id_with_ack:
		sb.handlePublishToIDWithAck(data, conn, *clientID)

	case get_ack_status:
		sb.handleGetAckStatus(data, conn, *clientID)

	case cancel_ack:
		sb.handleCancelAck(data, conn, *clientID)

	default:
		sb.sendError(conn, *clientID, "unknown action: "+data.Action)
	}
}

// handlePing - Gère l'enregistrement du client
func (sb *ServerBus) handlePing(data WsMessage, conn *ws.Conn, clientID *string) {
	from := data.From
	if from == "" {
		from = ksmux.GenerateID()
	}

	// Si le client change d'ID sur la même connexion, on nettoie l'ancien ID (Fix ID Leak)
	if *clientID != "" && *clientID != from {
		sb.Bus.cleanupWebSocketClient(*clientID, conn)
	}

	*clientID = from
	// Enregistrer la connexion dans le Bus (Fix Invisible Client)
	sb.Bus.registerWebSocket(*clientID, conn)

	// MOLECULAR FIX: Register in ServerBus connections map
	sb.connMu.Lock()
	sb.connections[*clientID] = conn
	sb.connMu.Unlock()

	// Répondre avec l'ID confirmé
	sb.sendResponse(conn, *clientID, WsMessage{
		Action: "pong",
		ID:     *clientID,
	})
}

// handleDirectMessage - Gère les messages directs vers un ID
func (sb *ServerBus) handleDirectMessage(data WsMessage, conn *ws.Conn, clientID string) {
	if clientID == "" {
		sb.sendError(conn, clientID, "client not registered")
		return
	}

	if data.To == "" {
		sb.sendError(conn, clientID, "missing target ID")
		return
	}

	// Si le message est pour le serveur
	if data.To == sb.ID {
		if sb.onId != nil {
			sb.onId(Message{
				Data: data.Data,
				From: data.From,
			})
		}
		return
	}

	// Sinon, transférer vers le client cible
	sb.PublishToID(data.To, Message{
		Data: data.Data,
		From: data.From,
	})
}

// handlePublishToServer - Gère les demandes de publication vers un serveur distant
func (sb *ServerBus) handlePublishToServer(data WsMessage, conn *ws.Conn, clientID string) {
	if clientID == "" {
		sb.sendError(conn, clientID, "client not registered")
		return
	}

	var isSecure bool
	if sec, ok := data.Status["is_secure"]; ok {
		isSecure = sec
		data.Status = nil
	}

	if data.To == "" {
		sb.sendError(conn, clientID, "missing target server address")
		return
	}

	// Relayer vers le serveur distant avec fromServer
	err := sb.publishToServerWithFrom(data.To, data.Data, data.From, isSecure)
	if err != nil {
		sb.sendError(conn, clientID, "failed to relay to server: "+err.Error())
		return
	}

	// Confirmer l'envoi
	sb.sendResponse(conn, clientID, WsMessage{
		Action: "server_message_sent",
		To:     data.To,
	})
}

// handleAck - Gère les acknowledgments reçus
func (sb *ServerBus) handleAck(data WsMessage, conn *ws.Conn, clientID string) {
	if clientID == "" {
		sb.sendError(conn, clientID, "client not registered")
		return
	}

	// Les données ACK sont dans le champ "Data"
	ackData, ok := data.Data.(map[string]any)
	if !ok {
		// Fallback pour compatibilité si l'ACK ID est direct
		if data.AckID == "" {
			sb.sendError(conn, clientID, "missing ack data")
			return
		}
	}

	ackID := data.AckID
	if ackID == "" && ok {
		ackID, _ = ackData["ack_id"].(string)
	}

	if ackID == "" {
		sb.sendError(conn, clientID, "missing ack_id")
		return
	}

	var success bool
	var errorMsg string
	if ok {
		success, _ = ackData["success"].(bool)
		errorMsg, _ = ackData["error"].(string)
	}

	// Créer la réponse ACK
	ackResp := AckResponse{
		AckID:    ackID,
		ClientID: clientID,
		Success:  success,
		Error:    errorMsg,
	}

	// Transmettre au bus
	sb.Bus.handleAck(ackResp)
}

// handleSubscribe - Gère la souscription WebSocket
func (sb *ServerBus) handleSubscribe(data WsMessage, conn *ws.Conn, clientID string) {
	if clientID == "" {
		sb.sendError(conn, clientID, "client not registered, send ping first")
		return
	}

	if data.Topic == "" {
		sb.sendError(conn, clientID, "missing or invalid topic")
		return
	}

	// Souscrire via le bus unifié
	sb.Bus.subscribeWebSocket(clientID, data.Topic, conn)

	// Confirmer la souscription
	sb.sendResponse(conn, clientID, WsMessage{
		Action: "subscribed",
		Topic:  data.Topic,
	})
}

// handleUnsubscribe - Gère le désabonnement WebSocket
func (sb *ServerBus) handleUnsubscribe(data WsMessage, conn *ws.Conn, clientID string) {
	if clientID == "" {
		sb.sendError(conn, clientID, "client not registered")
		return
	}

	if data.Topic == "" {
		sb.sendError(conn, clientID, "missing or invalid topic")
		return
	}

	// Désabonner via le bus unifié
	sb.Bus.unsubscribeWebSocket(clientID, data.Topic)

	// Confirmer le désabonnement
	sb.sendResponse(conn, clientID, WsMessage{
		Action: "unsubscribed",
		Topic:  data.Topic,
	})
}

// handlePublish - Gère la publication WebSocket
func (sb *ServerBus) handlePublish(data WsMessage, conn *ws.Conn, clientID string) {
	if clientID == "" {
		sb.sendError(conn, clientID, "client not registered")
		return
	}

	if data.Topic == "" {
		sb.sendError(conn, clientID, "missing or invalid topic")
		return
	}

	// Publier via le bus unifié (va notifier TOUS les subscribers)
	sb.Bus.Publish(data.Topic, Message{
		Data: data.Data,
		From: data.From,
	})

	// Optionnel: confirmer la publication
	sb.sendResponse(conn, clientID, WsMessage{
		Action: "published",
		Topic:  data.Topic,
	})
}

// cleanupConnection - Nettoie une connexion fermée par clientID avec vérification de sécurité
func (sb *ServerBus) cleanupConnection(clientID string, conn *ws.Conn) {
	if clientID == "" {
		return
	}
	sb.connMu.Lock()
	// Vérifier que c'est bien NOTRE connexion avant de supprimer (protection contre overwrite d'ID)
	if sb.connections[clientID] == conn {
		delete(sb.connections, clientID)
	}
	sb.connMu.Unlock()
	// Nettoyer toutes les souscriptions
	sb.Bus.cleanupWebSocketClient(clientID, conn)
	lg.DebugC("Client disconnected and cleaned up:", "clientID", clientID)
}

// cleanupConnectionByConn - Fallback lent (O(N)) pour trouver et nettoyer une connexion sans ID
func (sb *ServerBus) cleanupConnectionByConn(conn *ws.Conn) {
	sb.connMu.Lock()
	var foundID string
	for id, c := range sb.connections {
		if c == conn {
			foundID = id
			delete(sb.connections, id)
			break
		}
	}
	sb.connMu.Unlock()

	if foundID != "" {
		sb.Bus.cleanupWebSocketClient(foundID, conn)
		lg.DebugC("Client (found by conn) disconnected and cleaned up:", "clientID", foundID)
	}
}

// sendResponse - Envoie une réponse JSON de manière sécurisée (via le Bus)
func (sb *ServerBus) sendResponse(conn *ws.Conn, clientID string, data WsMessage) {
	defer func() { recover() }() // Protection contre socket mockée/fermée prématurément

	if clientID == "" {
		// clientID vide = client non enregistré, on tente un envoi direct risqué ou on drop
		_ = conn.WriteJSON(data)
		return
	}

	sb.Bus.wsConnsMu.RLock()
	wsConn := sb.Bus.wsConns[clientID]
	sb.Bus.wsConnsMu.RUnlock()

	if wsConn != nil {
		sb.Bus.sendToWebSocket(wsConn, data)
	} else {
		// Fallback si pas encore dans le registre du bus
		_ = conn.WriteJSON(data)
	}
}

// sendError - Envoie une erreur JSON de manière sécurisée
func (sb *ServerBus) sendError(conn *ws.Conn, clientID string, message string) {
	sb.sendResponse(conn, clientID, WsMessage{
		Action: "error",
		Error:  message,
	})
}

// handlePublishWithAck - Gère les demandes de publication avec ACK du client
func (sb *ServerBus) handlePublishWithAck(data WsMessage, conn *ws.Conn, clientID string) {
	if clientID == "" {
		sb.sendError(conn, clientID, "client not registered")
		return
	}

	if data.Topic == "" {
		sb.sendError(conn, clientID, "missing or invalid topic")
		return
	}

	if data.AckID == "" {
		sb.sendError(conn, clientID, "missing ack_id")
		return
	}

	// Publier avec ACK via le bus
	ack := sb.Bus.publishWithAck(data.Topic, Message{
		Data: data.Data,
		From: data.From,
	}, 5*time.Second) // Timeout par défaut

	// Attendre les réponses et les renvoyer au client
	go func() {
		responses := ack.Wait()
		sb.sendResponse(conn, clientID, WsMessage{
			Action:    "ack_response",
			AckID:     data.AckID,
			Responses: responses,
		})
	}()
}

// handlePublishToIDWithAck - Gère les demandes de message direct avec ACK
func (sb *ServerBus) handlePublishToIDWithAck(data WsMessage, conn *ws.Conn, clientID string) {
	if clientID == "" {
		sb.sendError(conn, clientID, "client not registered")
		return
	}

	if data.To == "" {
		sb.sendError(conn, clientID, "missing target ID")
		return
	}

	if data.AckID == "" {
		sb.sendError(conn, clientID, "missing ack_id")
		return
	}

	// Publier avec ACK via le bus
	ack := sb.PublishToIDWithAck(data.To, Message{
		Data: data.Data,
		From: data.From,
	}, 5*time.Second)

	// Attendre les réponses et les renvoyer au client
	go func() {
		responses := ack.Wait()
		sb.sendResponse(conn, clientID, WsMessage{
			Action:    "ack_response",
			AckID:     data.AckID,
			Responses: responses,
		})
	}()
}

// handleGetAckStatus - Gère les demandes de statut ACK
func (sb *ServerBus) handleGetAckStatus(data WsMessage, conn *ws.Conn, clientID string) {
	if clientID == "" {
		sb.sendError(conn, clientID, "client not registered")
		return
	}

	if data.AckID == "" {
		sb.sendError(conn, clientID, "missing ack_id")
		return
	}

	// Chercher l'ACK dans le bus
	sb.Bus.ackMu.RLock()
	ackReq := sb.Bus.ackRequests[data.AckID]
	sb.Bus.ackMu.RUnlock()

	if ackReq == nil {
		sb.sendResponse(conn, clientID, WsMessage{
			Action: "ack_status",
			AckID:  data.AckID,
			Status: make(map[string]bool),
		})
		return
	}

	// Retourner le statut
	status := make(map[string]bool)
	ackReq.mu.RLock()
	for clientID, received := range ackReq.received {
		status[clientID] = received
	}
	ackReq.mu.RUnlock()

	sb.sendResponse(conn, clientID, WsMessage{
		Action: "ack_status",
		AckID:  data.AckID,
		Status: status,
	})
}

// handleCancelAck - Gère les demandes d'annulation ACK
func (sb *ServerBus) handleCancelAck(data WsMessage, conn *ws.Conn, clientID string) {
	if clientID == "" {
		sb.sendError(conn, clientID, "client not registered")
		return
	}

	if data.AckID == "" {
		sb.sendError(conn, clientID, "missing ack_id")
		return
	}

	// Chercher et annuler l'ACK dans le bus
	sb.Bus.cancelAck(data.AckID)

	// Confirmer l'annulation
	sb.sendResponse(conn, clientID, WsMessage{
		Action: "ack_cancelled",
		AckID:  data.AckID,
	})
}

// closeAllConnections - Ferme toutes les connexions WebSocket
func (sb *ServerBus) closeAllConnections() {
	sb.connMu.Lock()
	defer sb.connMu.Unlock()

	for clientID, conn := range sb.connections {
		// Envoyer message de fermeture
		conn.WriteMessage(ws.CloseMessage, ws.FormatCloseMessage(ws.CloseNormalClosure, "server shutdown"))
		// Fermer la connexion
		conn.Close()
		// Nettoyer
		sb.Bus.cleanupWebSocketClient(clientID, conn)
	}

	// Vider la map
	sb.connections = make(map[string]*ws.Conn)
}
