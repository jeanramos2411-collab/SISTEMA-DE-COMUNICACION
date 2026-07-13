package websocket

import (
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"ptt-server/internal/store"
	"ptt-server/internal/utils"
)

const (
	pingInterval = 20 * time.Second
	pingTimeout  = 60 * time.Second
	maxMsgSize   = 1024 * 1024 // 1MB
)

// Cliente individual conectado
type Client struct {
	conn            *websocket.Conn
	sessionID       string
	username        string
	channel         string
	pendingChannel  string
	isTransmitting  bool
	ip              string
	deviceID        string
	mac             string
	connectedAt     string
	writeChan       chan []byte  // Canal para serializar escrituras
	doneChan        chan struct{}
}

// Estado global del servidor - similar a las variables globales de Python
type ServerState struct {
	mu              sync.RWMutex
	clients         map[*Client]bool           // Map de punteros de cliente a bool
	channelMembers  map[string]map[*Client]bool // Canal -> clientes
	channelSpeaker  map[string]*Client          // Canal -> quien habla
	store          *store.Store
}

func NewServerState(s *store.Store) *ServerState {
	return &ServerState{
		clients:        make(map[*Client]bool),
		channelMembers: make(map[string]map[*Client]bool),
		channelSpeaker: make(map[string]*Client),
		store:          s,
	}
}

// Manejador principal - similar a async def handler(ws) en Python
func (s *ServerState) HandleConnection(conn *websocket.Conn, ip string) {
	// Crear cliente como en Python: clients[ws] = Client(...)
	client := &Client{
		conn:        conn,
		sessionID:   generateSessionID(),
		username:    "Usuario",
		ip:          ip,
		mac:         utils.LookupMAC(ip),
		connectedAt: time.Now().UTC().Format(time.RFC3339),
		writeChan:   make(chan []byte, 256),
		doneChan:    make(chan struct{}),
	}

	s.mu.Lock()
	s.clients[client] = true
	s.mu.Unlock()

	log.Printf("[WS] Cliente conectado: %s (%s)", ip, client.sessionID)

	// Iniciar write pump en goroutine separada
	go s.writePump(client)

	// Loop principal - similar a: async for message in ws:
	// Configurar ping/pong como hace Python
	conn.SetReadLimit(maxMsgSize)
	conn.SetPingHandler(func(appData string) error {
		conn.SetReadDeadline(time.Now().Add(pingTimeout))
		return nil
	})

	defer s.cleanupClient(client)

	// Loop principal - recibe mensajes secuencialmente como Python
	for {
		msgType, data, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("[WS] Error de lectura para %s: %v", client.sessionID, err)
			}
			break
		}

		// Extender deadline después de leer
		conn.SetReadDeadline(time.Now().Add(pingTimeout))

		// Procesar según tipo de mensaje - como en Python:
		// if isinstance(message, bytes): ... else: ... await handle_json(...)
		if msgType == websocket.BinaryMessage {
			// Audio binario
			s.handleAudio(client, data)
		} else {
			// Mensaje JSON de texto
			var msg map[string]interface{}
			if err := json.Unmarshal(data, &msg); err != nil {
				log.Printf("[WS] JSON invalido de %s: %v", client.sessionID, err)
				s.sendJSON(client, map[string]interface{}{
					"type":    "error",
					"message": "JSON invalido",
				})
				continue
			}
			s.handleJSON(client, msg)
		}
	}
}

// cleanupClient - similar a async def cleanup_client(ws)
func (s *ServerState) cleanupClient(client *Client) {
	// Cerrar canales de escritura
	if client.doneChan != nil {
		close(client.doneChan)
	}
	if client.writeChan != nil {
		close(client.writeChan)
	}

	s.mu.Lock()
	delete(s.clients, client)

	if client.channel != "" {
		members, ok := s.channelMembers[client.channel]
		if ok {
			delete(members, client)
			if len(members) == 0 {
				delete(s.channelMembers, client.channel)
			}
		}

		// Si era el speaker, notificar a otros
		if speaker, ok := s.channelSpeaker[client.channel]; ok && speaker == client {
			delete(s.channelSpeaker, client.channel)
			s.mu.Unlock()
			s.broadcastJSON(client.channel, map[string]interface{}{
				"type":     "ptt_ended",
				"username": client.username,
			}, nil)
			s.notifyUsers(client.channel)
			s.mu.Lock()
		} else {
			s.mu.Unlock()
		}

		// Notificar actualización de usuarios
		s.notifyUsers(client.channel)
	} else {
		s.mu.Unlock()
	}

	// Limpiar pending requests
	s.store.RemovePendingBySession(client.sessionID)

	log.Printf("[WS] Cliente desconectado: %s (%s)", client.username, client.sessionID)
}

// handleJSON - procesa mensajes JSON (join, ptt_start, ptt_end, ping)
func (s *ServerState) handleJSON(client *Client, data map[string]interface{}) {
	msgType := getString(data, "type")

	switch msgType {
	case "join":
		s.handleJoin(client, data)
	case "ptt_start":
		s.handlePTTStart(client)
	case "ptt_end":
		s.handlePTTEnd(client)
	case "ping":
		s.sendJSON(client, map[string]interface{}{"type": "pong"})
	default:
		s.sendJSON(client, map[string]interface{}{
			"type":    "error",
			"message": "Tipo desconocido: " + msgType,
		})
	}
}

// handleJoin - similar a async def handle_join(ws, data)
func (s *ServerState) handleJoin(client *Client, data map[string]interface{}) {
	username := getString(data, "username")
	channel := getString(data, "channel")
	deviceID := getString(data, "device_id")
	mac := getString(data, "mac")

	client.username = username
	client.deviceID = deviceID
	if mac != "" {
		client.mac = mac
	}

	// Registrar dispositivo
	if deviceID != "" {
		s.store.TouchDevice(deviceID, username, client.ip, client.mac)
	}

	// Verificar bloqueo
	if s.store.IsBlocked(username, deviceID, client.ip) {
		s.sendJSON(client, map[string]interface{}{
			"type":    "error",
			"message": "Acceso bloqueado por el administrador",
		})
		client.conn.Close()
		return
	}

	// Verificar que el canal existe y está habilitado
	enabled := s.store.EnabledChannelNames()
	if !contains(enabled, channel) {
		s.sendJSON(client, map[string]interface{}{
			"type":    "error",
			"message": "Bloque invalido. Disponibles: " + joinStrings(enabled, ", "),
		})
		return
	}

	// Canal requiere aprobación?
	channelInfo := s.store.ChannelByName(channel)
	if channelInfo != nil && channelInfo.Access == "approval" {
		if !s.store.IsDeviceApprovedForChannel(deviceID, channel) {
			client.channel = ""
			client.pendingChannel = channel
			pending := s.store.UpsertPending(
				deviceID, username, client.ip, client.mac,
				channelInfo.ID, channel, client.sessionID,
			)
			s.sendJSON(client, map[string]interface{}{
				"type":       "approval_pending",
				"channel":    channel,
				"message":    "Esperando aprobacion del administrador para este bloque",
				"request_id": pending.ID,
			})
			log.Printf("Solicitud de acceso: %s -> %s (%s)", username, channel, deviceID)
			return
		}
	}

	// Completar join
	s.completeJoin(client, channel)
}

// completeJoin - similar a async def complete_join(ws, channel)
func (s *ServerState) completeJoin(client *Client, channel string) {
	s.mu.Lock()
	oldChannel := client.channel

	// Limpiar canal anterior si existe
	if oldChannel != "" && oldChannel != channel {
		if members, ok := s.channelMembers[oldChannel]; ok {
			delete(members, client)
			if len(members) == 0 {
				delete(s.channelMembers, oldChannel)
			}
		}
		if speaker, ok := s.channelSpeaker[oldChannel]; ok && speaker == client {
			delete(s.channelSpeaker, oldChannel)
			// Liberar lock antes de broadcast
			s.mu.Unlock()
			s.broadcastJSON(oldChannel, map[string]interface{}{
				"type":     "ptt_ended",
				"username": client.username,
			}, nil)
			s.notifyUsers(oldChannel)
			s.mu.Lock()
		}
	}

	client.channel = channel
	client.pendingChannel = ""
	client.isTransmitting = false

	// Agregar al nuevo canal
	if _, ok := s.channelMembers[channel]; !ok {
		s.channelMembers[channel] = make(map[*Client]bool)
	}
	s.channelMembers[channel][client] = true
	s.mu.Unlock()

	// Registrar acceso
	s.store.RecordDeviceChannelAccess(client.deviceID, channel)

	// Enviar respuesta al cliente
	enabled := s.store.EnabledChannelNames()
	gain := s.store.DevicePlaybackGain(client.deviceID)

	s.sendJSON(client, map[string]interface{}{
		"type":          "joined",
		"channel":       channel,
		"channels":      enabled,
		"users":         s.usersInChannel(channel),
		"playback_gain": gain,
		"audio_format":  "pcm",
	})

	// Notificar a otros usuarios del canal
	s.notifyUsers(channel)

	log.Printf("%s (%s / %s) entro a %s", client.username, client.ip, client.mac, channel)
}

// handlePTTStart - similar a async def handle_ptt_start(ws)
func (s *ServerState) handlePTTStart(client *Client) {
	if client.channel == "" {
		s.sendJSON(client, map[string]interface{}{
			"type":    "error",
			"message": "Debe unirse a un bloque primero",
		})
		return
	}

	if s.store.IsBlocked(client.username, client.deviceID, client.ip) {
		s.sendJSON(client, map[string]interface{}{
			"type":    "error",
			"message": "Acceso bloqueado por el administrador",
		})
		return
	}

	s.mu.Lock()

	// Verificar speaker actual
	currentSpeaker := s.channelSpeaker[client.channel]
	if currentSpeaker != nil && currentSpeaker != client && s.isOpen(currentSpeaker) {
		s.mu.Unlock()
		s.sendJSON(client, map[string]interface{}{
			"type":    "ptt_denied",
			"reason":  "Canal ocupado",
			"speaker": currentSpeaker.username,
		})
		return
	}

	// Asignar como speaker
	s.channelSpeaker[client.channel] = client
	client.isTransmitting = true

	// Enviar granted al que pide
	s.mu.Unlock()

	s.sendJSON(client, map[string]interface{}{"type": "ptt_granted"})

	// Notificar a otros del canal
	s.broadcastJSON(client.channel, map[string]interface{}{
		"type":     "ptt_started",
		"username": client.username,
	}, client)

	log.Printf("%s transmite en %s", client.username, client.channel)
}

// handlePTTEnd - similar a async def handle_ptt_end(ws)
func (s *ServerState) handlePTTEnd(client *Client) {
	if client.channel == "" {
		return
	}

	s.mu.Lock()
	if speaker, ok := s.channelSpeaker[client.channel]; ok && speaker == client {
		delete(s.channelSpeaker, client.channel)
	}
	s.mu.Unlock()

	if client.isTransmitting {
		client.isTransmitting = false
		s.broadcastJSON(client.channel, map[string]interface{}{
			"type":     "ptt_ended",
			"username": client.username,
		}, nil)
	}
}

// handleAudio - reenvía audio a otros en el canal
func (s *ServerState) handleAudio(sender *Client, audio []byte) {
	if sender.channel == "" || !sender.isTransmitting {
		return
	}

	s.mu.RLock()
	speaker := s.channelSpeaker[sender.channel]
	isSpeaker := speaker == sender
	members, ok := s.channelMembers[sender.channel]
	s.mu.RUnlock()

	if !ok || !isSpeaker {
		return
	}

	// Crear copia de destinatarios (como en Python)
	var recipients []*Client
	for c := range members {
		if c != sender && c != nil && s.isOpen(c) && !c.isTransmitting {
			recipients = append(recipients, c)
		}
	}

	// Enviar audio a cada destinatario - como Python: asyncio.create_task(_safe_send(ws))
	for _, c := range recipients {
		go s.sendAudioAsync(c, audio)
	}
}

// writePump - pump de escritura para serializar mensajes
func (s *ServerState) writePump(client *Client) {
	for {
		select {
		case msg, ok := <-client.writeChan:
			if !ok {
				return
			}
			client.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := client.conn.WriteMessage(websocket.BinaryMessage, msg); err != nil {
				log.Printf("[WS] Error en writePump: %v", err)
				return
			}
		case <-client.doneChan:
			return
		}
	}
}

// sendAudioAsync - envía audio sin bloquear (fire-and-forget como Python)
func (s *ServerState) sendAudioAsync(client *Client, audio []byte) {
	if client == nil || !s.isOpen(client) || client.writeChan == nil {
		return
	}

	select {
	case client.writeChan <- audio:
	default:
		// Canal lleno, ignorar
	}
}

// sendJSON - envía JSON al cliente directamente (como Python await ws.send)
func (s *ServerState) sendJSON(client *Client, data map[string]interface{}) {
	if client == nil || !s.isOpen(client) || client.writeChan == nil {
		return
	}

	msg, err := json.Marshal(data)
	if err != nil {
		log.Printf("[WS] Error serializando JSON: %v", err)
		return
	}

	select {
	case client.writeChan <- msg:
	default:
		// Canal lleno
	}
}

// broadcastJSON - envía a todos en el canal (como Python: asyncio.create_task)
func (s *ServerState) broadcastJSON(channel string, data map[string]interface{}, exclude *Client) {
	msg, err := json.Marshal(data)
	if err != nil {
		return
	}

	s.mu.RLock()
	members, ok := s.channelMembers[channel]
	s.mu.RUnlock()

	if !ok {
		return
	}

	// Enviar a cada cliente - fire-and-forget como Python
	for c := range members {
		if c == exclude || c == nil || !s.isOpen(c) {
			continue
		}
		go s.sendJSONAsync(c, msg)
	}
}

// sendJSONAsync - envía JSON de forma asíncrona (como asyncio.create_task en Python)
func (s *ServerState) sendJSONAsync(client *Client, msg []byte) {
	if client == nil || !s.isOpen(client) || client.writeChan == nil {
		return
	}

	select {
	case client.writeChan <- msg:
	default:
		// Canal lleno, ignorar
	}
}

// notifyUsers - notifica actualización de usuarios en canal
func (s *ServerState) notifyUsers(channel string) {
	users := s.usersInChannel(channel)
	s.broadcastJSON(channel, map[string]interface{}{
		"type":  "users_update",
		"users": users,
	}, nil)
}

// usersInChannel - lista de usernames en un canal
func (s *ServerState) usersInChannel(channel string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var names []string
	members, ok := s.channelMembers[channel]
	if !ok {
		return names
	}

	for c := range members {
		if c != nil && s.isOpen(c) {
			names = append(names, c.username)
		}
	}

	// Ordenar como hace Python
	sortStrings(names)
	return names
}

// isOpen - verifica si la conexión está abierta (como en Python)
// Nota: gorilla/websocket.Connection tiene un camponet.Conn cerrado internamente
// Simplemente verificamos que el cliente y la conexión no sean nil
func (s *ServerState) isOpen(client *Client) bool {
	if client == nil || client.conn == nil {
		return false
	}
	return true
}

// CompleteJoin público para cuando se aprueba una solicitud
func (s *ServerState) CompleteJoin(sessionID, channel string) bool {
	s.mu.RLock()
	var client *Client
	for c := range s.clients {
		if c.sessionID == sessionID {
			client = c
			break
		}
	}
	s.mu.RUnlock()

	if client == nil {
		return false
	}

	s.completeJoin(client, channel)
	return true
}

// KickClient - expulsa un cliente por sessionID
func (s *ServerState) KickClient(sessionID string) bool {
	s.mu.RLock()
	var client *Client
	for c := range s.clients {
		if c.sessionID == sessionID {
			client = c
			break
		}
	}
	s.mu.RUnlock()

	if client == nil {
		return false
	}

	client.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(4000, "Expulsado por administrador"))
	return true
}

// ApprovePending - aprueba una solicitud pendiente
func (s *ServerState) ApprovePending(pendingID string) bool {
	item := s.store.ApprovePending(pendingID)
	if item == nil || item.ChannelName == "" {
		return false
	}

	return s.CompleteJoin(item.SessionID, item.ChannelName)
}

// RejectPending - rechaza una solicitud pendiente
func (s *ServerState) RejectPending(pendingID string) bool {
	item := s.store.RemovePending(pendingID)
	if item == nil {
		return false
	}

	s.mu.RLock()
	var client *Client
	for c := range s.clients {
		if c.sessionID == item.SessionID {
			client = c
			break
		}
	}
	s.mu.RUnlock()

	if client != nil {
		client.pendingChannel = ""
		s.sendJSON(client, map[string]interface{}{
			"type":    "approval_denied",
			"channel": item.ChannelName,
			"message": "Acceso denegado por el administrador",
		})
	}

	return true
}

// GetClientsSnapshot - similar a clients_snapshot() de Python
func (s *ServerState) GetClientsSnapshot() []ClientSnapshot {
	s.mu.Lock()
	// Reconciliar estado PTT
	for channel, speaker := range s.channelSpeaker {
		if !s.isOpen(speaker) {
			delete(s.channelSpeaker, channel)
		}
	}
	s.mu.Unlock()

	s.mu.RLock()
	defer s.mu.RUnlock()

	var rows []ClientSnapshot
	for c := range s.clients {
		if !s.isOpen(c) {
			continue
		}
		snapshot := ClientSnapshot{
			SessionID:      c.sessionID,
			Username:       c.username,
			Channel:        c.channel,
			PendingChannel: c.pendingChannel,
			IP:             c.ip,
			MAC:            c.mac,
			DeviceID:       c.deviceID,
			IsTransmitting: c.isTransmitting,
			IsSpeaking:     s.channelSpeaker[c.channel] == c,
			ConnectedAt:    c.connectedAt,
		}
		rows = append(rows, snapshot)
	}

	// Ordenar como Python
	sortSnapshots(rows)
	return rows
}

type ClientSnapshot struct {
	SessionID      string `json:"session_id"`
	Username       string `json:"username"`
	Channel        string `json:"channel"`
	PendingChannel string `json:"pending_channel"`
	IP             string `json:"ip"`
	MAC            string `json:"mac"`
	DeviceID       string `json:"device_id"`
	IsTransmitting bool   `json:"is_transmitting"`
	IsSpeaking     bool   `json:"is_speaking"`
	ConnectedAt    string `json:"connected_at"`
}

// Funciones auxiliares
func generateSessionID() string {
	// Genera ID corto como Python: uuid.uuid4()[:8]
	return newUUID()[:8]
}

func getString(data map[string]interface{}, key string) string {
	if v, ok := data[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func joinStrings(slice []string, sep string) string {
	if len(slice) == 0 {
		return ""
	}
	result := slice[0]
	for i := 1; i < len(slice); i++ {
		result += sep + slice[i]
	}
	return result
}

func sortStrings(s []string) {
	for i := 0; i < len(s)-1; i++ {
		for j := i + 1; j < len(s); j++ {
			if s[i] > s[j] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}

func sortSnapshots(s []ClientSnapshot) {
	for i := 0; i < len(s)-1; i++ {
		for j := i + 1; j < len(s); j++ {
			ci := s[i].Channel
			if ci == "" {
				ci = s[i].PendingChannel
			}
			cj := s[j].Channel
			if cj == "" {
				cj = s[j].PendingChannel
			}
			if ci > cj || (ci == cj && s[i].Username > s[j].Username) {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}
