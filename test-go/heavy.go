package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

var (
	numChannels         = 10
	receiversPerChannel = 40
	serverURL          = "ws://localhost:8765"
	dialer             = websocket.Dialer{
		ReadBufferSize:  1024 * 1024,
		WriteBufferSize: 1024 * 1024,
	}
)

type Client struct {
	ID                int
	Username          string
	Channel           string
	IsTransmitter     bool
	Conn              *websocket.Conn
	Connected         bool
	Joined            bool
	AudioChunksSent   int
	AudioChunksRecv   int
	Errors            []string
	mu                sync.Mutex
}

type Results struct {
	ServerURL              string            `json:"server_url"`
	NumChannels            int              `json:"num_channels"`
	ReceiversPerChannel   int              `json:"receivers_per_channel"`
	TotalClients          int              `json:"total_clients"`
	StartTime             string           `json:"start_time"`
	EndTime               string           `json:"end_time"`
	DurationSeconds       float64          `json:"duration_seconds"`
	ConnectionStats       Stats            `json:"connection_stats"`
	JoinStats             Stats            `json:"join_stats"`
	TransmissionStats     Stats            `json:"transmission_stats"`
	AudioStats            AudioStats       `json:"audio_stats"`
	ChannelStats          map[string]ChStats `json:"channel_stats"`
	Success               bool             `json:"success"`
	Errors                []string         `json:"errors"`
}

type Stats struct {
	Total      int `json:"total"`
	Successful int `json:"successful"`
	Failed     int `json:"failed"`
	Denied     int `json:"denied"`
}

type AudioStats struct {
	ChunksSent     int `json:"chunks_sent"`
	ChunksReceived int `json:"chunks_received"`
}

type ChStats struct {
	Transmitters  int     `json:"transmitters"`
	Receivers    int     `json:"receivers"`
	AudioSent    int     `json:"audio_sent"`
	AudioRecv    int     `json:"audio_received"`
	AvgPerRecv   float64 `json:"avg_per_receiver"`
}

func generateAudioChunk(size int) []byte {
	chunk := make([]byte, size)
	rand.Read(chunk)
	return chunk
}

func connectClient(c *Client, wg *sync.WaitGroup) {
	defer wg.Done()

	conn, _, err := dialer.Dial(serverURL, nil)
	if err != nil {
		c.Errors = append(c.Errors, fmt.Sprintf("Conexión: %v", err))
		return
	}

	c.Conn = conn
	c.Connected = true
}

func joinChannel(c *Client, wg *sync.WaitGroup) {
	defer wg.Done()

	if !c.Connected || c.Conn == nil {
		return
	}

	joinMsg := map[string]interface{}{
		"type":      "join",
		"username":  c.Username,
		"channel":   c.Channel,
		"device_id": fmt.Sprintf("device-%d", c.ID),
	}

	msgBytes, _ := json.Marshal(joinMsg)
	if err := c.Conn.WriteMessage(websocket.TextMessage, msgBytes); err != nil {
		c.Errors = append(c.Errors, fmt.Sprintf("Join send: %v", err))
		return
	}

	c.Conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	_, msg, err := c.Conn.ReadMessage()
	if err != nil {
		c.Errors = append(c.Errors, fmt.Sprintf("Join recv: %v", err))
		return
	}

	var response map[string]interface{}
	json.Unmarshal(msg, &response)

	if response["type"] == "joined" {
		c.Joined = true
	}
}

func receiveMessages(c *Client, stop *atomic.Bool, wg *sync.WaitGroup) {
	defer wg.Done()

	for c.Connected && c.Conn != nil && !stop.Load() {
		c.Conn.SetReadDeadline(time.Now().Add(1 * time.Second))
		msgType, msg, err := c.Conn.ReadMessage()
		if err != nil {
			return
		}

		c.mu.Lock()
		c.AudioChunksRecv++
		c.mu.Unlock()

		if msgType == websocket.BinaryMessage {
			// Audio
		} else {
			json.Unmarshal(msg, nil)
		}
	}
}

func startTransmission(c *Client) bool {
	if !c.Joined || c.Conn == nil {
		return false
	}

	pttMsg, _ := json.Marshal(map[string]string{"type": "ptt_start"})
	if err := c.Conn.WriteMessage(websocket.TextMessage, pttMsg); err != nil {
		return false
	}

	startTime := time.Now()
	for time.Since(startTime) < 5*time.Second {
		c.Conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, msg, err := c.Conn.ReadMessage()
		if err != nil {
			return false
		}

		var response map[string]interface{}
		json.Unmarshal(msg, &response)

		if response["type"] == "ptt_granted" {
			return true
		} else if response["type"] == "ptt_denied" {
			return false
		}
	}
	return false
}

func stopTransmission(c *Client) {
	if c.Conn == nil {
		return
	}
	pttMsg, _ := json.Marshal(map[string]string{"type": "ptt_end"})
	c.Conn.WriteMessage(websocket.TextMessage, pttMsg)
	c.IsTransmitter = false
}

func sendAudioStream(c *Client, duration time.Duration) {
	chunkSize := 640
	chunkDelay := time.Duration(chunkSize) * time.Millisecond / 16
	
	start := time.Now()
	for c.IsTransmitter && time.Since(start) < duration {
		chunk := generateAudioChunk(chunkSize)
		if err := c.Conn.WriteMessage(websocket.BinaryMessage, chunk); err != nil {
			break
		}
		
		c.mu.Lock()
		c.AudioChunksSent++
		c.mu.Unlock()
		
		time.Sleep(chunkDelay)
	}
}

func main() {
	rand.Seed(time.Now().UnixNano())
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	totalClients := numChannels * (1 + receiversPerChannel)
	channels := make([]string, numChannels)
	for i := 0; i < numChannels; i++ {
		channels[i] = fmt.Sprintf("CANAL-%d", i+1)
	}

	fmt.Println("============================================================")
	fmt.Println("PRUEBA EXHAUSTIVA DE CARGA PTT (Go)")
	fmt.Printf("Servidor: %s\n", serverURL)
	fmt.Printf("Canales: %d\n", numChannels)
	fmt.Printf("Transmisores: %d (1 por canal)\n", numChannels)
	fmt.Printf("Receptores: %d (%d por canal)\n", numChannels*receiversPerChannel, receiversPerChannel)
	fmt.Printf("Total clientes: %d\n", totalClients)
	fmt.Println("============================================================")
	fmt.Println()

	results := Results{
		ServerURL:            serverURL,
		NumChannels:          numChannels,
		ReceiversPerChannel:  receiversPerChannel,
		TotalClients:         totalClients,
		StartTime:           time.Now().Format(time.RFC3339),
		ChannelStats:         make(map[string]ChStats),
	}

	start := time.Now()

	// Crear clientes
	fmt.Printf("[1/8] Creando %d clientes...\n", totalClients)
	clients := make([]*Client, totalClients)
	transmitters := make([]*Client, 0, numChannels)
	receivers := make(map[string][]*Client)

	clientID := 1
	for _, channel := range channels {
		// Transmisor
		tx := &Client{
			ID:            clientID,
			Username:      fmt.Sprintf("TX-%s-1", channel),
			Channel:       channel,
			IsTransmitter: true,
		}
		clients[clientID-1] = tx
		transmitters = append(transmitters, tx)
		receivers[channel] = make([]*Client, 0, receiversPerChannel)
		clientID++

		// Receptores
		for r := 0; r < receiversPerChannel; r++ {
			rx := &Client{
				ID:      clientID,
				Username: fmt.Sprintf("RX-%s-%d", channel, r+1),
				Channel:  channel,
			}
			clients[clientID-1] = rx
			receivers[channel] = append(receivers[channel], rx)
			clientID++
		}
	}
	fmt.Printf("       -> %d clientes creados\n", len(clients))

	// Conectar clientes
	fmt.Printf("[2/8] Conectando %d clientes...\n", len(clients))
	var wg sync.WaitGroup
	
	for _, c := range clients {
		wg.Add(1)
		go connectClient(c, &wg)
	}
	wg.Wait()

	connected := 0
	for _, c := range clients {
		if c.Connected {
			connected++
			results.ConnectionStats.Successful++
		} else {
			results.ConnectionStats.Failed++
		}
	}
	results.ConnectionStats.Total = len(clients)
	fmt.Printf("       -> Conectados: %d/%d\n", connected, len(clients))

	// Join a canales
	fmt.Printf("[3/8] Uniando clientes a canales...\n")
	for _, c := range clients {
		if c.Connected {
			wg.Add(1)
			go joinChannel(c, &wg)
		}
	}
	wg.Wait()

	joined := 0
	for _, c := range clients {
		if c.Joined {
			joined++
			results.JoinStats.Successful++
		} else {
			results.JoinStats.Failed++
		}
	}
	results.JoinStats.Total = len(clients)
	fmt.Printf("       -> Unidos: %d/%d\n", joined, len(clients))

	// Iniciar receptores
	fmt.Printf("[4/8] Iniciando receptores...\n")
	stop := atomic.Bool{}
	stop.Store(false)
	var recvWg sync.WaitGroup
	
	for _, c := range clients {
		if c.Joined && !c.IsTransmitter {
			recvWg.Add(1)
			go receiveMessages(c, &stop, &recvWg)
		}
	}
	fmt.Printf("       -> Receptores activos\n")

	// Iniciar transmisiones
	fmt.Printf("[5/8] Iniciando transmisiones simultáneas...\n")
	
	var txWg sync.WaitGroup
	for _, tx := range transmitters {
		if !tx.Joined {
			continue
		}
		
		txWg.Add(1)
		go func(t *Client) {
			defer txWg.Done()
			
			if startTransmission(t) {
				t.IsTransmitter = true
				results.TransmissionStats.Successful++
				fmt.Printf("       [✓] %s transmite\n", t.Username)
				
				// Enviar audio por 2 segundos
				sendAudioStream(t, 2*time.Second)
				stopTransmission(t)
				
				fmt.Printf("       [✓] %s terminó\n", t.Username)
			} else {
				results.TransmissionStats.Denied++
				fmt.Printf("       [!] %s no pudo transmitir\n", t.Username)
			}
		}(tx)
		results.TransmissionStats.Total++
	}

	txWg.Wait()

	// Esperar recepción
	fmt.Printf("[6/8] Esperando recepción de audio...\n")
	time.Sleep(2 * time.Second)

	// Detener receptores
	fmt.Printf("[7/8] Deteniendo receptores...\n")
	stop.Store(true)
	time.Sleep(500 * time.Millisecond)

	// Desconectar
	fmt.Printf("[8/8] Desconectando clientes...\n")
	for _, c := range clients {
		if c.Conn != nil {
			c.Conn.Close()
		}
	}

	end := time.Now()
	results.EndTime = end.Format(time.RFC3339)
	results.DurationSeconds = end.Sub(start).Seconds()

	// Estadísticas de audio
	for _, c := range clients {
		c.mu.Lock()
		results.AudioStats.ChunksSent += c.AudioChunksSent
		results.AudioStats.ChunksReceived += c.AudioChunksRecv
		c.mu.Unlock()
	}

	// Estadísticas por canal
	for _, channel := range channels {
		txList := make([]*Client, 0)
		for _, t := range transmitters {
			if t.Channel == channel {
				txList = append(txList, t)
			}
		}
		
		recvList := receivers[channel]
		totalSent := 0
		for _, t := range txList {
			totalSent += t.AudioChunksSent
		}
		
		totalRecv := 0
		for _, r := range recvList {
			totalRecv += r.AudioChunksRecv
		}
		
		avgRecv := float64(0)
		if len(recvList) > 0 {
			avgRecv = float64(totalRecv) / float64(len(recvList))
		}
		
		results.ChannelStats[channel] = ChStats{
			Transmitters: len(txList),
			Receivers:    len(recvList),
			AudioSent:    totalSent,
			AudioRecv:    totalRecv,
			AvgPerRecv:   avgRecv,
		}
	}

	// Determinar éxito
	successRate := float64(0)
	if results.JoinStats.Total > 0 {
		successRate = float64(results.JoinStats.Successful) / float64(results.JoinStats.Total) * 100
	}
	
	results.Success = results.ConnectionStats.Successful >= int(float64(totalClients)*0.9) &&
		successRate >= 80 &&
		results.TransmissionStats.Successful >= int(float64(numChannels)*0.5)

	// Resumen
	fmt.Println()
	fmt.Println("============================================================")
	fmt.Println("RESUMEN DE PRUEBA EXHAUSTIVA")
	fmt.Println("============================================================")
	fmt.Printf("Servidor: %s\n", results.ServerURL)
	fmt.Printf("Duración: %.2f segundos\n", results.DurationSeconds)
	fmt.Println()
	fmt.Println("--- Conexiones ---")
	fmt.Printf("Total:    %d\n", results.ConnectionStats.Total)
	fmt.Printf("Exitosas: %d\n", results.ConnectionStats.Successful)
	fmt.Printf("Fallidas: %d\n", results.ConnectionStats.Failed)
	fmt.Println()
	fmt.Println("--- Unirse a canales ---")
	fmt.Printf("Total:    %d\n", results.JoinStats.Total)
	fmt.Printf("Exitosas: %d\n", results.JoinStats.Successful)
	fmt.Printf("Fallidas: %d\n", results.JoinStats.Failed)
	fmt.Printf("Tasa:     %.1f%%\n", successRate)
	fmt.Println()
	fmt.Println("--- Transmisiones PTT ---")
	fmt.Printf("Total:      %d\n", results.TransmissionStats.Total)
	fmt.Printf("Concedidas: %d\n", results.TransmissionStats.Successful)
	fmt.Printf("Denegadas:  %d\n", results.TransmissionStats.Denied)
	fmt.Println()
	fmt.Println("--- Audio ---")
	fmt.Printf("Chunks enviados:   %d\n", results.AudioStats.ChunksSent)
	fmt.Printf("Chunks recibidos:  %d\n", results.AudioStats.ChunksReceived)
	
	fmt.Println()
	fmt.Println("--- Estadísticas por Canal ---")
	for channel, stats := range results.ChannelStats {
		fmt.Printf("  %s:\n", channel)
		fmt.Printf("    Transmisión: %d chunks\n", stats.AudioSent)
		fmt.Printf("    Reception:   %d chunks (%.1f por receptor)\n", stats.AudioRecv, stats.AvgPerRecv)
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
	file, _ := os.Create("test_results_go_heavy.json")
	defer file.Close()
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	encoder.Encode(results)
	fmt.Printf("Resultados guardados en: test_results_go_heavy.json\n")

	// Esperar señal
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
}
