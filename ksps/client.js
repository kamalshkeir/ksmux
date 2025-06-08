/**
 * Client WebSocket pour le système pub/sub
 * Traduction fidèle du client.go en JavaScript
 */

class ClientSubscription {
    constructor(topic, callback) {
        this.topic = topic;
        this.callback = callback;
        this.active = true;
    }
}

class ClientSubscriber {
    constructor(client, id = '', topic = '') {
        this.client = client;
        this.Id = id;
        this.Topic = topic;
        this.Ch = null; // Pas utilisé en JS
        this.Conn = null; // Référence WebSocket
    }

    Unsubscribe() {
        this.client.Unsubscribe(this.Topic);
    }
}

class ClientAck {
    constructor(id, client, timeout, cancelled = false) {
        this.ID = id;
        this.Client = client;
        this.timeout = timeout;
        this.cancelled = cancelled;
        this.responses = null;
        this.status = null;
        this.done = false;
        this.responsePromise = null;
        this.statusPromise = null;
    }

    /**
     * Attend tous les acknowledgments avec timeout
     * @returns {Promise<Object>} Map des réponses
     */
    async Wait() {
        if (this.cancelled || this.done) {
            return {};
        }

        return new Promise((resolve) => {
            const timeoutId = setTimeout(() => {
                this.Cancel();
                resolve({});
            }, this.timeout);

            this.responsePromise = (responses) => {
                clearTimeout(timeoutId);
                resolve(responses);
            };
        });
    }

    /**
     * Attend au moins un acknowledgment
     * @returns {Promise<{response: Object, success: boolean}>}
     */
    async WaitAny() {
        if (this.cancelled || this.done) {
            return { response: {}, success: false };
        }

        return new Promise((resolve) => {
            const timeoutId = setTimeout(() => {
                this.Cancel();
                resolve({ response: {}, success: false });
            }, this.timeout);

            this.responsePromise = (responses) => {
                clearTimeout(timeoutId);
                // Retourner le premier ACK reçu
                for (const [clientID, response] of Object.entries(responses)) {
                    resolve({ response: response, success: true });
                    return;
                }
                resolve({ response: {}, success: false });
            };
        });
    }

    /**
     * Retourne le statut actuel des acknowledgments
     * @returns {Promise<Object>} Map du statut
     */
    async GetStatus() {
        if (this.cancelled || this.done) {
            return {};
        }

        // Demander le statut au serveur
        this.Client.sendMessage({
            action: 'get_ack_status',
            ack_id: this.ID,
            from: this.Client.Id
        });

        return new Promise((resolve) => {
            const timeoutId = setTimeout(() => {
                resolve({});
            }, 2000); // 2 secondes de timeout

            this.statusPromise = (status) => {
                clearTimeout(timeoutId);
                resolve(status);
            };
        });
    }

    /**
     * Vérifie si tous les ACK sont reçus
     * @returns {Promise<boolean>}
     */
    async IsComplete() {
        const status = await this.GetStatus();
        for (const received of Object.values(status)) {
            if (!received) {
                return false;
            }
        }
        return Object.keys(status).length > 0;
    }

    /**
     * Annule l'attente des acknowledgments
     */
    Cancel() {
        if (this.cancelled || this.done) {
            return;
        }

        this.cancelled = true;
        this.done = true;

        // Envoyer la demande d'annulation au serveur
        this.Client.sendMessage({
            action: 'cancel_ack',
            ack_id: this.ID,
            from: this.Client.Id
        });

        // Nettoyer localement
        if (this.Client.ackRequests) {
            this.Client.ackRequests.delete(this.ID);
        }
    }

    /**
     * Traite une réponse ACK du serveur
     * @param {Object} responses - Réponses reçues
     */
    handleResponse(responses) {
        if (this.responsePromise) {
            this.responsePromise(responses);
            this.responsePromise = null;
        }
        this.responses = responses;
    }

    /**
     * Traite un statut ACK du serveur
     * @param {Object} status - Statut reçu
     */
    handleStatus(status) {
        if (this.statusPromise) {
            this.statusPromise(status);
            this.statusPromise = null;
        }
        this.status = status;
    }
}

class Client {
    constructor() {
        this.Id = '';
        this.Address = '';
        this.Path = '';
        this.Secure = '';
        this.onDataWS = null;
        this.onId = null;
        this.onClose = null;
        this.RestartEvery = 10000; // 10 secondes en ms
        this.Conn = null;
        this.Autorestart = false;
        this.Done = false;

        // Optimisations pour pub/sub
        this.subscriptions = new Map(); // topic -> ClientSubscription
        this.messageQueue = [];
        this.connected = false;
        this.messageQueueProcessing = false;
    }

    /**
     * Crée un nouveau client avec les options spécifiées
     * @param {Object} opts - Options de connexion
     * @returns {Promise<Client>}
     */
    static async NewClient(opts) {
        if (opts.Autorestart && !opts.RestartEvery) {
            opts.RestartEvery = 10000; // 10 secondes
        }
        if (!opts.OnDataWs) {
            opts.OnDataWs = (data, conn) => Promise.resolve();
        }

        const client = new Client();
        client.Id = opts.Id || this.generateID();
        client.Address = opts.Address || window.location.host;
        client.Path = opts.Path || '/ws/bus';
        client.Secure = opts.Secure || false;
        client.Autorestart = opts.Autorestart || false;
        client.RestartEvery = opts.RestartEvery || 10000;
        client.onDataWS = opts.OnDataWs;
        client.onId = opts.OnId;
        client.onClose = opts.OnClose;
        await client.connect(opts);
        this.client=client;
        return client;
    }

    /**
     * Se connecte au serveur WebSocket
     * @param {Object} opts - Options de connexion
     */
    async connect(opts) {
        const scheme = this.Secure ? 'wss' : 'ws';
        const path = this.Path;
        const url = `${scheme}://${this.Address}${path}`;

        try {
            this.Conn = new WebSocket(url);
            
            // Promesse pour attendre la connexion
            await new Promise((resolve, reject) => {
                this.Conn.onopen = () => {
                    this.connected = true;
                    console.log(`client connected to ${url}`);
                    
                    // Démarrer les handlers
                    this.messageHandler();
                    this.messageSender();
                    
                    // Ping initial pour enregistrer le client
                    this.sendMessage({
                        action: 'ping',
                        from: this.Id
                    });
                    
                    resolve();
                };

                this.Conn.onerror = (error) => {
                    if (this.Autorestart) {
                        console.log(`Connection failed, retrying in ${this.RestartEvery/1000} seconds`);
                        setTimeout(() => {
                            this.connect(opts).then(resolve).catch(reject);
                        }, this.RestartEvery);
                    } else {
                        reject(error);
                    }
                };

                this.Conn.onclose = (event) => {
                    this.connected = false;
                    if (this.Autorestart && !this.Done) {
                        setTimeout(() => {
                            this.reconnect(opts);
                        }, this.RestartEvery);
                    }
                };
            });

        } catch (error) {
            if (this.Autorestart) {
                console.log(`Connection failed, retrying in ${this.RestartEvery/1000} seconds`);
                setTimeout(() => {
                    return this.connect(opts);
                }, this.RestartEvery);
            } else {
                throw error;
            }
        }
    }

    /**
     * Gère les messages entrants
     */
    messageHandler() {
        if (!this.Conn) return;

        this.Conn.onmessage = (event) => {
            if (this.Done || !this.connected) return;

            try {
                const data = JSON.parse(event.data);
                this.handleMessage(data);
            } catch (error) {
                console.error('WebSocket read error:', error);
                this.connected = false;
                if (this.Autorestart) {
                    this.reconnect();
                }
            }
        };

        this.Conn.onerror = (error) => {
            console.error('WebSocket read error:', error);
            this.connected = false;
            if (this.Autorestart) {
                this.reconnect();
            }
        };
    }

    /**
     * Gère l'envoi des messages en queue
     */
    async messageSender() {
        if (this.messageQueueProcessing) return;
        this.messageQueueProcessing = true;

        while (!this.Done) {
            if (this.messageQueue.length === 0) {
                await this.sleep(10); // Attendre 10ms
                continue;
            }

            const msg = this.messageQueue.shift();
            if (!msg) continue;

            if (!this.connected || !this.Conn) {
                continue;
            }

            try {
                this.Conn.send(JSON.stringify(msg));
            } catch (error) {
                console.error('WebSocket write error:', error);
                this.connected = false;
                break;
            }
        }

        this.messageQueueProcessing = false;
    }

    /**
     * Traite les messages reçus du serveur
     * @param {Object} data - Données du message
     */
    async handleMessage(data) {
        const action = data.action;
        if (!action) return;

        switch (action) {
            case 'pong':
                // Confirmation de connexion
                if (this.onId) {
                    this.onId(data, new ClientSubscriber(this));
                }
                break;

            case 'publish':
                // Message publié sur un topic
                this.handlePublishMessage(data);
                break;

            case 'publish_ack':
                // Message publié avec demande d'ACK
                this.handlePublishAckMessage(data);
                break;

            case 'direct_message':
                // Message direct vers ce client
                if (this.onId) {
                    const payload = data.data;
                    this.onId({ data: payload }, new ClientSubscriber(this));
                }
                break;

            case 'subscribed':
            case 'unsubscribed':
            case 'published':
                // Confirmations - on peut les ignorer ou les logger
                console.debug('Received confirmation:', action);
                break;

            case 'error':
                // Erreur du serveur
                if (data.error) {
                    console.error('Server error:', data.error);
                }
                break;

            case 'ack_response':
                // Réponse ACK du serveur
                this.handleAckResponse(data);
                break;

            case 'ack_status':
                // Statut ACK du serveur
                this.handleAckStatus(data);
                break;

            case 'ack_cancelled':
                // Confirmation d'annulation ACK
                this.handleAckCancelled(data);
                break;
        }

        // Callback utilisateur pour tous les messages
        if (this.onDataWS) {
            await this.onDataWS(data, this.Conn);
        }
    }

    /**
     * Traite les messages publiés
     * @param {Object} data - Données du message
     */
    handlePublishMessage(data) {
        const topic = data.topic;
        if (!topic) return;

        const payload = data.data;
        const sub = this.subscriptions.get(topic);

        if (sub && sub.active) {
            // Créer fonction d'unsubscribe
            const unsubFn = () => {
                this.Unsubscribe(topic);
            };

            // Exécuter le callback
            setTimeout(() => {
                sub.callback(payload, unsubFn);
            }, 0);
        }
    }

    /**
     * Traite les messages publiés avec ACK
     * @param {Object} data - Données du message
     */
    handlePublishAckMessage(data) {
        const topic = data.topic;
        if (!topic) return;

        const payload = data.data;
        const ackID = data.ack_id;
        const sub = this.subscriptions.get(topic);

        if (sub && sub.active) {
            // Créer fonction d'unsubscribe
            const unsubFn = () => {
                this.Unsubscribe(topic);
            };

            // Exécuter le callback avec gestion d'erreur
            setTimeout(async () => {
                let success = true;
                let errorMsg = '';

                try {
                    await sub.callback(payload, unsubFn);
                } catch (error) {
                    success = false;
                    errorMsg = error.message || error.toString();
                }

                // Envoyer ACK après traitement
                if (ackID) {
                    this.sendAck(ackID, success, errorMsg);
                }
            }, 0);
        } else if (ackID) {
            // Pas de subscriber, envoyer ACK d'erreur
            this.sendAck(ackID, false, 'no subscriber for topic');
        }
    }

    /**
     * Envoie un acknowledgment
     * @param {string} ackID - ID de l'ACK
     * @param {boolean} success - Succès du traitement
     * @param {string} errorMsg - Message d'erreur éventuel
     */
    sendAck(ackID, success, errorMsg = '') {
        const ackMsg = {
            action: 'ack',
            ack_id: ackID,
            from: this.Id,
            data: {
                ack_id: ackID,
                client_id: this.Id,
                success: success
            }
        };

        if (errorMsg) {
            ackMsg.data.error = errorMsg;
        }

        this.sendMessage(ackMsg);
    }

    /**
     * Souscription WebSocket optimisée
     * @param {string} topic - Topic à souscrire
     * @param {Function} callback - Callback à exécuter
     * @returns {Function} Fonction d'unsubscribe
     */
    Subscribe(topic, callback) {
        if (!this.connected) {
            console.debug('Cannot subscribe: client not connected');
            return () => {};
        }

        // Créer la subscription locale
        const sub = new ClientSubscription(topic, callback);
        this.subscriptions.set(topic, sub);

        // Envoyer la demande de souscription au serveur
        this.sendMessage({
            action: 'subscribe',
            topic: topic,
            from: this.Id
        });

        // Retourner fonction d'unsubscribe
        return () => {
            this.Unsubscribe(topic);
        };
    }

    /**
     * Désabonnement WebSocket
     * @param {string} topic - Topic à désabonner
     */
    Unsubscribe(topic) {
        const sub = this.subscriptions.get(topic);
        if (sub) {
            sub.active = false;
            this.subscriptions.delete(topic);
        }

        if (this.connected) {
            // Envoyer la demande de désabonnement au serveur
            this.sendMessage({
                action: 'unsubscribe',
                topic: topic,
                from: this.Id
            });
        }
    }

    /**
     * Publication WebSocket optimisée
     * @param {string} topic - Topic à publier
     * @param {any} data - Données à publier
     */
    Publish(topic, data) {
        if (!this.connected) {
            console.debug('Cannot publish: client not connected');
            return;
        }

        this.sendMessage({
            action: 'publish',
            topic: topic,
            data: data,
            from: this.Id
        });
    }

    /**
     * Envoi de message direct vers un ID (client ou serveur)
     * @param {string} targetID - ID de la cible
     * @param {any} data - Données à envoyer
     */
    PublishToID(targetID, data) {
        if (!this.connected) {
            console.debug('Cannot send direct message: client not connected');
            return;
        }

        this.sendMessage({
            action: 'direct_message',
            to: targetID,
            data: data,
            from: this.Id
        });
    }

    /**
     * Envoi de message vers un serveur distant via le serveur local
     * @param {string} addr - Adresse du serveur distant
     * @param {any} data - Données à envoyer
     * @param {boolean} secure - Utiliser HTTPS/WSS
     */
    PublishToServer(addr, data, secure = false) {
        if (!this.connected) {
            console.debug('Cannot send to server: client not connected');
            return;
        }

        this.sendMessage({
            action: 'publish_to_server',
            to: addr,
            data: data,
            from: this.Id
        });
    }

    /**
     * Publication avec acknowledgment via le serveur
     * @param {string} topic - Topic à publier
     * @param {any} data - Données à publier
     * @param {number} timeout - Timeout en millisecondes
     * @returns {ClientAck} Handle pour attendre les ACK
     */
    PublishWithAck(topic, data, timeout = 30000) {
        if (!this.connected) {
            console.debug('Cannot publish with ACK: client not connected');
            return new ClientAck('disconnected', this, timeout, true);
        }

        const ackID = this.generateAckID();
        const clientAck = new ClientAck(ackID, this, timeout);

        // Enregistrer l'ACK localement
        if (!this.ackRequests) {
            this.ackRequests = new Map();
        }
        this.ackRequests.set(ackID, clientAck);

        // Envoyer la demande au serveur
        this.sendMessage({
            action: 'publish_with_ack',
            topic: topic,
            data: data,
            ack_id: ackID,
            from: this.Id
        });

        return clientAck;
    }

    /**
     * Envoi de message direct avec acknowledgment
     * @param {string} targetID - ID de la cible
     * @param {any} data - Données à envoyer
     * @param {number} timeout - Timeout en millisecondes
     * @returns {ClientAck} Handle pour attendre les ACK
     */
    PublishToIDWithAck(targetID, data, timeout = 30000) {
        if (!this.connected) {
            console.debug('Cannot send direct message with ACK: client not connected');
            return new ClientAck('disconnected', this, timeout, true);
        }

        const ackID = this.generateAckID();
        const clientAck = new ClientAck(ackID, this, timeout);

        // Enregistrer l'ACK localement
        if (!this.ackRequests) {
            this.ackRequests = new Map();
        }
        this.ackRequests.set(ackID, clientAck);

        // Envoyer la demande au serveur
        this.sendMessage({
            action: 'publish_to_id_with_ack',
            to: targetID,
            data: data,
            ack_id: ackID,
            from: this.Id
        });

        return clientAck;
    }

    /**
     * Génère un ID unique pour ACK côté client
     * @returns {string}
     */
    generateAckID() {
        if (!this.nextAckID) {
            this.nextAckID = 1;
        }
        return `client_ack_${this.Id}_${this.nextAckID++}`;
    }

    /**
     * Traite les réponses ACK du serveur
     * @param {Object} data - Données du message
     */
    handleAckResponse(data) {
        const ackID = data.ack_id;
        if (!ackID || !this.ackRequests) return;

        const clientAck = this.ackRequests.get(ackID);
        if (!clientAck || clientAck.cancelled) return;

        const responses = data.responses || {};
        clientAck.handleResponse(responses);
    }

    /**
     * Traite les statuts ACK du serveur
     * @param {Object} data - Données du message
     */
    handleAckStatus(data) {
        const ackID = data.ack_id;
        if (!ackID || !this.ackRequests) return;

        const clientAck = this.ackRequests.get(ackID);
        if (!clientAck || clientAck.cancelled) return;

        const status = data.status || {};
        clientAck.handleStatus(status);
    }

    /**
     * Traite les confirmations d'annulation ACK
     * @param {Object} data - Données du message
     */
    handleAckCancelled(data) {
        const ackID = data.ack_id;
        if (!ackID || !this.ackRequests) return;

        // Nettoyer l'ACK local
        this.ackRequests.delete(ackID);
    }

    /**
     * Envoi non-bloquant de message
     * @param {Object} msg - Message à envoyer
     */
    sendMessage(msg) {
        if (this.messageQueue.length >= 1024) {
            // Queue pleine, on drop le message
            console.debug('Message queue full, dropping message');
            return;
        }
        this.messageQueue.push(msg);
    }

    /**
     * Reconnexion automatique
     * @param {Object} opts - Options de connexion
     */
    async reconnect(opts = null) {
        if (!this.Autorestart) return;

        await this.sleep(this.RestartEvery);

        // Utiliser les options stockées si pas fournies
        if (!opts) {
            opts = {
                Id: this.Id,
                Address: this.Address.replace(/^wss?:\/\//, '').replace(/\/.*$/, ''),
                Secure: this.Address.startsWith('wss'),
                Autorestart: this.Autorestart,
                RestartEvery: this.RestartEvery,
                OnDataWs: this.onDataWS,
                OnId: this.onId,
                OnClose: this.onClose
            };
        }

        try {
            await this.connect(opts);
            // Reconnexion réussie, re-souscrire aux topics
            this.resubscribeAll();
        } catch (error) {
            console.error('Reconnection failed:', error);
        }
    }

    /**
     * Re-souscrit à tous les topics après reconnexion
     */
    resubscribeAll() {
        const topics = Array.from(this.subscriptions.keys());

        // Re-souscrire à tous les topics
        for (const topic of topics) {
            this.sendMessage({
                action: 'subscribe',
                topic: topic,
                from: this.Id
            });
        }
    }

    /**
     * Ferme la connexion
     * @returns {Promise<void>}
     */
    async Close() {
        this.connected = false;
        this.Done = true;

        if (this.onClose) {
            this.onClose();
        }

        if (this.Conn) {
            try {
                this.Conn.close(1000, 'Normal closure');
            } catch (error) {
                console.error('Error closing connection:', error);
            }
            this.Conn = null;
        }

        // Vider la queue de messages
        this.messageQueue = [];
    }

    /**
     * Lance le client en mode run (équivalent du Run() Go)
     * En JavaScript, on utilise les event listeners du navigateur
     */
    Run() {
        // Écouter les événements de fermeture de page
        if (typeof window !== 'undefined') {
            window.addEventListener('beforeunload', () => {
                this.Close();
            });

            // Écouter Ctrl+C en mode console (Node.js)
        } else if (typeof process !== 'undefined') {
            process.on('SIGINT', () => {
                console.log('Closed');
                this.Close();
                process.exit(0);
            });
        }
    }

    /**
     * Définit le callback de fermeture
     * @param {Function} fn - Fonction à exécuter à la fermeture
     */
    OnClose(fn) {
        this.onClose = fn;
    }

    /**
     * Utilitaire pour attendre
     * @param {number} ms - Millisecondes à attendre
     * @returns {Promise<void>}
     */
    sleep(ms) {
        return new Promise(resolve => setTimeout(resolve, ms));
    }

    /**
     * Génère un ID unique (équivalent de ksmux.GenerateID())
     * @returns {string}
     */
    static generateID() {
        return 'client_' + Date.now() + '_' + Math.random().toString(36).substr(2, 9);
    }
}

// Export pour utilisation en module
if (typeof module !== 'undefined' && module.exports) {
    module.exports = { Client, ClientSubscriber, ClientSubscription, ClientAck };
}

// Export pour utilisation en navigateur
if (typeof window !== 'undefined') {
    window.BusClient = { Client, ClientSubscriber, ClientSubscription, ClientAck };
} 