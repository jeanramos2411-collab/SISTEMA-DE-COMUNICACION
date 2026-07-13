package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"ptt-server/internal/config"
	ws "ptt-server/internal/websocket"
	"ptt-server/internal/store"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func wsHandler(hub *ws.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("Error de upgrade WebSocket: %v", err)
			return
		}

		ip := ""
		if remote := conn.RemoteAddr(); remote != nil {
			addr := remote.String()
			if idx := strings.LastIndex(addr, ":"); idx > 0 {
				ip = addr[:idx]
			}
		}

		mac := ws.LookupMAC(ip)
		sessionID := generateSessionID()

		client := &ws.Client{
			Hub:         hub,
			Conn:        conn,
			Send:        make(chan []byte, 256),
			SessionID:   sessionID,
			Username:    "Usuario",
			IP:          ip,
			MAC:         mac,
			ConnectedAt: time.Now().UTC().Format(time.RFC3339),
		}

		hub.Register <- client

		go client.WritePump()
		go client.ReadPump()
	}
}

func generateSessionID() string {
	return uuid.New().String()[:8]
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("Iniciando servidor PTT...")

	dataDir := getDataDir()
	staticDir := getStaticDir()

	s := store.New(dataDir)
	if err := s.Load(); err != nil {
		log.Fatalf("Error cargando configuracion: %v", err)
	}

	hub := ws.NewHub(s)
	go hub.Run()

	mux := http.NewServeMux()

	mux.HandleFunc("/", wsIndexHandler(staticDir))
	mux.HandleFunc("/admin", wsIndexHandler(staticDir))
	mux.HandleFunc("/static/", staticHandler(staticDir))
	mux.HandleFunc("/api/login", loginHandler(s))
	mux.HandleFunc("/api/public/info", publicInfoHandler(s))
	mux.HandleFunc("/api/status", statusHandler(s, hub))
	mux.HandleFunc("/api/settings/gain", setGainHandler(s, hub))
	mux.HandleFunc("/api/devices/", deviceGainHandler(s, hub))
	mux.HandleFunc("/api/channels", channelsHandler(s, hub))
	mux.HandleFunc("/api/channels/", channelHandler(s, hub))
	mux.HandleFunc("/api/blocked", blockedHandler(s))
	mux.HandleFunc("/api/blocked/", removeBlockHandler(s))
	mux.HandleFunc("/api/kick/", kickHandler(hub))
	mux.HandleFunc("/api/approvals/", approvalHandler(hub))

	wsAddr := fmt.Sprintf("%s:%d", config.Host, config.Port)
	adminAddr := fmt.Sprintf("%s:%d", config.Host, config.AdminPort)

	// Crear un ServeMux separado para WebSocket en puerto 8765
	// La app Android se conecta a ws://IP:8765 (en raíz /)
	wsMux := http.NewServeMux()
	wsMux.HandleFunc("/", wsHandler(hub)) // Maneja WebSocket en cualquier ruta (/, /ws, etc.)

	go func() {
		log.Printf("WebSocket server.listenAndServe on %s", wsAddr)
		if err := http.ListenAndServe(wsAddr, wsMux); err != nil {
			log.Fatalf("Error en WebSocket server: %v", err)
		}
	}()

	go func() {
		log.Printf("Panel admin: http://localhost:%d/admin", config.AdminPort)
		log.Printf("Admin server.listenAndServe on %s", adminAddr)
		if err := http.ListenAndServe(adminAddr, mux); err != nil {
			log.Printf("Admin server ended: %v", err)
		}
	}()

	enabled := s.EnabledChannelNames()
	log.Printf("Servidor PTT iniciado en %s:%d", config.Host, config.Port)
	log.Printf("Panel admin: http://<IP-DE-ESTA-PC>:%d/admin", config.AdminPort)
	log.Printf("Clave admin por defecto: %s (cambiar en data/config.json)", s.GetConfig().AdminPassword)
	log.Printf("Bloques activos: %s", strings.Join(enabled, ", "))

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Deteniendo servidor...")
	if err := s.Flush(); err != nil {
		log.Printf("Error guardando configuracion: %v", err)
	}
	log.Println("Servidor detenido")
}

func getDataDir() string {
	if dir := os.Getenv("DATA_DIR"); dir != "" {
		return dir
	}
	// Usar directorio del ejecutable, no el directorio de trabajo actual
	execPath, err := os.Executable()
	if err != nil {
		return config.DataDir
	}
	execDir := filepath.Dir(execPath)
	return filepath.Join(execDir, config.DataDir)
}

func getStaticDir() string {
	if dir := os.Getenv("STATIC_DIR"); dir != "" {
		return dir
	}
	execPath, err := os.Executable()
	if err != nil {
		return "static"
	}
	execDir := filepath.Dir(execPath)
	return filepath.Join(execDir, "static")
}

func wsIndexHandler(staticDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, staticDir+"/admin.html")
	}
}

func staticHandler(staticDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/static/")
		http.ServeFile(w, r, staticDir+"/"+path)
	}
}

func loginHandler(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "Method not allowed", 405)
			return
		}

		var data map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
			http.Error(w, "Invalid JSON", 400)
			return
		}

		password, _ := data["password"].(string)
		if !s.VerifyPassword(password) {
			http.Error(w, `{"error":"Clave incorrecta"}`, 403)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"ok":true,"token":"%s"}`, password)
	}
}

func publicInfoHandler(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		channels := s.EnabledChannelNames()
		fmt.Fprintf(w, `{"ok":true,"service":"ptt-comunicacion","channels":%s,"audio_format":"pcm"}`, toJSON(channels))
	}
}

func statusHandler(s *store.Store, hub *ws.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-Admin-Token")
		if !s.VerifyPassword(token) {
			http.Error(w, `{"error":"No autorizado"}`, 401)
			return
		}

		onlineByChannel := make(map[string][]map[string]interface{})
		for _, client := range hub.ClientsSnapshot() {
			key := ""
			if client.Channel != "" {
				key = client.Channel
			} else if client.PendingChannel != "" {
				key = client.PendingChannel
			}
			if key != "" {
				clientMap := map[string]interface{}{
					"session_id":       client.SessionID,
					"username":         client.Username,
					"channel":          client.Channel,
					"pending_channel":  client.PendingChannel,
					"ip":               client.IP,
					"mac":              client.MAC,
					"device_id":        client.DeviceID,
					"is_transmitting":  client.IsTransmitting,
					"is_speaking":      client.IsSpeaking,
					"connected_at":     client.ConnectedAt,
				}
				onlineByChannel[key] = append(onlineByChannel[key], clientMap)
			}
		}

		groups := s.DevicesByChannel()
		for i := range groups {
			if online, ok := onlineByChannel[groups[i].ChannelName]; ok {
				groups[i].Online = online
			}
		}

		cfg := s.GetConfig()
		response := map[string]interface{}{
			"clients": hub.ClientsSnapshot(),
			"config": map[string]interface{}{
				"playback_gain":     s.PlaybackGain(),
				"channels":         cfg.Channels,
				"blocked":          cfg.Blocked,
				"devices":          s.ListDevices(),
				"pending_approvals": cfg.PendingApprovals,
				"groups":           groups,
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}
}

func setGainHandler(s *store.Store, hub *ws.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-Admin-Token")
		if !s.VerifyPassword(token) {
			http.Error(w, `{"error":"No autorizado"}`, 401)
			return
		}

		if r.Method != "PUT" {
			http.Error(w, "Method not allowed", 405)
			return
		}

		var data map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
			http.Error(w, "Invalid JSON", 400)
			return
		}

		gain, ok := data["playback_gain"].(float64)
		if !ok {
			gain = 3.0
		}

		s.SetPlaybackGain(gain)
		hub.BroadcastConfigUpdate()

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"playback_gain":%f}`, gain)
	}
}

func deviceGainHandler(s *store.Store, hub *ws.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-Admin-Token")
		if !s.VerifyPassword(token) {
			http.Error(w, `{"error":"No autorizado"}`, 401)
			return
		}

		path := strings.TrimPrefix(r.URL.Path, "/api/devices/")
		parts := strings.SplitN(path, "/", 2)
		if len(parts) < 1 {
			http.Error(w, "Invalid path", 400)
			return
		}

		deviceID := parts[0]
		if !strings.HasSuffix(r.URL.Path, "/gain") && r.Method == "PUT" {
			deviceID = parts[0]
		}

		if r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/gain") {
			var data map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
				http.Error(w, "Invalid JSON", 400)
				return
			}

			var gain *float64
			if v, ok := data["playback_gain"]; ok && v != nil {
				if g, ok := v.(float64); ok {
					gain = &g
				}
			}

			entry, err := s.SetDevicePlaybackGain(deviceID, gain)
			if err != nil {
				http.Error(w, `{"error":"`+err.Error()+`"}`, 400)
				return
			}

			hub.PushDeviceGain(deviceID)

			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"device_id":"%s","playback_gain":%v,"effective_gain":%f}`,
				deviceID,
				pointerToJSON(entry.PlaybackGain),
				s.DevicePlaybackGain(deviceID))
			return
		}

		http.NotFound(w, r)
	}
}

func channelsHandler(s *store.Store, hub *ws.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-Admin-Token")
		if !s.VerifyPassword(token) {
			http.Error(w, `{"error":"No autorizado"}`, 401)
			return
		}

		if r.Method == "POST" {
			var data map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
				http.Error(w, "Invalid JSON", 400)
				return
			}

			name, _ := data["name"].(string)
			access, _ := data["access"].(string)
			if access == "" {
				access = "open"
			}

			channel, err := s.AddChannel(name, access)
			if err != nil {
				http.Error(w, `{"error":"`+err.Error()+`"}`, 400)
				return
			}

			hub.BroadcastConfigUpdate()

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(map[string]interface{}{"channel": channel})
			return
		}

		http.NotFound(w, r)
	}
}

func channelHandler(s *store.Store, hub *ws.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-Admin-Token")
		if !s.VerifyPassword(token) {
			http.Error(w, `{"error":"No autorizado"}`, 401)
			return
		}

		path := strings.TrimPrefix(r.URL.Path, "/api/channels/")
		parts := strings.SplitN(path, "/", 2)
		channelID := parts[0]
		isMemberOp := len(parts) > 1 && strings.HasPrefix(parts[1], "members/")

		if isMemberOp && r.Method == "DELETE" {
			memberParts := strings.SplitN(parts[1], "/", 3)
			if len(memberParts) >= 3 {
				deviceID := memberParts[2]
				if err := s.RevokeDeviceFromChannel(deviceID, channelID); err != nil {
					http.Error(w, `{"error":"`+err.Error()+`"}`, 404)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"ok":true}`)
				return
			}
		}

		switch r.Method {
		case "PUT":
			var data map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
				http.Error(w, "Invalid JSON", 400)
				return
			}

			var name *string
			if n, ok := data["name"].(string); ok {
				name = &n
			}

			var enabled *bool
			if e, ok := data["enabled"].(bool); ok {
				enabled = &e
			}

			var access *bool
			if a, ok := data["access"].(string); ok {
				isApproval := a == "approval"
				access = &isApproval
			}

			channel, err := s.UpdateChannel(channelID, name, enabled, access)
			if err != nil {
				if strings.Contains(err.Error(), "no encontrado") {
					http.Error(w, `{"error":"Bloque no encontrado"}`, 404)
				} else {
					http.Error(w, `{"error":"`+err.Error()+`"}`, 400)
				}
				return
			}

			hub.BroadcastConfigUpdate()

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"channel": channel})

		case "DELETE":
			if err := s.DeleteChannel(channelID); err != nil {
				if strings.Contains(err.Error(), "no encontrado") {
					http.Error(w, `{"error":"Bloque no encontrado"}`, 404)
				} else {
					http.Error(w, `{"error":"`+err.Error()+`"}`, 400)
				}
				return
			}

			hub.BroadcastConfigUpdate()

			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"ok":true}`)

		default:
			http.NotFound(w, r)
		}
	}
}

func blockedHandler(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-Admin-Token")
		if !s.VerifyPassword(token) {
			http.Error(w, `{"error":"No autorizado"}`, 401)
			return
		}

		if r.Method == "POST" {
			var data map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
				http.Error(w, "Invalid JSON", 400)
				return
			}

			blockType, _ := data["type"].(string)
			value, _ := data["value"].(string)
			reason, _ := data["reason"].(string)

			entry, err := s.AddBlock(blockType, value, reason)
			if err != nil {
				http.Error(w, `{"error":"`+err.Error()+`"}`, 400)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(map[string]interface{}{"blocked": entry})
			return
		}

		http.NotFound(w, r)
	}
}

func removeBlockHandler(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-Admin-Token")
		if !s.VerifyPassword(token) {
			http.Error(w, `{"error":"No autorizado"}`, 401)
			return
		}

		blockID := strings.TrimPrefix(r.URL.Path, "/api/blocked/")

		if r.Method == "DELETE" {
			if err := s.RemoveBlock(blockID); err != nil {
				if strings.Contains(err.Error(), "no encontrado") {
					http.Error(w, `{"error":"Bloqueo no encontrado"}`, 404)
				} else {
					http.Error(w, `{"error":"`+err.Error()+`"}`, 400)
				}
				return
			}

			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"ok":true}`)
			return
		}

		http.NotFound(w, r)
	}
}

func kickHandler(hub *ws.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionID := strings.TrimPrefix(r.URL.Path, "/api/kick/")

		if r.Method == "POST" {
			if !hub.KickClient(sessionID) {
				http.Error(w, `{"error":"Usuario no encontrado"}`, 404)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"ok":true}`)
			return
		}

		http.NotFound(w, r)
	}
}

func approvalHandler(hub *ws.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/approvals/")
		parts := strings.SplitN(path, "/", 2)
		if len(parts) < 2 {
			http.NotFound(w, r)
			return
		}

		pendingID := parts[0]
		action := parts[1]

		var success bool
		if action == "approve" && r.Method == "POST" {
			success = hub.ApprovePendingRequest(pendingID)
		} else if action == "reject" && r.Method == "POST" {
			success = hub.RejectPendingRequest(pendingID)
		} else {
			http.NotFound(w, r)
			return
		}

		if !success {
			http.Error(w, `{"error":"Solicitud no encontrada"}`, 404)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"ok":true}`)
	}
}

func adminServer(w http.ResponseWriter, r *http.Request, s *store.Store, hub *ws.Hub, staticDir string) {
	switch r.URL.Path {
	case "/", "/admin":
		http.ServeFile(w, r, staticDir+"/admin.html")
	default:
		http.NotFound(w, r)
	}
}

func toJSON(v interface{}) string {
	data, _ := json.Marshal(v)
	return string(data)
}

func pointerToJSON(v *float64) string {
	if v == nil {
		return "null"
	}
	return fmt.Sprintf("%f", *v)
}
