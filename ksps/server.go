package ksps

import (
	"fmt"
	"net/http"
	"net/url"
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
	onId         func(data any)
	onServerData []func(data any, conn *ws.Conn)
	router       *ksmux.Router

	// WebSocket connections management
	connections map[string]*ws.Conn // clientID -> connection
	connMu      sync.RWMutex
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
		onServerData: make([]func(data any, conn *ws.Conn), 0),
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

func (sb *ServerBus) OnID(fn func(data any)) {
	sb.onId = fn
}

func (sb *ServerBus) OnServerData(fn func(data any, conn *ws.Conn)) {
	sb.onServerData = append(sb.onServerData, fn)
}

// Subscribe - Souscription locale (délègue au bus)
func (sb *ServerBus) Subscribe(topic string, fn func(data any, unsub func())) func() {
	return sb.Bus.Subscribe(topic, fn)
}

// Unsubscribe - Désabonnement local (délègue au bus)
func (sb *ServerBus) Unsubscribe(topic string) {
	sb.Bus.Unsubscribe(topic)
}

// Publish - Publication unifiée (délègue au bus)
func (sb *ServerBus) Publish(topic string, data any) {
	sb.Bus.Publish(topic, data)
}

// PublishToID - Publication directe ultra-rapide via le registre du bus
func (sb *ServerBus) PublishToID(clientID string, data any) {
	sb.Bus.wsConnsMu.RLock()
	wsConn := sb.Bus.wsConns[clientID]
	sb.Bus.wsConnsMu.RUnlock()

	if wsConn != nil {
		sb.Bus.sendToWebSocket(wsConn, wsMessage{
			Action: "direct_message",
			Data:   data,
			From:   sb.ID,
		})
	}
}

func (sb *ServerBus) PublishWithAck(topic string, data any, timeout time.Duration) *Ack {
	return sb.Bus.PublishWithAck(topic, data, timeout)
}

func (sb *ServerBus) PublishToIDWithAck(clientID string, data any, timeout time.Duration) *Ack {
	// Vérifier que la connexion existe
	sb.connMu.RLock()
	conn := sb.connections[clientID]
	sb.connMu.RUnlock()

	if conn == nil {
		// Retourner un ACK vide si pas de connexion
		return &Ack{
			ID:      "no-connection",
			Request: nil,
			Bus:     sb.Bus,
		}
	}

	// Pour PublishToID avec ACK, on crée un topic temporaire unique
	tempTopic := fmt.Sprintf("__direct_%s_%d", clientID, time.Now().UnixNano())

	// Souscrire temporairement le client au topic
	sb.Bus.subscribeWebSocket(clientID, tempTopic, conn)

	// Publier avec ACK
	ack := sb.Bus.PublishWithAck(tempTopic, data, timeout)

	// Nettoyer après timeout
	go func() {
		time.Sleep(timeout + time.Second)
		sb.Bus.unsubscribeWebSocket(clientID, tempTopic)
	}()

	return ack
}

func (sb *ServerBus) PublishToServer(addr string, data any, secure ...bool) error {
	return sb.PublishToServerWithFrom(addr, data, "", secure...)
}

func (sb *ServerBus) PublishToServerWithFrom(addr string, data any, fromClient string, secure ...bool) error {
	// Créer un client pour se connecter au serveur distant
	address := addr
	path := ""
	if u, err := url.Parse(addr); err == nil && u.Host != "" {
		// Si l'adresse est une URL complète ou contient un path
		address = u.Host
		path = u.Path
	} else if u, err := url.Parse("ws://" + addr); err == nil {
		// Tenter de parser comme "host/path" sans scheme
		address = u.Host
		path = u.Path
	}

	client, err := NewClient(ClientConnectOptions{
		Id:      sb.ID + "-to-" + addr,
		Address: address,
		Path:    path,
		Secure:  len(secure) > 0 && secure[0],
	})
	if err != nil {
		return err
	}
	defer client.Close()

	// Préparer le message avec fromServer et fromClient
	message := map[string]any{
		"data":       data,
		"fromServer": sb.ID,
	}
	if fromClient != "" {
		message["fromClient"] = fromClient
	}

	// Publier vers le serveur distant
	client.PublishToID("server", message)

	// ATTENDRE que le message soit envoyé avant de Close (puisque c'est une connexion éphémère)
	timeout := time.After(3 * time.Second)
drain:
	for {
		select {
		case <-timeout:
			break drain
		default:
			if len(client.messageQueue) == 0 {
				time.Sleep(50 * time.Millisecond) // Un petit peu plus pour l'OS
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

func (sb *ServerBus) Stop() {
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
			var m map[string]any
			err := conn.ReadJSON(&m)
			if err != nil {
				lg.DebugC("WebSocket read error:", "error", err.Error())
				break
			}

			// Traiter les callbacks utilisateur
			for _, callback := range sb.onServerData {
				callback(m, conn)
			}

			// Traiter les actions du bus
			sb.handleBusActions(m, conn, &clientID)
		}
	}
}

// handleBusActions - Gère les actions du bus WebSocket
func (sb *ServerBus) handleBusActions(data map[string]any, conn *ws.Conn, clientID *string) {
	action, ok := data["action"].(string)
	if !ok {
		sb.sendError(conn, *clientID, "missing or invalid action")
		return
	}

	switch action {
	case "ping":
		sb.handlePing(data, conn, clientID)

	case "subscribe":
		sb.handleSubscribe(data, conn, *clientID)

	case "unsubscribe":
		sb.handleUnsubscribe(data, conn, *clientID)

	case "publish":
		sb.handlePublish(data, conn, *clientID)

	case "direct_message":
		sb.handleDirectMessage(data, conn, *clientID)

	case "publish_to_server":
		sb.handlePublishToServer(data, conn, *clientID)

	case "ack":
		sb.handleAck(data, conn, *clientID)

	case "publish_with_ack":
		sb.handlePublishWithAck(data, conn, *clientID)

	case "publish_to_id_with_ack":
		sb.handlePublishToIDWithAck(data, conn, *clientID)

	case "get_ack_status":
		sb.handleGetAckStatus(data, conn, *clientID)

	case "cancel_ack":
		sb.handleCancelAck(data, conn, *clientID)

	default:
		sb.sendError(conn, *clientID, "unknown action: "+action)
	}
}

// handlePing - Gère l'enregistrement du client
func (sb *ServerBus) handlePing(data map[string]any, conn *ws.Conn, clientID *string) {
	from, ok := data["from"].(string)
	if !ok || from == "" {
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
	sb.sendResponse(conn, *clientID, map[string]any{
		"action": "pong",
		"id":     *clientID,
	})
}

// handleDirectMessage - Gère les messages directs vers un ID
func (sb *ServerBus) handleDirectMessage(data map[string]any, conn *ws.Conn, clientID string) {
	if clientID == "" {
		sb.sendError(conn, clientID, "client not registered")
		return
	}

	targetID, ok := data["to"].(string)
	if !ok || targetID == "" {
		sb.sendError(conn, clientID, "missing target ID")
		return
	}

	payload := data["data"]

	// Si le message est pour le serveur
	if targetID == sb.ID || targetID == "server" {
		if sb.onId != nil {
			sb.onId(payload)
		}
		return
	}

	// Sinon, transférer vers le client cible
	sb.PublishToID(targetID, payload)
}

// handlePublishToServer - Gère les demandes de publication vers un serveur distant
func (sb *ServerBus) handlePublishToServer(data map[string]any, conn *ws.Conn, clientID string) {
	if clientID == "" {
		sb.sendError(conn, clientID, "client not registered")
		return
	}

	targetAddr, ok := data["to"].(string)
	if !ok || targetAddr == "" {
		sb.sendError(conn, clientID, "missing target server address")
		return
	}

	payload := data["data"]
	fromClient := data["from"].(string)

	// Relayer vers le serveur distant avec fromServer
	err := sb.PublishToServerWithFrom(targetAddr, payload, fromClient)
	if err != nil {
		sb.sendError(conn, clientID, "failed to relay to server: "+err.Error())
		return
	}

	// Confirmer l'envoi
	sb.sendResponse(conn, clientID, map[string]any{
		"action": "server_message_sent",
		"to":     targetAddr,
	})
}

// handleAck - Gère les acknowledgments reçus
func (sb *ServerBus) handleAck(data map[string]any, conn *ws.Conn, clientID string) {
	if clientID == "" {
		sb.sendError(conn, clientID, "client not registered")
		return
	}

	// Les données ACK sont dans le champ "data"
	ackData, ok := data["data"].(map[string]any)
	if !ok {
		sb.sendError(conn, clientID, "missing ack data")
		return
	}

	ackID, ok := ackData["ack_id"].(string)
	if !ok || ackID == "" {
		sb.sendError(conn, clientID, "missing ack_id")
		return
	}

	success, _ := ackData["success"].(bool)
	errorMsg, _ := ackData["error"].(string)

	// Créer la réponse ACK
	ackResp := ackResponse{
		AckID:    ackID,
		ClientID: clientID,
		Success:  success,
		Error:    errorMsg,
	}

	// Transmettre au bus
	sb.Bus.handleAck(ackResp)
}

// handleSubscribe - Gère la souscription WebSocket
func (sb *ServerBus) handleSubscribe(data map[string]any, conn *ws.Conn, clientID string) {
	if clientID == "" {
		sb.sendError(conn, clientID, "client not registered, send ping first")
		return
	}

	topic, ok := data["topic"].(string)
	if !ok || topic == "" {
		sb.sendError(conn, clientID, "missing or invalid topic")
		return
	}

	// Souscrire via le bus unifié
	sb.Bus.subscribeWebSocket(clientID, topic, conn)

	// Confirmer la souscription
	sb.sendResponse(conn, clientID, map[string]any{
		"action": "subscribed",
		"topic":  topic,
	})
}

// handleUnsubscribe - Gère le désabonnement WebSocket
func (sb *ServerBus) handleUnsubscribe(data map[string]any, conn *ws.Conn, clientID string) {
	if clientID == "" {
		sb.sendError(conn, clientID, "client not registered")
		return
	}

	topic, ok := data["topic"].(string)
	if !ok || topic == "" {
		sb.sendError(conn, clientID, "missing or invalid topic")
		return
	}

	// Désabonner via le bus unifié
	sb.Bus.unsubscribeWebSocket(clientID, topic)

	// Confirmer le désabonnement
	sb.sendResponse(conn, clientID, map[string]any{
		"action": "unsubscribed",
		"topic":  topic,
	})
}

// handlePublish - Gère la publication WebSocket
func (sb *ServerBus) handlePublish(data map[string]any, conn *ws.Conn, clientID string) {
	if clientID == "" {
		sb.sendError(conn, clientID, "client not registered")
		return
	}

	topic, ok := data["topic"].(string)
	if !ok || topic == "" {
		sb.sendError(conn, clientID, "missing or invalid topic")
		return
	}

	payload := data["data"]

	// Publier via le bus unifié (va notifier TOUS les subscribers)
	sb.Bus.Publish(topic, payload)

	// Optionnel: confirmer la publication
	sb.sendResponse(conn, clientID, map[string]any{
		"action": "published",
		"topic":  topic,
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
func (sb *ServerBus) sendResponse(conn *ws.Conn, clientID string, data map[string]any) {
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
		msg := wsMessage{
			Action: data["action"].(string),
			Data:   data,
		}
		if topic, ok := data["topic"].(string); ok {
			msg.Topic = topic
		}
		sb.Bus.sendToWebSocket(wsConn, msg)
	} else {
		// Fallback si pas encore dans le registre du bus
		_ = conn.WriteJSON(data)
	}
}

// sendError - Envoie une erreur JSON de manière sécurisée
func (sb *ServerBus) sendError(conn *ws.Conn, clientID string, message string) {
	sb.sendResponse(conn, clientID, map[string]any{
		"action": "error",
		"error":  message,
	})
}

// handlePublishWithAck - Gère les demandes de publication avec ACK du client
func (sb *ServerBus) handlePublishWithAck(data map[string]any, conn *ws.Conn, clientID string) {
	if clientID == "" {
		sb.sendError(conn, clientID, "client not registered")
		return
	}

	topic, ok := data["topic"].(string)
	if !ok || topic == "" {
		sb.sendError(conn, clientID, "missing or invalid topic")
		return
	}

	ackID, ok := data["ack_id"].(string)
	if !ok || ackID == "" {
		sb.sendError(conn, clientID, "missing ack_id")
		return
	}

	payload := data["data"]

	// Publier avec ACK via le bus
	ack := sb.Bus.PublishWithAck(topic, payload, 30*time.Second) // Timeout par défaut

	// Attendre les réponses et les renvoyer au client
	go func() {
		responses := ack.Wait()
		sb.sendResponse(conn, clientID, map[string]any{
			"action":    "ack_response",
			"ack_id":    ackID,
			"responses": responses,
		})
	}()
}

// handlePublishToIDWithAck - Gère les demandes de message direct avec ACK
func (sb *ServerBus) handlePublishToIDWithAck(data map[string]any, conn *ws.Conn, clientID string) {
	if clientID == "" {
		sb.sendError(conn, clientID, "client not registered")
		return
	}

	targetID, ok := data["to"].(string)
	if !ok || targetID == "" {
		sb.sendError(conn, clientID, "missing target ID")
		return
	}

	ackID, ok := data["ack_id"].(string)
	if !ok || ackID == "" {
		sb.sendError(conn, clientID, "missing ack_id")
		return
	}

	payload := data["data"]

	// Publier avec ACK via le bus
	ack := sb.PublishToIDWithAck(targetID, payload, 30*time.Second)

	// Attendre les réponses et les renvoyer au client
	go func() {
		responses := ack.Wait()
		sb.sendResponse(conn, clientID, map[string]any{
			"action":    "ack_response",
			"ack_id":    ackID,
			"responses": responses,
		})
	}()
}

// handleGetAckStatus - Gère les demandes de statut ACK
func (sb *ServerBus) handleGetAckStatus(data map[string]any, conn *ws.Conn, clientID string) {
	if clientID == "" {
		sb.sendError(conn, clientID, "client not registered")
		return
	}

	ackID, ok := data["ack_id"].(string)
	if !ok || ackID == "" {
		sb.sendError(conn, clientID, "missing ack_id")
		return
	}

	// Chercher l'ACK dans le bus
	sb.Bus.ackMu.RLock()
	ackReq := sb.Bus.ackRequests[ackID]
	sb.Bus.ackMu.RUnlock()

	if ackReq == nil {
		sb.sendResponse(conn, clientID, map[string]any{
			"action": "ack_status",
			"ack_id": ackID,
			"status": make(map[string]bool),
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

	sb.sendResponse(conn, clientID, map[string]any{
		"action": "ack_status",
		"ack_id": ackID,
		"status": status,
	})
}

// handleCancelAck - Gère les demandes d'annulation ACK
func (sb *ServerBus) handleCancelAck(data map[string]any, conn *ws.Conn, clientID string) {
	if clientID == "" {
		sb.sendError(conn, clientID, "client not registered")
		return
	}

	ackID, ok := data["ack_id"].(string)
	if !ok || ackID == "" {
		sb.sendError(conn, clientID, "missing ack_id")
		return
	}

	// Chercher et annuler l'ACK dans le bus
	sb.Bus.cancelAck(ackID)

	// Confirmer l'annulation
	sb.sendResponse(conn, clientID, map[string]any{
		"action": "ack_cancelled",
		"ack_id": ackID,
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
