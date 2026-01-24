"""
Client WebSocket pour le système pub/sub
Traduction fidèle du client.go en Python avec asyncio pour la performance
"""

import asyncio
import json
import logging
import signal
import time
import uuid
from typing import Any, Callable, Dict, List, Optional, Union
from dataclasses import dataclass, field
from concurrent.futures import ThreadPoolExecutor
import websockets
from websockets.exceptions import ConnectionClosed, WebSocketException


@dataclass
class ClientSubscription:
    """Subscription locale côté client"""
    topic: str
    callback: Callable[[Any, Callable[[], None]], None]
    active: bool = True


@dataclass
class ClientSubscriber:
    """Équivalent du ClientSubscriber Go"""
    client: 'Client'
    Id: str = ""
    Topic: str = ""
    Ch: Optional[Any] = None  # Pas utilisé en Python
    Conn: Optional[Any] = None  # Référence WebSocket

    def Unsubscribe(self):
        """Désabonne ce subscriber"""
        asyncio.create_task(self.client.Unsubscribe(self.Topic))


@dataclass
class ClientAck:
    """Handle pour attendre les acknowledgments côté client"""
    ID: str
    Client: 'Client'
    timeout: float
    cancelled: bool = False
    responses: Optional[Dict[str, Any]] = None
    status: Optional[Dict[str, bool]] = None
    done: bool = False
    _response_future: Optional[asyncio.Future] = field(default=None, init=False)
    _status_future: Optional[asyncio.Future] = field(default=None, init=False)

    async def Wait(self) -> Dict[str, Any]:
        """Attend tous les acknowledgments avec timeout"""
        if self.cancelled or self.done:
            return {}

        try:
            # Créer une future pour attendre la réponse
            self._response_future = asyncio.Future()
            
            # Attendre avec timeout
            return await asyncio.wait_for(self._response_future, timeout=self.timeout)
        except asyncio.TimeoutError:
            await self.Cancel()
            return {}
        except Exception:
            return {}

    async def WaitAny(self) -> tuple[Dict[str, Any], bool]:
        """Attend au moins un acknowledgment"""
        if self.cancelled or self.done:
            return {}, False

        try:
            self._response_future = asyncio.Future()
            responses = await asyncio.wait_for(self._response_future, timeout=self.timeout)
            
            # Retourner le premier ACK reçu
            for client_id, response in responses.items():
                return response, True
            return {}, False
        except asyncio.TimeoutError:
            await self.Cancel()
            return {}, False
        except Exception:
            return {}, False

    async def GetStatus(self) -> Dict[str, bool]:
        """Retourne le statut actuel des acknowledgments"""
        if self.cancelled or self.done:
            return {}

        # Demander le statut au serveur
        await self.Client.send_message({
            "action": "get_ack_status",
            "ack_id": self.ID,
            "from": self.Client.Id
        })

        try:
            self._status_future = asyncio.Future()
            return await asyncio.wait_for(self._status_future, timeout=2.0)
        except asyncio.TimeoutError:
            return {}
        except Exception:
            return {}

    async def IsComplete(self) -> bool:
        """Vérifie si tous les ACK sont reçus"""
        status = await self.GetStatus()
        return len(status) > 0 and all(status.values())

    async def Cancel(self):
        """Annule l'attente des acknowledgments"""
        if self.cancelled or self.done:
            return

        self.cancelled = True
        self.done = True

        # Envoyer la demande d'annulation au serveur
        await self.Client.send_message({
            "action": "cancel_ack",
            "ack_id": self.ID,
            "from": self.Client.Id
        })

        # Nettoyer localement
        if self.Client.ack_requests and self.ID in self.Client.ack_requests:
            del self.Client.ack_requests[self.ID]

        # Résoudre les futures en attente
        if self._response_future and not self._response_future.done():
            self._response_future.set_result({})
        if self._status_future and not self._status_future.done():
            self._status_future.set_result({})

    def handle_response(self, responses: Dict[str, Any]):
        """Traite une réponse ACK du serveur"""
        if self._response_future and not self._response_future.done():
            self._response_future.set_result(responses)
        self.responses = responses

    def handle_status(self, status: Dict[str, bool]):
        """Traite un statut ACK du serveur"""
        if self._status_future and not self._status_future.done():
            self._status_future.set_result(status)
        self.status = status


class Client:
    """Client WebSocket ultra-performant pour le système pub/sub"""

    def __init__(self):
        self.Id: str = ""
        self.ServerAddr: str = ""
        self.onDataWS: Optional[Callable[[Dict[str, Any], Any], None]] = None
        self.onId: Optional[Callable[[Dict[str, Any], ClientSubscriber], None]] = None
        self.onClose: Optional[Callable[[], None]] = None
        self.RestartEvery: float = 10.0  # secondes
        self.Conn: Optional[websockets.WebSocketServerProtocol] = None
        self.Autorestart: bool = False
        self.Done: bool = False

        # Optimisations pour pub/sub
        self.subscriptions: Dict[str, ClientSubscription] = {}
        self.message_queue: asyncio.Queue = asyncio.Queue(maxsize=1024)
        self.connected: bool = False
        self.ack_requests: Dict[str, ClientAck] = {}
        self.next_ack_id: int = 1

        # Tasks pour gestion asynchrone
        self._message_handler_task: Optional[asyncio.Task] = None
        self._message_sender_task: Optional[asyncio.Task] = None
        self._reconnect_task: Optional[asyncio.Task] = None

        # Thread pool pour callbacks utilisateur
        self._executor = ThreadPoolExecutor(max_workers=4)

    @classmethod
    async def NewClient(cls, **opts) -> 'Client':
        """
        Crée un nouveau client avec les options spécifiées
        
        Args:
            Id: ID du client
            Address: Adresse du serveur
            Secure: Utiliser WSS
            Path: Chemin WebSocket (défaut: /ws/bus)
            Autorestart: Reconnexion automatique
            RestartEvery: Intervalle de reconnexion
            OnDataWs: Callback pour tous les messages
            OnId: Callback pour messages directs
            OnClose: Callback de fermeture
        """
        if opts.get('Autorestart') and not opts.get('RestartEvery'):
            opts['RestartEvery'] = 10.0

        if not opts.get('OnDataWs'):
            opts['OnDataWs'] = lambda data, conn: None

        client = cls()
        client.Id = opts.get('Id', cls.generate_id())
        client.Autorestart = opts.get('Autorestart', False)
        client.RestartEvery = opts.get('RestartEvery', 10.0)
        client.onDataWS = opts.get('OnDataWs')
        client.onId = opts.get('OnId')
        client.onClose = opts.get('OnClose')

        await client.connect(opts)
        return client

    async def connect(self, opts: Dict[str, Any]):
        """Se connecte au serveur WebSocket"""
        scheme = "wss" if opts.get('Secure') else "ws"
        path = opts.get('Path', '/ws/bus')
        address = opts['Address']
        url = f"{scheme}://{address}{path}"
        
        self.ServerAddr = url

        try:
            # Connexion WebSocket avec options optimisées
            self.Conn = await websockets.connect(
                url,
                ping_interval=20,
                ping_timeout=10,
                close_timeout=5,
                max_size=10**7,  # 10MB max message size
                compression=None  # Désactiver compression pour performance
            )
            
            self.connected = True
            print(f"client connected to {url}")
            
            # Démarrer les handlers
            self._message_handler_task = asyncio.create_task(self.message_handler())
            self._message_sender_task = asyncio.create_task(self.message_sender())
            
            # Ping initial pour enregistrer le client
            await self.send_message({
                "action": "ping",
                "from": self.Id
            })

        except Exception as e:
            if self.Autorestart:
                print(f"Connection failed, retrying in {self.RestartEvery} seconds")
                await asyncio.sleep(self.RestartEvery)
                await self.connect(opts)
            else:
                raise e

    async def message_handler(self):
        """Gère les messages entrants de manière asynchrone"""
        try:
            async for message in self.Conn:
                if self.Done or not self.connected:
                    break

                try:
                    data = json.loads(message)
                    # Traiter le message dans une tâche séparée pour ne pas bloquer
                    asyncio.create_task(self.handle_message(data))
                except json.JSONDecodeError as e:
                    logging.error(f"WebSocket JSON decode error: {e}")
                except Exception as e:
                    logging.error(f"WebSocket message handling error: {e}")

        except ConnectionClosed:
            self.connected = False
            if self.Autorestart and not self.Done:
                self._reconnect_task = asyncio.create_task(self.reconnect())
        except Exception as e:
            logging.error(f"WebSocket read error: {e}")
            self.connected = False
            if self.Autorestart and not self.Done:
                self._reconnect_task = asyncio.create_task(self.reconnect())

    async def message_sender(self):
        """Gère l'envoi des messages en queue de manière asynchrone"""
        while not self.Done:
            try:
                # Attendre un message dans la queue
                msg = await self.message_queue.get()
                
                if not self.connected or not self.Conn:
                    continue

                # Envoyer le message
                await self.Conn.send(json.dumps(msg))
                self.message_queue.task_done()

            except ConnectionClosed:
                self.connected = False
                break
            except Exception as e:
                logging.error(f"WebSocket write error: {e}")
                self.connected = False
                break

    async def handle_message(self, data: Dict[str, Any]):
        """Traite les messages reçus du serveur"""
        action = data.get("action")
        if not action:
            return

        if action == "pong":
            # Confirmation de connexion
            if self.onId:
                await self._run_callback(self.onId, data, ClientSubscriber(self))

        elif action == "publish":
            # Message publié sur un topic
            await self.handle_publish_message(data)

        elif action == "publish_ack":
            # Message publié avec demande d'ACK
            await self.handle_publish_ack_message(data)

        elif action == "direct_message":
            # Message direct vers ce client
            if self.onId:
                payload = data.get("data")
                await self._run_callback(self.onId, {"data": payload}, ClientSubscriber(self))

        elif action in ["subscribed", "unsubscribed", "published"]:
            # Confirmations - on peut les ignorer ou les logger
            logging.debug(f"Received confirmation: {action}")

        elif action == "error":
            # Erreur du serveur
            if "error" in data:
                logging.error(f"Server error: {data['error']}")

        elif action == "ack_response":
            # Réponse ACK du serveur
            self.handle_ack_response(data)

        elif action == "ack_status":
            # Statut ACK du serveur
            self.handle_ack_status(data)

        elif action == "ack_cancelled":
            # Confirmation d'annulation ACK
            self.handle_ack_cancelled(data)

        # Callback utilisateur pour tous les messages
        if self.onDataWS:
            await self._run_callback(self.onDataWS, data, self.Conn)

    async def handle_publish_message(self, data: Dict[str, Any]):
        """Traite les messages publiés"""
        topic = data.get("topic")
        if not topic:
            return

        payload = data.get("data")
        sub = self.subscriptions.get(topic)

        if sub and sub.active:
            # Créer fonction d'unsubscribe
            async def unsub_fn():
                await self.Unsubscribe(topic)

            # Exécuter le callback dans le thread pool
            await self._run_callback(sub.callback, payload, unsub_fn)

    async def handle_publish_ack_message(self, data: Dict[str, Any]):
        """Traite les messages publiés avec ACK"""
        topic = data.get("topic")
        if not topic:
            return

        payload = data.get("data")
        ack_id = data.get("ack_id")
        sub = self.subscriptions.get(topic)

        if sub and sub.active:
            # Créer fonction d'unsubscribe
            async def unsub_fn():
                await self.Unsubscribe(topic)

            # Exécuter le callback avec gestion d'erreur
            success = True
            error_msg = ""

            try:
                await self._run_callback(sub.callback, payload, unsub_fn)
            except Exception as e:
                success = False
                error_msg = str(e)

            # Envoyer ACK après traitement
            if ack_id:
                await self.send_ack(ack_id, success, error_msg)

        elif ack_id:
            # Pas de subscriber, envoyer ACK d'erreur
            await self.send_ack(ack_id, False, "no subscriber for topic")

    async def send_ack(self, ack_id: str, success: bool, error_msg: str = ""):
        """Envoie un acknowledgment"""
        ack_msg = {
            "action": "ack",
            "ack_id": ack_id,
            "from": self.Id,
            "data": {
                "ack_id": ack_id,
                "client_id": self.Id,
                "success": success
            }
        }

        if error_msg:
            ack_msg["data"]["error"] = error_msg

        await self.send_message(ack_msg)

    async def Subscribe(self, topic: str, callback: Callable[[Any, Callable[[], None]], None]) -> Callable[[], None]:
        """Souscription WebSocket optimisée"""
        if not self.connected:
            logging.debug("Cannot subscribe: client not connected")
            return lambda: None

        # Créer la subscription locale
        sub = ClientSubscription(topic, callback)
        self.subscriptions[topic] = sub

        # Envoyer la demande de souscription au serveur
        await self.send_message({
            "action": "subscribe",
            "topic": topic,
            "from": self.Id
        })

        # Retourner fonction d'unsubscribe
        async def unsubscribe():
            await self.Unsubscribe(topic)
        
        return unsubscribe

    async def Unsubscribe(self, topic: str):
        """Désabonnement WebSocket"""
        sub = self.subscriptions.get(topic)
        if sub:
            sub.active = False
            del self.subscriptions[topic]

        if self.connected:
            # Envoyer la demande de désabonnement au serveur
            await self.send_message({
                "action": "unsubscribe",
                "topic": topic,
                "from": self.Id
            })

    async def Publish(self, topic: str, data: Any):
        """Publication WebSocket optimisée"""
        if not self.connected:
            logging.debug("Cannot publish: client not connected")
            return

        await self.send_message({
            "action": "publish",
            "topic": topic,
            "data": data,
            "from": self.Id
        })

    async def PublishToID(self, target_id: str, data: Any):
        """Envoi de message direct vers un ID (client ou serveur)"""
        if not self.connected:
            logging.debug("Cannot send direct message: client not connected")
            return

        await self.send_message({
            "action": "direct_message",
            "to": target_id,
            "data": data,
            "from": self.Id
        })

    async def PublishToServer(self, addr: str, data: Any, secure: bool = False):
        """Envoi de message vers un serveur distant via le serveur local"""
        if not self.connected:
            logging.debug("Cannot send to server: client not connected")
            return

        await self.send_message({
            "action": "publish_to_server",
            "to": addr,
            "data": data,
            "from": self.Id
        })

    async def PublishWithAck(self, topic: str, data: Any, timeout: float = 30.0) -> ClientAck:
        """Publication avec acknowledgment via le serveur"""
        if not self.connected:
            logging.debug("Cannot publish with ACK: client not connected")
            return ClientAck("disconnected", self, timeout, cancelled=True)

        ack_id = self.generate_ack_id()
        client_ack = ClientAck(ack_id, self, timeout)

        # Enregistrer l'ACK localement
        self.ack_requests[ack_id] = client_ack

        # Envoyer la demande au serveur
        await self.send_message({
            "action": "publish_with_ack",
            "topic": topic,
            "data": data,
            "ack_id": ack_id,
            "from": self.Id
        })

        return client_ack

    async def PublishToIDWithAck(self, target_id: str, data: Any, timeout: float = 30.0) -> ClientAck:
        """Envoi de message direct avec acknowledgment"""
        if not self.connected:
            logging.debug("Cannot send direct message with ACK: client not connected")
            return ClientAck("disconnected", self, timeout, cancelled=True)

        ack_id = self.generate_ack_id()
        client_ack = ClientAck(ack_id, self, timeout)

        # Enregistrer l'ACK localement
        self.ack_requests[ack_id] = client_ack

        # Envoyer la demande au serveur
        await self.send_message({
            "action": "publish_to_id_with_ack",
            "to": target_id,
            "data": data,
            "ack_id": ack_id,
            "from": self.Id
        })

        return client_ack

    def generate_ack_id(self) -> str:
        """Génère un ID unique pour ACK côté client"""
        ack_id = f"client_ack_{self.Id}_{self.next_ack_id}"
        self.next_ack_id += 1
        return ack_id

    def handle_ack_response(self, data: Dict[str, Any]):
        """Traite les réponses ACK du serveur"""
        # Support optimized protocol (flat) or legacy (nested in data)
        ack_id = data.get("ack_id") or data.get("data", {}).get("ack_id")
        
        if not ack_id or ack_id not in self.ack_requests:
            return

        client_ack = self.ack_requests[ack_id]
        if client_ack.cancelled:
            return

        responses = data.get("responses") or data.get("data", {}).get("responses", {})
        client_ack.handle_response(responses)

    def handle_ack_status(self, data: Dict[str, Any]):
        """Traite les statuts ACK du serveur"""
        ack_id = data.get("ack_id") or data.get("data", {}).get("ack_id")
        
        if not ack_id or ack_id not in self.ack_requests:
            return

        client_ack = self.ack_requests[ack_id]
        if client_ack.cancelled:
            return

        status = data.get("status") or data.get("data", {}).get("status", {})
        client_ack.handle_status(status)

    def handle_ack_cancelled(self, data: Dict[str, Any]):
        """Traite les confirmations d'annulation ACK"""
        ack_id = data.get("ack_id") or data.get("data", {}).get("ack_id")
        
        if ack_id and ack_id in self.ack_requests:
            del self.ack_requests[ack_id]

    async def send_message(self, msg: Dict[str, Any]):
        """Envoi non-bloquant de message"""
        try:
            await self.message_queue.put(msg)
        except asyncio.QueueFull:
            logging.debug("Message queue full, dropping message")

    async def reconnect(self, opts: Optional[Dict[str, Any]] = None):
        """Reconnexion automatique"""
        if not self.Autorestart:
            return

        await asyncio.sleep(self.RestartEvery)

        # Utiliser les options stockées si pas fournies
        if not opts:
            # Extraire l'adresse de l'URL
            import urllib.parse
            parsed = urllib.parse.urlparse(self.ServerAddr)
            
            opts = {
                'Id': self.Id,
                'Address': parsed.netloc,
                'Path': parsed.path,  # Fix: Preserve Path
                'Secure': parsed.scheme == 'wss',
                'Autorestart': self.Autorestart,
                'RestartEvery': self.RestartEvery,
                'OnDataWs': self.onDataWS,
                'OnId': self.onId,
                'OnClose': self.onClose
            }

        try:
            await self.connect(opts)
            # Reconnexion réussie, re-souscrire aux topics
            await self.resubscribe_all()
        except Exception as e:
            logging.error(f"Reconnection failed: {e}")

    async def resubscribe_all(self):
        """Re-souscrit à tous les topics après reconnexion"""
        topics = list(self.subscriptions.keys())

        # Re-souscrire à tous les topics
        for topic in topics:
            await self.send_message({
                "action": "subscribe",
                "topic": topic,
                "from": self.Id
            })

    async def Close(self):
        """Ferme la connexion"""
        self.connected = False
        self.Done = True

        if self.onClose:
            await self._run_callback(self.onClose)

        # Annuler toutes les tâches
        tasks = [self._message_handler_task, self._message_sender_task, self._reconnect_task]
        for task in tasks:
            if task and not task.done():
                task.cancel()

        # Fermer la connexion WebSocket
        if self.Conn:
            try:
                await self.Conn.close()
            except Exception as e:
                logging.error(f"Error closing connection: {e}")
            self.Conn = None

        # Fermer le thread pool
        self._executor.shutdown(wait=False)

    def Run(self):
        """Lance le client en mode run (équivalent du Run() Go)"""
        def signal_handler(signum, frame):
            print("Closed")
            asyncio.create_task(self.Close())

        # Écouter Ctrl+C
        signal.signal(signal.SIGINT, signal_handler)
        signal.signal(signal.SIGTERM, signal_handler)

    def OnClose(self, fn: Callable[[], None]):
        """Définit le callback de fermeture"""
        self.onClose = fn

    async def _run_callback(self, callback: Callable, *args):
        """Exécute un callback dans le thread pool pour éviter de bloquer l'event loop"""
        if callback:
            try:
                # Si c'est une coroutine, l'exécuter directement
                if asyncio.iscoroutinefunction(callback):
                    await callback(*args)
                else:
                    # Sinon, l'exécuter dans le thread pool
                    loop = asyncio.get_event_loop()
                    await loop.run_in_executor(self._executor, callback, *args)
            except Exception as e:
                logging.error(f"Callback error: {e}")

    @staticmethod
    def generate_id() -> str:
        """Génère un ID unique (équivalent de ksmux.GenerateID())"""
        return f"client_{int(time.time() * 1000)}_{uuid.uuid4().hex[:8]}"


# Exemple d'utilisation
async def main():
    """Exemple d'utilisation du client Python"""
    
    # Créer le client
    client = await Client.NewClient(
        Id="python-client",
        Address="localhost:9313",
        Autorestart=True,
        OnId=lambda data, unsub: print(f"📬 Reçu: {data}"),
        OnDataWs=lambda data, conn: print(f"🔔 Message: {data}")
    )

    # Souscrire à un topic
    unsub = await client.Subscribe("test", lambda data, unsub: print(f"📨 Topic test: {data}"))

    # Publier un message
    await client.Publish("test", "Hello from Python!")

    # Test ACK
    ack = await client.PublishWithAck("test", "Message avec ACK", 5.0)
    responses = await ack.Wait()
    print(f"📬 ACK reçus: {responses}")

    # Garder le client en vie
    try:
        await asyncio.sleep(10)
    finally:
        await client.Close()


if __name__ == "__main__":
    # Configuration du logging
    logging.basicConfig(level=logging.INFO)
    
    # Lancer l'exemple
    asyncio.run(main()) 