package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

var (
	numClients    = 10
	serverURL     = "ws://localhost:8765"
	channel       = "CANAL LIBRE"
	dialer        = websocket.Dialer{
		ReadBufferSize:  1024 * 1024,
		WriteBufferSize: 1024 * 1024,
	}
)

type Client struct {
	ID                 int
	Username           string
	Conn               *websocket.Conn
	Connected          bool
	Joined             bool
	Channel            string
	IsTransmitting     bool
	SessionID          string
	AudioChunksSent    int
	MessagesReceived   int
	Errors             []string
	mu                 sync.Mutex
}

type Results struct {
	ServerURL         string          `json:"server_url"`
	NumClients        int             `json:"num_clients"`
	Channel           string          `json:"channel"`
	StartTime         string          `json:"start_time"`
	EndTime           string          `json:"end_time"`
	DurationSeconds   float64         `json:"duration_seconds"`
	ConnectionStats   Stats           `json:"connection_stats"`
	JoinStats         Stats           `json:"join_stats"`
	TransmissionStats Stats           `json:"transmission_stats"`
	AudioStats        AudioStats      `json:"audio_stats"`
	ClientDetails     []ClientDetail  `json:"client_details"`
	Errors            []string        `json:"errors"`
	Success           bool            `json:"success"`
}

type Stats struct {
	Total      int `json:"total"`
	Successful int `json:"successful"`
	Failed     int `json:"failed"`
	Denied     int `json:"denied,omitempty"`
}

type AudioStats struct {
	TotalChunksSent     int `json:"total_chunks_sent"`
	TotalChunksReceived int `json:"total_chunks_received"`
}

type ClientDetail struct {
	ID               int      `json:"id"`
	Username         string   `json:"username"`
	Connected        bool     `json:"connected"`
	Joined           bool     `json:"joined"`
	Channel          string   `json:"channel"`
	IsTransmitting   bool     `json:"is_transmitting"`
	SessionID        string   `json:"session_id"`
	AudioChunksSent  int      `json:"audio_chunks_sent"`
	MessagesReceived int      `json:"messages_received"`
	Errors           []string `json:"errors"`
}

func generateAudioChunk(size int) []byte {
	chunk := make([]byte, size)
	rand.Read(chunk)
	return chunk
}

func connectClient(c *Client, wg *sync.WaitGroup) {
	defer wg.Done()
	
	log.Printf("[Client %d] Intentando conectar...", c.ID)
	
	conn, _, err := dialer.Dial(serverURL, nil)
	if err != nil {
		c.Errors = append(c.Errors, fmt.Sprintf("Conexión: %v", err))
		log.Printf("[Client %d] Error de conexión: %v", c.ID, err)
		return
	}
	
	c.Conn = conn
	c.Connected = true
	log.Printf("[Client %d] Conectado exitosamente", c.ID)
}

func joinChannel(c *Client, wg *sync.WaitGroup) {
	defer wg.Done()
	
	if !c.Connected || c.Conn == nil {
		return
	}
	
	log.Printf("[Client %d] Uniendo al canal %s...", c.ID, channel)
	
	joinMsg := map[string]interface{}{
		"type":      "join",
		"username":  c.Username,
		"channel":   channel,
		"device_id": fmt.Sprintf("test-device-%d", c.ID),
		"mac":       fmt.Sprintf("00:11:22:33:44:%02x", c.ID),
	}
	
	msgBytes, _ := json.Marshal(joinMsg)
	if err := c.Conn.WriteMessage(websocket.TextMessage, msgBytes); err != nil {
		c.Errors = append(c.Errors, fmt.Sprintf("Join send: %v", err))
		return
	}
	
	// Esperar respuesta
	c.Conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := c.Conn.ReadMessage()
	if err != nil {
		c.Errors = append(c.Errors, fmt.Sprintf("Join recv: %v", err))
		return
	}
	
	var response map[string]interface{}
	if err := json.Unmarshal(msg, &response); err != nil {
		c.Errors = append(c.Errors, fmt.Sprintf("Join parse: %v", err))
		return
	}
	
	if response["type"] == "joined" {
		c.Joined = true
		c.Channel = channel
		if sid, ok := response["session_id"].(string); ok {
			c.SessionID = sid
		}
		log.Printf("[Client %d] Unido exitosamente al canal", c.ID)
	} else if response["type"] == "approval_pending" {
		c.Errors = append(c.Errors, "Canal requiere aprobación")
		log.Printf("[Client %d] Canal requiere aprobación", c.ID)
	} else {
		c.Errors = append(c.Errors, fmt.Sprintf("Tipo inesperado: %v", response["type"]))
	}
}

func receiveMessages(c *Client, done chan<- struct{}) {
	defer func() { done <- struct{}{} }()
	
	for c.Connected && c.Conn != nil {
		c.Conn.SetReadDeadline(time.Now().Add(1 * time.Second))
		msgType, msg, err := c.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				c.Errors = append(c.Errors, fmt.Sprintf("Receive: %v", err))
			}
			return
		}
		
		c.mu.Lock()
		c.MessagesReceived++
		c.mu.Unlock()
		
		if msgType == websocket.BinaryMessage {
			// Audio recibido
		} else {
			// Mensaje de texto
			var data map[string]interface{}
			json.Unmarshal(msg, &data)
			_ = data // Para evitar error de variable no usada
		}
	}
}

func startTransmission(c *Client) bool {
	if !c.Joined || c.Conn == nil {
		return false
	}
	
	c.Conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	
	pttMsg, _ := json.Marshal(map[string]string{"type": "ptt_start"})
	if err := c.Conn.WriteMessage(websocket.TextMessage, pttMsg); err != nil {
		c.Errors = append(c.Errors, fmt.Sprintf("PTT start: %v", err))
		return false
	}
	
	_, msg, err := c.Conn.ReadMessage()
	if err != nil {
		c.Errors = append(c.Errors, fmt.Sprintf("PTT start recv: %v", err))
		return false
	}
	
	var response map[string]interface{}
	json.Unmarshal(msg, &response)
	
	if response["type"] == "ptt_granted" {
		c.IsTransmitting = true
		return true
	}
	return false
}

func stopTransmission(c *Client) {
	if c.Conn == nil {
		return
	}
	
	pttMsg, _ := json.Marshal(map[string]string{"type": "ptt_end"})
	c.Conn.WriteMessage(websocket.TextMessage, pttMsg)
	c.IsTransmitting = false
}

func sendAudioChunks(c *Client, numChunks int, delay time.Duration) {
	for i := 0; i < numChunks && c.IsTransmitting; i++ {
		chunk := generateAudioChunk(640)
		if err := c.Conn.WriteMessage(websocket.BinaryMessage, chunk); err != nil {
			c.Errors = append(c.Errors, fmt.Sprintf("Audio send: %v", err))
			break
		}
		c.mu.Lock()
		c.AudioChunksSent++
		c.mu.Unlock()
		time.Sleep(delay)
	}
}

func disconnectClient(c *Client) {
	if c.Conn != nil {
		c.Conn.Close()
	}
	c.Connected = false
	c.Joined = false
}

func main() {
	rand.Seed(time.Now().UnixNano())
	
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	
	fmt.Println("============================================================")
	fmt.Println("INICIANDO PRUEBA DE CARGA PTT (Go)")
	fmt.Printf("Servidor: %s\n", serverURL)
	fmt.Printf("Clientes: %d\n", numClients)
	fmt.Printf("Canal: %s\n", channel)
	fmt.Println("============================================================")
	fmt.Println()
	
	results := Results{
		ServerURL:   serverURL,
		NumClients:  numClients,
		Channel:     channel,
		StartTime:   time.Now().Format(time.RFC3339),
	}
	
	start := time.Now()
	
	// Crear clientes
	fmt.Printf("[1/5] Creando %d clientes...\n", numClients)
	clients := make([]*Client, numClients)
	for i := 0; i < numClients; i++ {
		clients[i] = &Client{
			ID:       i + 1,
			Username: fmt.Sprintf("TestUser%02d", i+1),
		}
	}
	
	// Conectar clientes
	fmt.Println("[2/5] Conectando clientes al servidor...")
	var wg sync.WaitGroup
	for _, c := range clients {
		wg.Add(1)
		go connectClient(c, &wg)
	}
	wg.Wait()
	
	connectedCount := 0
	for _, c := range clients {
		if c.Connected {
			connectedCount++
			results.ConnectionStats.Successful++
		} else {
			results.ConnectionStats.Failed++
		}
	}
	results.ConnectionStats.Total = numClients
	fmt.Printf("       Conectados: %d/%d\n", connectedCount, numClients)
	
	// Join al canal
	fmt.Println("[3/5] Uniendo clientes al canal...")
	for _, c := range clients {
		if c.Connected {
			wg.Add(1)
			go joinChannel(c, &wg)
		}
	}
	wg.Wait()
	
	joinedCount := 0
	for _, c := range clients {
		if c.Joined {
			joinedCount++
			results.JoinStats.Successful++
		} else {
			results.JoinStats.Failed++
		}
	}
	results.JoinStats.Total = connectedCount
	fmt.Printf("       Unidos: %d/%d\n", joinedCount, connectedCount)
	
	// Iniciar receptores de mensajes
	fmt.Println("[4/5] Iniciando receptores de mensajes...")
	receiveDone := make([]chan struct{}, len(clients))
	receiveWg := sync.NewCond(&sync.Mutex{})
	receiveCount := 0
	
	for i, c := range clients {
		if c.Joined {
			receiveDone[i] = make(chan struct{})
			go func(c *Client, done chan struct{}) {
				receiveMessages(c, done)
				receiveWg.L.Lock()
				receiveCount++
				receiveWg.L.Unlock()
				receiveWg.Broadcast()
			}(c, receiveDone[i])
		}
	}
	
	// Simular transmisiones
	fmt.Println("[5/5] Simulando transmisiones PTT...")
	
	for _, c := range clients {
		if !c.Joined {
			continue
		}
		
		fmt.Printf("       Cliente %d (%s) transmitiendo...\n", c.ID, c.Username)
		
		if startTransmission(c) {
			sendAudioChunks(c, 5, 50*time.Millisecond)
			stopTransmission(c)
			fmt.Printf("       -> Transmisión completada (%d chunks)\n", c.AudioChunksSent)
			results.TransmissionStats.Successful++
		} else {
			fmt.Printf("       -> Transmisión denegada (canal ocupado)\n")
			results.TransmissionStats.Denied++
		}
		
		results.TransmissionStats.Total++
		time.Sleep(200 * time.Millisecond)
	}
	
	// Esperar recepción de mensajes
	fmt.Println("       Esperando recepción de mensajes...")
	time.Sleep(1 * time.Second)
	
	// Detener receptores
	for i, done := range receiveDone {
		if done != nil {
			disconnectClient(clients[i])
			select {
			case <-done:
			case <-time.After(1 * time.Second):
			}
		}
	}
	
	// Desconectar clientes
	fmt.Println("       Desconectando clientes...")
	for _, c := range clients {
		disconnectClient(c)
	}
	
	end := time.Now()
	results.EndTime = end.Format(time.RFC3339)
	results.DurationSeconds = end.Sub(start).Seconds()
	
	// Recopilar estadísticas de audio
	for _, c := range clients {
		results.AudioStats.TotalChunksSent += c.AudioChunksSent
		results.AudioStats.TotalChunksReceived += c.MessagesReceived
	}
	
	// Compilar detalles de clientes
	for _, c := range clients {
		detail := ClientDetail{
			ID:               c.ID,
			Username:         c.Username,
			Connected:        c.Connected,
			Joined:           c.Joined,
			Channel:          c.Channel,
			IsTransmitting:   c.IsTransmitting,
			SessionID:        c.SessionID,
			AudioChunksSent:  c.AudioChunksSent,
			MessagesReceived: c.MessagesReceived,
			Errors:           c.Errors,
		}
		results.ClientDetails = append(results.ClientDetails, detail)
		
		for _, err := range c.Errors {
			results.Errors = append(results.Errors, fmt.Sprintf("Cliente %d: %s", c.ID, err))
		}
	}
	
	// Determinar éxito
	results.Success = results.ConnectionStats.Successful == numClients &&
		results.JoinStats.Successful > 0 &&
		len(results.Errors) < numClients/2
	
	// Imprimir resumen
	fmt.Println()
	fmt.Println("============================================================")
	fmt.Println("RESUMEN DE PRUEBA")
	fmt.Println("============================================================")
	fmt.Printf("Servidor: %s\n", results.ServerURL)
	fmt.Printf("Duración: %.2f segundos\n", results.DurationSeconds)
	fmt.Println()
	fmt.Println("--- Conexiones ---")
	fmt.Printf("Intentos: %d\n", results.ConnectionStats.Total)
	fmt.Printf("Exitosos: %d\n", results.ConnectionStats.Successful)
	fmt.Printf("Fallidos: %d\n", results.ConnectionStats.Failed)
	fmt.Println()
	fmt.Println("--- Unirse al canal ---")
	fmt.Printf("Intentos: %d\n", results.JoinStats.Total)
	fmt.Printf("Exitosos: %d\n", results.JoinStats.Successful)
	fmt.Printf("Fallidos: %d\n", results.JoinStats.Failed)
	fmt.Println()
	fmt.Println("--- Transmisiones PTT ---")
	fmt.Printf("Total: %d\n", results.TransmissionStats.Total)
	fmt.Printf("Concedidas: %d\n", results.TransmissionStats.Successful)
	fmt.Printf("Denegadas: %d\n", results.TransmissionStats.Denied)
	fmt.Println()
	fmt.Println("--- Audio ---")
	fmt.Printf("Chunks enviados: %d\n", results.AudioStats.TotalChunksSent)
	fmt.Printf("Chunks recibidos: %d\n", results.AudioStats.TotalChunksReceived)
	
	if len(results.Errors) > 0 {
		fmt.Println()
		fmt.Printf("--- Errores (%d) ---\n", len(results.Errors))
		for _, err := range results.Errors {
			if len(results.Errors) <= 10 {
				fmt.Printf("  - %s\n", err)
			}
		}
		if len(results.Errors) > 10 {
			fmt.Printf("  ... y %d errores más\n", len(results.Errors)-10)
		}
	}
	
	fmt.Println()
	fmt.Println("============================================================")
	if results.Success {
		fmt.Println("RESULTADO: ✓ EXITOSO")
	} else {
		fmt.Println("RESULTADO: ✗ FALLIDO")
	}
	fmt.Println("============================================================")
	fmt.Println()
	
	// Guardar resultados
	outputFile := "test_results_go.json"
	file, err := os.Create(outputFile)
	if err != nil {
		log.Fatalf("Error creando archivo de resultados: %v", err)
	}
	defer file.Close()
	
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(results); err != nil {
		log.Fatalf("Error guardando resultados: %v", err)
	}
	
	fmt.Printf("Resultados guardados en: %s\n", outputFile)
	
	// Esperar señal de terminación
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
}
