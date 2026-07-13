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

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("Iniciando servidor PTT...")

	dataDir := getDataDir()
	staticDir := getStaticDir()

	s := store.New(dataDir)
	if err := s.Load(); err != nil {
		log.Fatalf("Error cargando configuracion: %v", err)
	}

	// Crear estado del servidor (similar a variables globales en Python)
	serverState := ws.NewServerState(s)

	mux := http.NewServeMux()

	mux.HandleFunc("/", wsIndexHandler(staticDir))
	mux.HandleFunc("/admin", wsIndexHandler(staticDir))
	mux.HandleFunc("/static/", staticHandler(staticDir))
	mux.HandleFunc("/api/login", loginHandler(s))
	mux.HandleFunc("/api/public/info", publicInfoHandler(s))
	mux.HandleFunc("/api/status", statusHandler(s, serverState))
	mux.HandleFunc("/api/settings/gain", setGainHandler(s, serverState))
	mux.HandleFunc("/api/devices/", deviceGainHandler(s, serverState))
	mux.HandleFunc("/api/channels", channelsHandler(s, serverState))
	mux.HandleFunc("/api/channels/", channelHandler(s, serverState))
	mux.HandleFunc("/api/blocked", blockedHandler(s))
	mux.HandleFunc("/api/blocked/", removeBlockHandler(s))
	mux.HandleFunc("/api/kick/", kickHandler(serverState))
	mux.HandleFunc("/api/approvals/", approvalHandler(serverState))

	wsAddr := fmt.Sprintf("%s:%d", config.Host, config.Port)
	adminAddr := fmt.Sprintf("%s:%d", config.Host, config.AdminPort)

	// Crear servidor WebSocket - patrón similar a Python
	wsServer := &http.Server{
		Addr:    wsAddr,
		Handler: wsHandler(serverState),
	}

	go func() {
		log.Printf("WebSocket server.listenAndServe on %s", wsAddr)
		if err := wsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
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
	wsServer.Close()
	if err := s.Flush(); err != nil {
		log.Printf("Error guardando configuracion: %v", err)
	}
	log.Println("Servidor detenido")
}

// wsHandler - maneja conexiones WebSocket (patrón similar a Python async def handler)
func wsHandler(state *ws.ServerState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("[WS] Error de upgrade WebSocket: %v", err)
			return
		}

		// Extraer IP del cliente
		ip := ""
		if remote := conn.RemoteAddr(); remote != nil {
			addr := remote.String()
			if idx := strings.LastIndex(addr, ":"); idx > 0 {
				ip = addr[:idx]
			}
		}

		// Manejar conexión en goroutine (similar a como Python maneja async)
		go state.HandleConnection(conn, ip)
	}
}

func getDataDir() string {
	if dir := os.Getenv("DATA_DIR"); dir != "" {
		log.Printf("[DEBUG] DATA_DIR env: %s", dir)
		return dir
	}
	// Usar directorio del ejecutable, no el directorio de trabajo actual
	execPath, err := os.Executable()
	if err != nil {
		log.Printf("[DEBUG] No se pudo obtener ruta del ejecutable, usando: %s", config.DataDir)
		return config.DataDir
	}
	execDir := filepath.Dir(execPath)
	dataDir := filepath.Join(execDir, config.DataDir)
	log.Printf("[DEBUG] Ejecutable: %s", execPath)
	log.Printf("[DEBUG] Directorio de datos: %s", dataDir)
	return dataDir
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
		filePath := filepath.Join(staticDir, path)

		// Establecer Content-Type correcto
		switch {
		case strings.HasSuffix(path, ".css"):
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
		case strings.HasSuffix(path, ".js"):
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		case strings.HasSuffix(path, ".html"):
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		case strings.HasSuffix(path, ".json"):
			w.Header().Set("Content-Type", "application/json")
		case strings.HasSuffix(path, ".png"):
			w.Header().Set("Content-Type", "image/png")
		case strings.HasSuffix(path, ".jpg"), strings.HasSuffix(path, ".jpeg"):
			w.Header().Set("Content-Type", "image/jpeg")
		case strings.HasSuffix(path, ".ico"):
			w.Header().Set("Content-Type", "image/x-icon")
		}

		log.Printf("[DEBUG] Sirviendo archivo: %s", filePath)
		http.ServeFile(w, r, filePath)
	}
}

func loginHandler(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log.Printf("[API] POST /api/login desde %s", r.RemoteAddr)
		if r.Method != "POST" {
			http.Error(w, "Method not allowed", 405)
			return
		}

		var data map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
			log.Printf("[API] /api/login - JSON invalido: %v", err)
			http.Error(w, "Invalid JSON", 400)
			return
		}

		password, _ := data["password"].(string)
		log.Printf("[API] /api/login - Intentando password: %s", password)
		if !s.VerifyPassword(password) {
			log.Printf("[API] /api/login - Clave incorrecta")
			http.Error(w, `{"error":"Clave incorrecta"}`, 403)
			return
		}

		log.Printf("[API] /api/login - Login exitoso!")
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

func statusHandler(s *store.Store, state *ws.ServerState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-Admin-Token")
		if !s.VerifyPassword(token) {
			http.Error(w, `{"error":"No autorizado"}`, 401)
			return
		}

		clients := state.GetClientsSnapshot()

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"ok":true,"clients":%s}`, toJSON(clients))
	}
}

func setGainHandler(s *store.Store, state *ws.ServerState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-Admin-Token")
		if !s.VerifyPassword(token) {
			http.Error(w, `{"error":"No autorizado"}`, 401)
			return
		}

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

		if gain != nil {
			s.SetPlaybackGain(*gain)
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"playback_gain":%f}`, s.PlaybackGain())
	}
}

func deviceGainHandler(s *store.Store, state *ws.ServerState) http.HandlerFunc {
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

func channelsHandler(s *store.Store, state *ws.ServerState) http.HandlerFunc {
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

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(map[string]interface{}{"channel": channel})
			return
		}

		http.NotFound(w, r)
	}
}

func channelHandler(s *store.Store, state *ws.ServerState) http.HandlerFunc {
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

func kickHandler(state *ws.ServerState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionID := strings.TrimPrefix(r.URL.Path, "/api/kick/")

		if r.Method == "POST" {
			if !state.KickClient(sessionID) {
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

func approvalHandler(state *ws.ServerState) http.HandlerFunc {
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
			success = state.ApprovePending(pendingID)
		} else if action == "reject" && r.Method == "POST" {
			success = state.RejectPending(pendingID)
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

