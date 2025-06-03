/**
 * UltraFast PubSub Client - Compatible navigateur
 * Inspiré de kactor mais optimisé pour la performance maximale
 */
class PubSubClient {
    constructor(options = {}) {
        this.url = options.url || `ws://${window.location.host}/ws`;
        this.protocols = options.protocols || ['pubsub'];
        this.autoReconnect = options.autoReconnect !== false;
        this.reconnectInterval = options.reconnectInterval || 3000;
        this.maxReconnectAttempts = options.maxReconnectAttempts || 10;
        
        // ID unique du client (auto-généré ou fourni)
        this.clientId = options.clientId || `js-client-${Date.now()}-${Math.random().toString(36).substring(2, 9)}`;
        
        this.ws = null;
        this.isConnected = false;
        this.reconnectAttempts = 0;
        this.messageQueue = [];
        this.subscriptions = new Map();
        this.messageId = 0;
        this.pendingResponses = new Map();
        this.pendingAcks = new Map();
        this.pendingDirectResponses = new Map(); // Nouveau: pour les réponses directes
        
        // Callbacks
        this.onConnect = options.onConnect || (() => {});
        this.onDisconnect = options.onDisconnect || (() => {});
        this.onError = options.onError || ((error) => console.error('PubSub Error:', error));
        this.onMessage = options.onMessage || (() => {});
        this.onIDMessage = options.onIDMessage || (() => {}); // Nouveau: callback pour messages directs
        
        // Optimisations performance
        this.batchSize = options.batchSize || 100;
        this.batchTimeout = options.batchTimeout || 10;
        this.messageBatch = [];
        this.batchTimer = null;
        
        this.connect();
    }
    
    connect() {
        try {
            this.ws = new WebSocket(this.url, this.protocols);
            this.setupEventHandlers();
        } catch (error) {
            this.onError(error);
            this.attemptReconnect();
        }
    }
    
    setupEventHandlers() {
        this.ws.onopen = () => {
            this.isConnected = true;
            this.reconnectAttempts = 0;
            console.log('PubSub connected');
            
            // Renvoyer les abonnements
            this.subscriptions.forEach((callback, topic) => {
                this.sendMessage({ topic: 'subscribe', data: topic });
            });
            
            // Vider la queue
            while (this.messageQueue.length > 0) {
                const message = this.messageQueue.shift();
                this.sendMessage(message);
            }
            
            this.onConnect();
        };
        
        this.ws.onclose = (event) => {
            this.isConnected = false;
            console.log('PubSub disconnected');
            this.onDisconnect(event);
            
            if (this.autoReconnect && this.reconnectAttempts < this.maxReconnectAttempts) {
                this.attemptReconnect();
            }
        };
        
        this.ws.onerror = (error) => {
            console.error('PubSub WebSocket error:', error);
            this.onError(error);
        };
        
        this.ws.onmessage = (event) => {
            try {
                const message = JSON.parse(event.data);
                this.handleMessage(message);
            } catch (error) {
                console.error('Error parsing message:', error);
                this.onError(error);
            }
        };
    }
    
    handleMessage(message) {
        // Si le message nécessite un ACK, l'envoyer automatiquement
        if (message.needsAck && message.id) {
            this.sendAutoAck(message);
        }
        
        // Gérer les messages directs
        if (message.topic === '__direct__') {
            if (message.data && message.data.fromID && message.data.data !== undefined) {
                this.onIDMessage(message.data.fromID, message.data.data, message);
                
                // Si c'est une réponse à une requête directe
                if (message.data.replyToID && this.pendingDirectResponses.has(message.data.replyToID)) {
                    const resolver = this.pendingDirectResponses.get(message.data.replyToID);
                    resolver(message.data.data);
                    this.pendingDirectResponses.delete(message.data.replyToID);
                }
                return;
            }
        }
        
        // Vérifier si c'est un ACK
        if (message.isAck && message.ackFor) {
            this.handleAck(message);
            return;
        }
        
        // Appeler le callback global
        this.onMessage(message);
        
        // Appeler le callback spécifique au topic
        const callback = this.subscriptions.get(message.topic);
        if (callback) {
            callback(message.data, message);
        }
        
        // Gérer les réponses en attente (pour publishAndWait)
        if (message.id && this.pendingResponses.has(message.id)) {
            const resolver = this.pendingResponses.get(message.id);
            resolver(message);
            this.pendingResponses.delete(message.id);
        }
    }
    
    // Envoie automatiquement un ACK pour un message reçu
    sendAutoAck(originalMessage) {
        const ackMessage = {
            topic: originalMessage.topic,
            data: {
                status: 'received',
                timestamp: Date.now(),
                clientId: this.clientId
            },
            id: ++this.messageId,
            isAck: true,
            ackFor: originalMessage.id,
            from: `client-js-${this.clientId}`
        };
        
        this.sendMessage(ackMessage);
    }
    
    // Gère la réception d'un ACK
    handleAck(ackMessage) {
        if (this.pendingAcks.has(ackMessage.ackFor)) {
            const ackCollector = this.pendingAcks.get(ackMessage.ackFor);
            ackCollector.receivedAcks++;
            ackCollector.responses.push(ackMessage);
            
            // Si tous les ACKs sont reçus, résoudre la promesse
            if (ackCollector.receivedAcks >= ackCollector.expectedAcks) {
                ackCollector.resolve(ackCollector.responses);
                this.pendingAcks.delete(ackMessage.ackFor);
            }
        }
    }
    
    sendMessage(message) {
        if (!this.isConnected) {
            this.messageQueue.push(message);
            return;
        }
        
        try {
            this.ws.send(JSON.stringify(message));
        } catch (error) {
            console.error('Error sending message:', error);
            this.onError(error);
        }
    }
    
    // API simple comme kactor
    subscribe(topic, callback) {
        // Créer la fonction de désabonnement
        const unsubscribeFunc = () => this.unsubscribe(topic);
        
        // Créer l'objet subscription
        const subscription = {
            topic: topic,
            unsubscribe: unsubscribeFunc
        };
        
        // Wrapper pour adapter l'ancienne signature à la nouvelle
        const wrappedCallback = (data, message) => {
            callback(data, subscription);
        };
        
        this.subscriptions.set(topic, wrappedCallback);
        this.sendMessage({ topic: 'subscribe', data: topic });
        
        // Retourner une fonction pour se désabonner
        return unsubscribeFunc;
    }
    
    // S'abonner à un topic avec callback pour messages directs
    subscribeWithID(topic, callback, onIDMessage = null) {
        // Créer la fonction de désabonnement
        const unsubscribeFunc = () => this.unsubscribe(topic);
        
        // Créer l'objet subscription
        const subscription = {
            topic: topic,
            unsubscribe: unsubscribeFunc
        };
        
        // Wrapper pour adapter l'ancienne signature à la nouvelle
        const wrappedCallback = (data, message) => {
            callback(data, subscription);
        };
        
        this.subscriptions.set(topic, wrappedCallback);
        
        // Configurer le callback pour messages directs si fourni
        if (onIDMessage) {
            this.onIDMessage = onIDMessage;
        }
        
        this.sendMessage({ topic: 'subscribe', data: topic });
        
        // Retourner une fonction pour se désabonner
        return unsubscribeFunc;
    }
    
    unsubscribe(topic) {
        this.subscriptions.delete(topic);
        this.sendMessage({ topic: 'unsubscribe', data: topic });
    }
    
    publish(topic, data) {
        this.sendMessage({ 
            topic, 
            data,
            from: this.clientId // Inclure automatiquement l'ID du client
        });
    }
    
    // Publier un message directement à un client/serveur par ID
    publishToID(targetID, data) {
        this.sendMessage({
            topic: '__direct__',
            data: {
                targetID: targetID,
                data: data
            },
            from: this.clientId
        });
    }
    
    // Publier un message directement à un client et attendre une réponse
    publishToIDAndWait(targetID, data, timeout = 5000) {
        return new Promise((resolve, reject) => {
            const messageId = ++this.messageId;
            
            // Stocker le resolver pour la réponse
            this.pendingDirectResponses.set(messageId, resolve);
            
            // Envoyer le message avec demande de réponse
            this.sendMessage({
                topic: '__direct__',
                data: {
                    targetID: targetID,
                    data: data,
                    needsReply: true,
                    replyToID: messageId
                },
                id: messageId,
                from: this.clientId
            });
            
            // Timeout
            setTimeout(() => {
                if (this.pendingDirectResponses.has(messageId)) {
                    this.pendingDirectResponses.delete(messageId);
                    reject(new Error(`Timeout: aucune réponse de ${targetID} après ${timeout}ms`));
                }
            }, timeout);
        });
    }
    
    // Publish avec promesse de réponse
    publishAndWait(topic, data, timeout = 5000) {
        return new Promise((resolve, reject) => {
            const messageId = ++this.messageId;
            const message = { topic, data, id: messageId };
            
            this.pendingResponses.set(messageId, resolve);
            this.sendMessage(message);
            
            // Timeout
            setTimeout(() => {
                if (this.pendingResponses.has(messageId)) {
                    this.pendingResponses.delete(messageId);
                    reject(new Error('Message timeout'));
                }
            }, timeout);
        });
    }
    
    // Publish avec attente d'ACKs de tous les abonnés
    publishAndWaitForAcks(topic, data, expectedAcks = null, timeout = 5000) {
        return new Promise((resolve, reject) => {
            const messageId = ++this.messageId;
            const message = { 
                topic, 
                data, 
                id: messageId,
                needsAck: true,
                from: `client-js-${this.clientId}`
            };
            
            // Si expectedAcks n'est pas spécifié, on ne peut pas savoir combien attendre
            // Le serveur devra nous dire combien d'abonnés il y a
            const ackCollector = {
                messageId: messageId,
                expectedAcks: expectedAcks || 999, // Valeur par défaut élevée
                receivedAcks: 0,
                responses: [],
                resolve: resolve,
                reject: reject
            };
            
            this.pendingAcks.set(messageId, ackCollector);
            this.sendMessage(message);
            
            // Timeout
            setTimeout(() => {
                if (this.pendingAcks.has(messageId)) {
                    const collector = this.pendingAcks.get(messageId);
                    this.pendingAcks.delete(messageId);
                    reject(new Error(`ACK timeout: received ${collector.receivedAcks}/${collector.expectedAcks} ACKs`));
                }
            }, timeout);
        });
    }
    
    // Publier vers un serveur distant
    publishToServer(serverUrl, secure = false, topic, data) {
        return new Promise((resolve, reject) => {
            // Construire l'URL WebSocket
            const protocol = secure ? 'wss' : 'ws';
            const wsUrl = `${protocol}://${serverUrl}/ws`;
            
            // Créer une connexion temporaire
            const tempWs = new WebSocket(wsUrl, ['pubsub']);
            
            tempWs.onopen = () => {
                // Publier le message
                const message = {
                    topic: topic,
                    data: data,
                    id: ++this.messageId,
                    from: `client-js-${this.clientId || 'unknown'}`
                };
                
                tempWs.send(JSON.stringify(message));
                
                // Attendre un peu puis fermer
                setTimeout(() => {
                    tempWs.close();
                    resolve();
                    console.log(`Message publié vers serveur ${serverUrl} sur topic ${topic}`);
                }, 100);
            };
            
            tempWs.onerror = (error) => {
                reject(new Error(`Erreur connexion vers ${serverUrl}: ${error.message}`));
            };
            
            tempWs.onclose = (event) => {
                if (event.code !== 1000) { // 1000 = fermeture normale
                    reject(new Error(`Connexion fermée anormalement vers ${serverUrl}: ${event.code}`));
                }
            };
            
            // Timeout de connexion
            setTimeout(() => {
                if (tempWs.readyState === WebSocket.CONNECTING) {
                    tempWs.close();
                    reject(new Error(`Timeout de connexion vers ${serverUrl}`));
                }
            }, 5000);
        });
    }

    // Publier vers un serveur distant et attendre une réponse
    publishToServerAndWait(serverUrl, secure = false, topic, data, timeout = 5000) {
        return new Promise((resolve, reject) => {
            // Construire l'URL WebSocket
            const protocol = secure ? 'wss' : 'ws';
            const wsUrl = `${protocol}://${serverUrl}/ws`;
            
            // Créer une connexion temporaire
            const tempWs = new WebSocket(wsUrl, ['pubsub']);
            let responseReceived = false;
            
            tempWs.onopen = () => {
                // S'abonner au topic pour recevoir la réponse
                const subscribeMessage = {
                    topic: 'subscribe',
                    data: topic
                };
                tempWs.send(JSON.stringify(subscribeMessage));
                
                // Attendre un peu puis publier
                setTimeout(() => {
                    const message = {
                        topic: topic,
                        data: data,
                        id: ++this.messageId,
                        from: `client-js-${this.clientId || 'unknown'}`
                    };
                    
                    tempWs.send(JSON.stringify(message));
                }, 100);
            };
            
            tempWs.onmessage = (event) => {
                try {
                    const message = JSON.parse(event.data);
                    if (message.topic === topic && !responseReceived) {
                        responseReceived = true;
                        tempWs.close();
                        resolve(message);
                        console.log(`Réponse reçue du serveur ${serverUrl} sur topic ${topic}`);
                    }
                } catch (error) {
                    console.error('Erreur parsing réponse:', error);
                }
            };
            
            tempWs.onerror = (error) => {
                reject(new Error(`Erreur connexion vers ${serverUrl}: ${error.message}`));
            };
            
            tempWs.onclose = (event) => {
                if (event.code !== 1000 && !responseReceived) {
                    reject(new Error(`Connexion fermée anormalement vers ${serverUrl}: ${event.code}`));
                }
            };
            
            // Timeout global
            setTimeout(() => {
                if (!responseReceived) {
                    tempWs.close();
                    reject(new Error(`Timeout: aucune réponse du serveur ${serverUrl} après ${timeout}ms`));
                }
            }, timeout);
        });
    }

    // Publier vers plusieurs serveurs en parallèle
    publishToMultipleServers(servers, secure = false, topic, data) {
        const promises = servers.map(serverUrl => 
            this.publishToServer(serverUrl, secure, topic, data)
                .then(() => ({ server: serverUrl, success: true, error: null }))
                .catch(error => ({ server: serverUrl, success: false, error: error.message }))
        );
        
        return Promise.all(promises);
    }

    // Publier vers plusieurs serveurs et attendre toutes les réponses
    publishToMultipleServersAndWait(servers, secure = false, topic, data, timeout = 5000) {
        const promises = servers.map(serverUrl => 
            this.publishToServerAndWait(serverUrl, secure, topic, data, timeout)
                .then(response => ({ server: serverUrl, success: true, response: response, error: null }))
                .catch(error => ({ server: serverUrl, success: false, response: null, error: error.message }))
        );
        
        return Promise.all(promises);
    }
    
    // Batch publishing pour de meilleures performances
    publishBatch(messages) {
        if (!Array.isArray(messages)) {
            throw new Error('Messages must be an array');
        }
        
        // Optimisation: envoyer par lots
        for (let i = 0; i < messages.length; i += this.batchSize) {
            const batch = messages.slice(i, i + this.batchSize);
            batch.forEach(({ topic, data }) => {
                this.publish(topic, data);
            });
        }
    }
    
    attemptReconnect() {
        if (this.reconnectAttempts >= this.maxReconnectAttempts) {
            console.error('Max reconnection attempts reached');
            return;
        }
        
        this.reconnectAttempts++;
        console.log(`Attempting to reconnect... (${this.reconnectAttempts}/${this.maxReconnectAttempts})`);
        
        setTimeout(() => {
            this.connect();
        }, this.reconnectInterval);
    }
    
    disconnect() {
        this.autoReconnect = false;
        if (this.ws) {
            this.ws.close();
        }
    }
    
    // Méthodes utilitaires
    isReady() {
        return this.isConnected && this.ws.readyState === WebSocket.OPEN;
    }
    
    // Retourne l'ID unique du client
    getClientId() {
        return this.clientId;
    }
    
    getStats() {
        return {
            connected: this.isConnected,
            reconnectAttempts: this.reconnectAttempts,
            subscriptions: this.subscriptions.size,
            pendingMessages: this.messageQueue.length,
            pendingResponses: this.pendingResponses.size
        };
    }
}

// Fonction helper pour une utilisation encore plus simple
function createPubSubClient(url, options = {}) {
    return new PubSubClient({ url, ...options });
}

// Exports pour différents environnements
if (typeof module !== 'undefined' && module.exports) {
    // Node.js
    module.exports = { PubSubClient, createPubSubClient };
} else if (typeof window !== 'undefined') {
    // Browser
    window.PubSubClient = PubSubClient;
    window.createPubSubClient = createPubSubClient;
}

/* 
Exemple d'utilisation dans le navigateur:

// Connexion simple
const client = new PubSubClient({
    url: 'ws://localhost:8080/ws',
    onConnect: () => console.log('Connecté!'),
    onError: (error) => console.error('Erreur:', error)
});

// S'abonner à un topic
const unsubscribe = client.subscribe('chat', (data, message) => {
    console.log('Message reçu:', data);
});

// Publier un message
client.publish('chat', {
    user: 'Alice',
    message: 'Salut tout le monde!',
    timestamp: Date.now()
});

// Publier et attendre une réponse
client.publishAndWait('request', { action: 'getData' })
    .then(response => console.log('Réponse:', response))
    .catch(error => console.error('Timeout:', error));

// Publier avec ACKs automatiques
client.publishAndWaitForAcks('notifications', { message: 'Important!' }, 2, 3000)
    .then(acks => console.log('ACKs reçus:', acks))
    .catch(error => console.error('ACK timeout:', error));

// ===== NOUVELLES FONCTIONNALITÉS: PUBLICATION VERS SERVEURS DISTANTS =====

// Publier vers un serveur distant
client.publishToServer('localhost:8082', false, 'cross-server', {
    message: 'Hello from another server!',
    timestamp: Date.now()
})
.then(() => console.log('Message envoyé vers serveur distant'))
.catch(error => console.error('Erreur publication distante:', error));

// Publier vers un serveur distant et attendre une réponse
client.publishToServerAndWait('localhost:8082', false, 'request', {
    action: 'getRemoteData',
    params: { id: 123 }
}, 5000)
.then(response => console.log('Réponse du serveur distant:', response))
.catch(error => console.error('Timeout serveur distant:', error));

// Publier vers plusieurs serveurs en parallèle
const servers = ['localhost:8082', 'localhost:8083', 'localhost:8084'];
client.publishToMultipleServers(servers, false, 'broadcast', {
    message: 'Message diffusé à tous les serveurs',
    from: 'client-browser'
})
.then(results => {
    results.forEach(result => {
        if (result.success) {
            console.log(`✅ Succès vers ${result.server}`);
        } else {
            console.log(`❌ Erreur vers ${result.server}: ${result.error}`);
        }
    });
});

// Publier vers plusieurs serveurs et attendre toutes les réponses
client.publishToMultipleServersAndWait(servers, false, 'query', {
    question: 'Quel est votre statut?'
}, 3000)
.then(results => {
    results.forEach(result => {
        if (result.success) {
            console.log(`✅ Réponse de ${result.server}:`, result.response.data);
        } else {
            console.log(`❌ Pas de réponse de ${result.server}: ${result.error}`);
        }
    });
});

// Publication sécurisée (HTTPS/WSS)
client.publishToServer('secure-server.com', true, 'secure-topic', {
    sensitiveData: 'encrypted-content'
})
.then(() => console.log('Message sécurisé envoyé'))
.catch(error => console.error('Erreur sécurisée:', error));

// Se désabonner
unsubscribe();
*/ 