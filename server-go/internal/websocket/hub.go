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
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingInterval   = 20 * time.Second
	maxMessageSize = 1024 * 1024
)

type Client struct {
	Hub            *Hub
	Conn           *websocket.Conn
	Send           chan []byte
	SessionID      string
	Username       string
	Channel        string
	PendingChannel string
	IsTransmitting bool
	IP             string
	DeviceID       string
	MAC            string
	ConnectedAt    string
	closed         bool
}

type Hub struct {
	Clients         map[*Client]bool
	ChannelMembers map[string]map[*Client]bool
	ChannelSpeaker map[string]*Client
	Store          *store.Store
	BroadcastChan  chan []byte
	Register       chan *Client
	Unregister     chan *Client
	mu             sync.RWMutex
}

func NewHub(s *store.Store) *Hub {
	return &Hub{
		Clients:         make(map[*Client]bool),
		ChannelMembers: make(map[string]map[*Client]bool),
		ChannelSpeaker: make(map[string]*Client),
		Store:          s,
		BroadcastChan:  make(chan []byte, 256),
		Register:       make(chan *Client),
		Unregister:    make(chan *Client),
	}
}

func (h *Hub) Run() {
	for {
		select {
		case client := <-h.Register:
			h.mu.Lock()
			h.Clients[client] = true
			h.mu.Unlock()
			log.Printf("Cliente conectado: %s (%s)", client.IP, client.SessionID)

		case client := <-h.Unregister:
			h.mu.Lock()
			if _, ok := h.Clients[client]; ok {
				delete(h.Clients, client)
				close(client.Send)

				if client.Channel != "" {
					if members, ok := h.ChannelMembers[client.Channel]; ok {
						delete(members, client)
						if len(members) == 0 {
							delete(h.ChannelMembers, client.Channel)
						}
					}

					if speaker, ok := h.ChannelSpeaker[client.Channel]; ok && speaker == client {
						delete(h.ChannelSpeaker, client.Channel)
						h.broadcastToChannel(client.Channel, map[string]interface{}{
							"type":     "ptt_ended",
							"username": client.Username,
						}, nil)
					}
				}
			}
			h.mu.Unlock()
			log.Printf("Cliente desconectado: %s de %s", client.Username, client.Channel)

		case <-h.BroadcastChan:
			// Reserved for future use
		}
	}
}

func (h *Hub) ClientIP(client *Client) string {
	return client.IP
}

func (h *Hub) AddChannelMember(channel string, client *Client) {
	if channel == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.ChannelMembers[channel]; !ok {
		h.ChannelMembers[channel] = make(map[*Client]bool)
	}
	h.ChannelMembers[channel][client] = true
}

func (h *Hub) RemoveChannelMember(channel string, client *Client) {
	if channel == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	if members, ok := h.ChannelMembers[channel]; ok {
		delete(members, client)
		if len(members) == 0 {
			delete(h.ChannelMembers, channel)
		}
	}
}

func (h *Hub) ChannelRecipients(channel string, exclude *Client, listenersOnly bool) []*Client {
	h.mu.RLock()
	defer h.mu.RUnlock()

	var recipients []*Client
	members, ok := h.ChannelMembers[channel]
	if !ok {
		return recipients
	}

	for c := range members {
		if c == exclude {
			continue
		}
		if c != nil && c.Conn != nil && !c.closed {
			if listenersOnly && c.IsTransmitting {
				continue
			}
			recipients = append(recipients, c)
		}
	}
	return recipients
}

func (h *Hub) UsersInChannel(channel string) []string {
	h.mu.RLock()
	defer h.mu.RUnlock()

	var names []string
	members, ok := h.ChannelMembers[channel]
	if !ok {
		return names
	}

	for c := range members {
		if c != nil && c.Conn != nil && !c.closed {
			names = append(names, c.Username)
		}
	}
	return names
}

func (h *Hub) IsActiveSpeaker(client *Client) bool {
	if client.Channel == "" {
		return false
	}
	h.mu.RLock()
	defer h.mu.RUnlock()

	if speaker, ok := h.ChannelSpeaker[client.Channel]; ok {
		return speaker == client
	}
	return false
}

func (h *Hub) ReconcilePTTState() {
	h.mu.Lock()
	defer h.mu.Unlock()

	for channel, speaker := range h.ChannelSpeaker {
		if _, ok := h.Clients[speaker]; !ok || speaker.closed {
			delete(h.ChannelSpeaker, channel)
		}
	}

	for c := range h.Clients {
		if !c.closed && c.IsTransmitting && !h.isActiveSpeakerLocked(c) {
			c.IsTransmitting = false
		}
	}
}

func (h *Hub) isActiveSpeakerLocked(c *Client) bool {
	if c.Channel == "" {
		return false
	}
	if speaker, ok := h.ChannelSpeaker[c.Channel]; ok {
		return speaker == c
	}
	return false
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

func (h *Hub) ClientsSnapshot() []ClientSnapshot {
	h.ReconcilePTTState()
	h.mu.RLock()
	defer h.mu.RUnlock()

	var rows []ClientSnapshot
	for c := range h.Clients {
		if c == nil || c.Conn == nil || c.closed {
			continue
		}
		speaking := h.isActiveSpeakerLocked(c)
		rows = append(rows, ClientSnapshot{
			SessionID:      c.SessionID,
			Username:       c.Username,
			Channel:        c.Channel,
			PendingChannel: c.PendingChannel,
			IP:             c.IP,
			MAC:            c.MAC,
			DeviceID:       c.DeviceID,
			IsTransmitting: c.IsTransmitting,
			IsSpeaking:     speaking,
			ConnectedAt:    c.ConnectedAt,
		})
	}
	return rows
}

func (h *Hub) FindBySession(sessionID string) *Client {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for c := range h.Clients {
		if c != nil && c.SessionID == sessionID && c.Conn != nil && !c.closed {
			return c
		}
	}
	return nil
}

func (h *Hub) CompleteJoin(client *Client, channel string) {
	h.mu.Lock()
	oldChannel := client.Channel

	if oldChannel != "" && oldChannel != channel {
		if members, ok := h.ChannelMembers[oldChannel]; ok {
			delete(members, client)
			if len(members) == 0 {
				delete(h.ChannelMembers, oldChannel)
			}
		}
		if speaker, ok := h.ChannelSpeaker[oldChannel]; ok && speaker == client {
			delete(h.ChannelSpeaker, oldChannel)
		}
	}

	client.Channel = channel
	client.PendingChannel = ""
	client.IsTransmitting = false

	if _, ok := h.ChannelMembers[channel]; !ok {
		h.ChannelMembers[channel] = make(map[*Client]bool)
	}
	h.ChannelMembers[channel][client] = true
	h.mu.Unlock()

	h.Store.RecordDeviceChannelAccess(client.DeviceID, channel)

	enabled := h.Store.EnabledChannelNames()
	gain := h.Store.DevicePlaybackGain(client.DeviceID)

	response := map[string]interface{}{
		"type":          "joined",
		"channel":       channel,
		"channels":      enabled,
		"users":         h.UsersInChannel(channel),
		"playback_gain": gain,
		"audio_format":  "pcm",
	}
	h.SendJSON(client, response)

	h.NotifyUsers(channel)
	log.Printf("%s (%s / %s) entro a %s", client.Username, client.IP, client.MAC, channel)
}

func (h *Hub) HandleJoin(client *Client, data map[string]interface{}) {
	username := getString(data, "username")
	channel := getString(data, "channel")
	deviceID := getString(data, "device_id")

	if channel == "" {
		h.SendJSON(client, map[string]interface{}{
			"type":    "error",
			"message": "Canal requerido",
		})
		return
	}

	client.Username = username
	client.DeviceID = deviceID

	if deviceID != "" {
		h.Store.TouchDevice(deviceID, username, client.IP, client.MAC)
	}

	if h.Store.IsBlocked(username, deviceID, client.IP) {
		h.SendJSON(client, map[string]interface{}{
			"type":    "error",
			"message": "Acceso bloqueado por el administrador",
		})
		h.CloseClient(client, 4003, "Bloqueado")
		return
	}

	enabled := h.Store.EnabledChannelNames()
	if !contains(enabled, channel) {
		h.SendJSON(client, map[string]interface{}{
			"type":    "error",
			"message": "Bloque invalido. Disponibles: " + join(enabled, ", "),
		})
		return
	}

	channelInfo := h.Store.ChannelByName(channel)
	if channelInfo != nil && channelInfo.Access == "approval" {
		if !h.Store.IsDeviceApprovedForChannel(deviceID, channelInfo.ID) {
			client.Channel = ""
			client.PendingChannel = channel
			pending := h.Store.UpsertPending(
				deviceID,
				username,
				client.IP,
				client.MAC,
				channelInfo.ID,
				channel,
				client.SessionID,
			)
			h.SendJSON(client, map[string]interface{}{
				"type":      "approval_pending",
				"channel":   channel,
				"message":   "Esperando aprobacion del administrador para este bloque",
				"request_id": pending.ID,
			})
			log.Printf("Solicitud de acceso: %s -> %s (%s)", username, channel, deviceID)
			return
		}
	}

	h.CompleteJoin(client, channel)
}

func (h *Hub) HandlePTTStart(client *Client) {
	if client.Channel == "" {
		h.SendJSON(client, map[string]interface{}{
			"type":    "error",
			"message": "Debe unirse a un bloque primero",
		})
		return
	}

	if h.Store.IsBlocked(client.Username, client.DeviceID, client.IP) {
		h.SendJSON(client, map[string]interface{}{
			"type":    "error",
			"message": "Acceso bloqueado por el administrador",
		})
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	currentSpeaker := h.ChannelSpeaker[client.Channel]
	if currentSpeaker != nil && currentSpeaker != client && !currentSpeaker.closed {
		h.SendJSON(client, map[string]interface{}{
			"type":    "ptt_denied",
			"reason":  "Canal ocupado",
			"speaker": currentSpeaker.Username,
		})
		return
	}

	h.ChannelSpeaker[client.Channel] = client
	client.IsTransmitting = true

	h.SendJSON(client, map[string]interface{}{"type": "ptt_granted"})
	h.broadcastToChannel(client.Channel, map[string]interface{}{
		"type":     "ptt_started",
		"username": client.Username,
	}, client)

	log.Printf("%s transmite en %s", client.Username, client.Channel)
}

func (h *Hub) HandlePTTEnd(client *Client) {
	if client.Channel == "" {
		return
	}

	h.mu.Lock()
	if speaker, ok := h.ChannelSpeaker[client.Channel]; ok && speaker == client {
		delete(h.ChannelSpeaker, client.Channel)
	}
	h.mu.Unlock()

	if client.IsTransmitting {
		client.IsTransmitting = false
		h.broadcastToChannel(client.Channel, map[string]interface{}{
			"type":     "ptt_ended",
			"username": client.Username,
		}, nil)
	}
}

func (h *Hub) NotifyUsers(channel string) {
	users := h.UsersInChannel(channel)
	response := map[string]interface{}{
		"type":  "users_update",
		"users": users,
	}
	h.broadcastToChannel(channel, response, nil)
}

func (h *Hub) KickClient(sessionID string) bool {
	client := h.FindBySession(sessionID)
	if client != nil {
		h.CloseClient(client, 4000, "Expulsado por administrador")
		return true
	}
	return false
}

func (h *Hub) ApprovePendingRequest(pendingID string) bool {
	item := h.Store.ApprovePending(pendingID)
	if item == nil {
		return false
	}

	if item.SessionID != "" && item.ChannelName != "" {
		client := h.FindBySession(item.SessionID)
		if client != nil {
			h.CompleteJoin(client, item.ChannelName)
		}
	}
	return true
}

func (h *Hub) RejectPendingRequest(pendingID string) bool {
	item := h.Store.RemovePending(pendingID)
	if item == nil {
		return false
	}

	if item.SessionID != "" {
		client := h.FindBySession(item.SessionID)
		if client != nil {
			client.PendingChannel = ""
		}
	}
	return true
}

func (h *Hub) PushDeviceGain(deviceID string) {
	if deviceID == "" {
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	gain := h.Store.DevicePlaybackGain(deviceID)
	payload := map[string]interface{}{
		"type":          "config_update",
		"playback_gain": gain,
		"channels":      h.Store.EnabledChannelNames(),
		"audio_format":  "pcm",
	}
	msg, _ := json.Marshal(payload)

	for c := range h.Clients {
		if c != nil && c.DeviceID == deviceID && c.Conn != nil && !c.closed {
			select {
			case c.Send <- msg:
			default:
			}
		}
	}
}

func (h *Hub) BroadcastConfigUpdate() {
	h.mu.RLock()
	defer h.mu.RUnlock()

	gain := h.Store.PlaybackGain()
	channels := h.Store.EnabledChannelNames()

	payload := map[string]interface{}{
		"type":          "config_update",
		"playback_gain": gain,
		"channels":      channels,
		"audio_format":  "pcm",
	}
	msg, _ := json.Marshal(payload)

	for c := range h.Clients {
		if c != nil && c.Conn != nil && !c.closed {
			select {
			case c.Send <- msg:
			default:
			}
		}
	}
}

func (h *Hub) HandleMessage(client *Client, message []byte) {
	if isBinaryMessage(message) {
		if client.Channel != "" && client.IsTransmitting && h.IsActiveSpeaker(client) {
			h.broadcastAudio(client.Channel, client, message)
		}
		return
	}

	var data map[string]interface{}
	if err := json.Unmarshal(message, &data); err != nil {
		h.SendJSON(client, map[string]interface{}{
			"type":    "error",
			"message": "JSON invalido",
		})
		return
	}

	msgType := getString(data, "type")
	switch msgType {
	case "join":
		h.HandleJoin(client, data)
	case "ptt_start":
		h.HandlePTTStart(client)
	case "ptt_end":
		h.HandlePTTEnd(client)
	case "ping":
		h.SendJSON(client, map[string]interface{}{"type": "pong"})
	default:
		h.SendJSON(client, map[string]interface{}{
			"type":    "error",
			"message": "Tipo desconocido: " + msgType,
		})
	}
}

func isBinaryMessage(message []byte) bool {
	if len(message) == 0 {
		return false
	}
	firstByte := message[0]
	return firstByte != '{' && firstByte != '[' && firstByte != '"'
}

func (h *Hub) broadcastAudio(channel string, sender *Client, audio []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	members, ok := h.ChannelMembers[channel]
	if !ok {
		return
	}

	for c := range members {
		if c != sender && c != nil && c.Conn != nil && !c.closed && !c.IsTransmitting {
			select {
			case c.Send <- audio:
			default:
			}
		}
	}
}

func (h *Hub) broadcastToChannel(channel string, data interface{}, exclude *Client) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	msg, err := json.Marshal(data)
	if err != nil {
		return
	}

	members, ok := h.ChannelMembers[channel]
	if !ok {
		return
	}

	for c := range members {
		if c == exclude {
			continue
		}
		if c != nil && c.Conn != nil && !c.closed {
			select {
			case c.Send <- msg:
			default:
			}
		}
	}
}

func (h *Hub) SendJSON(client *Client, data interface{}) {
	msg, err := json.Marshal(data)
	if err != nil {
		return
	}
	select {
	case client.Send <- msg:
	default:
	}
}

func (h *Hub) CloseClient(client *Client, code int, reason string) {
	h.mu.Lock()
	delete(h.Clients, client)
	client.closed = true
	h.mu.Unlock()

	if client.Channel != "" {
		h.RemoveChannelMember(client.Channel, client)
	}
	if client.PendingChannel != "" && client.Channel == "" {
		h.Store.RemovePendingBySession(client.SessionID)
	}

	if client.Conn != nil {
		client.Conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason))
	}
	close(client.Send)
}

func (h *Hub) RemoveClient(client *Client) {
	h.mu.Lock()
	delete(h.Clients, client)
	client.closed = true
	if client.Channel != "" {
		if members, ok := h.ChannelMembers[client.Channel]; ok {
			delete(members, client)
			if len(members) == 0 {
				delete(h.ChannelMembers, client.Channel)
			}
		}
		if speaker, ok := h.ChannelSpeaker[client.Channel]; ok && speaker == client {
			delete(h.ChannelSpeaker, client.Channel)
		}
	}
	h.mu.Unlock()

	h.Store.RemovePendingBySession(client.SessionID)
	close(client.Send)
}

func (c *Client) ReadPump() {
	defer func() {
		c.Hub.RemoveClient(c)
		c.Conn.Close()
	}()

	c.Conn.SetReadLimit(maxMessageSize)
	c.Conn.SetReadDeadline(time.Now().Add(pongWait))
	c.Conn.SetPongHandler(func(string) error {
		c.Conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, message, err := c.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("Error de lectura: %v", err)
			}
			break
		}
		c.Hub.HandleMessage(c, message)
	}
}

func (c *Client) WritePump() {
	ticker := time.NewTicker(pingInterval)
	defer func() {
		ticker.Stop()
		c.Conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.Send:
			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.Conn.NextWriter(websocket.BinaryMessage)
			if err != nil {
				return
			}
			w.Write(message)

			n := len(c.Send)
			for i := 0; i < n; i++ {
				w.Write([]byte{'\n'})
				w.Write(<-c.Send)
			}

			if err := w.Close(); err != nil {
				return
			}

		case <-ticker.C:
			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
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

func join(slice []string, sep string) string {
	if len(slice) == 0 {
		return ""
	}
	result := slice[0]
	for i := 1; i < len(slice); i++ {
		result += sep + slice[i]
	}
	return result
}

func GetClientIP(conn *websocket.Conn) string {
	if conn != nil {
		if remote := conn.RemoteAddr(); remote != nil {
			return remote.String()
		}
	}
	return ""
}

func LookupMAC(ip string) string {
	return utils.LookupMAC(ip)
}
