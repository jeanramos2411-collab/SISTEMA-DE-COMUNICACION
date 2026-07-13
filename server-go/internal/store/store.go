package store

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Channel struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Access  string `json:"access"`
}

type Device struct {
	Username        string   `json:"username"`
	MAC            string   `json:"mac"`
	IPLast         string   `json:"ip_last"`
	PlaybackGain   *float64 `json:"playback_gain"`
	ApprovedChans  []string `json:"approved_channels"`
	FirstSeen      string   `json:"first_seen"`
	LastSeen       string   `json:"last_seen"`
}

type PendingApproval struct {
	ID          string `json:"id"`
	DeviceID    string `json:"device_id"`
	Username    string `json:"username"`
	IP          string `json:"ip"`
	MAC         string `json:"mac"`
	ChannelID   string `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	SessionID   string `json:"session_id"`
	RequestedAt string `json:"requested_at"`
}

type BlockEntry struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Value     string `json:"value"`
	Reason    string `json:"reason"`
	BlockedAt string `json:"blocked_at"`
}

type Config struct {
	AdminPassword    string             `json:"admin_password"`
	PlaybackGain     float64            `json:"playback_gain"`
	Channels         []Channel          `json:"channels"`
	Blocked         []BlockEntry       `json:"blocked"`
	Devices         map[string]Device  `json:"devices"`
	PendingApprovals []PendingApproval `json:"pending_approvals"`
}

type Store struct {
	mu            sync.RWMutex
	config        Config
	dirty         bool
	saveTask      *time.Timer
	dataDir       string
	configPath    string
}

var DefaultChannels = []Channel{
	{ID: "canal-1", Name: "Canal 1", Enabled: true, Access: "open"},
	{ID: "canal-2", Name: "Canal 2", Enabled: true, Access: "open"},
	{ID: "canal-3", Name: "Canal 3", Enabled: true, Access: "open"},
	{ID: "canal-4", Name: "Canal 4", Enabled: true, Access: "open"},
	{ID: "canal-5", Name: "Canal 5", Enabled: true, Access: "open"},
	{ID: "mantenimiento", Name: "Mantenimiento", Enabled: true, Access: "approval"},
	{ID: "trazabilidad", Name: "Trazabilidad", Enabled: true, Access: "approval"},
	{ID: "produccion", Name: "Produccion", Enabled: true, Access: "approval"},
	{ID: "calidad", Name: "Calidad", Enabled: true, Access: "approval"},
	{ID: "logistica", Name: "Logistica", Enabled: true, Access: "approval"},
}

func New(dataDir string) *Store {
	return &Store{
		config: Config{
			AdminPassword:    "admin123",
			PlaybackGain:     3.0,
			Channels:         DefaultChannels,
			Blocked:          []BlockEntry{},
			Devices:          make(map[string]Device),
			PendingApprovals: []PendingApproval{},
		},
		dataDir:    dataDir,
		configPath: filepath.Join(dataDir, "config.json"),
	}
}

func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(s.dataDir, 0755); err != nil {
		return err
	}

	log.Printf("[DEBUG] Buscando config.json en: %s", s.configPath)

	data, err := os.ReadFile(s.configPath)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("[INFO] config.json no encontrado, creando con valores por defecto")
			return s.saveLocked()
		}
		log.Printf("[ERROR] Error leyendo config.json: %v", err)
		return err
	}

	log.Printf("[INFO] config.json encontrado, cargando configuracion...")

	var loaded Config
	if err := json.Unmarshal(data, &loaded); err != nil {
		log.Printf("[ERROR] Error parseando config.json: %v", err)
		return err
	}

	s.config = s.mergeDefaults(loaded)
	
	// Log de los canales cargados
	if len(s.config.Channels) > 0 {
		names := make([]string, 0)
		for _, ch := range s.config.Channels {
			if ch.Enabled {
				names = append(names, ch.Name)
			}
		}
		log.Printf("[INFO] Canales cargados: %v", names)
	}
	
	return nil
}

func (s *Store) mergeDefaults(loaded Config) Config {
	result := Config{
		AdminPassword:    "admin123",
		PlaybackGain:     3.0,
		Channels:         DefaultChannels,
		Blocked:          []BlockEntry{},
		Devices:          make(map[string]Device),
		PendingApprovals: []PendingApproval{},
	}

	if loaded.AdminPassword != "" {
		result.AdminPassword = loaded.AdminPassword
	}
	result.PlaybackGain = loaded.PlaybackGain
	if result.PlaybackGain == 0 {
		result.PlaybackGain = 3.0
	}

	if loaded.Channels != nil {
		result.Channels = loaded.Channels
	}
	if loaded.Blocked != nil {
		result.Blocked = loaded.Blocked
	}
	if loaded.Devices != nil {
		result.Devices = loaded.Devices
	}
	if loaded.PendingApprovals != nil {
		result.PendingApprovals = loaded.PendingApprovals
	}

	for i := range result.Channels {
		if result.Channels[i].Access == "" {
			result.Channels[i].Access = "open"
		}
	}

	return result
}

func (s *Store) save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	data, err := json.MarshalIndent(s.config, "", "  ")
	if err != nil {
		return err
	}

	tmpPath := s.configPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}

	if err := os.Rename(tmpPath, s.configPath); err != nil {
		return err
	}

	s.dirty = false
	return nil
}

func (s *Store) scheduleSaveLocked() {
	s.dirty = true
	if s.saveTask != nil {
		s.saveTask.Stop()
	}
	s.saveTask = time.AfterFunc(3*time.Second, func() {
		s.save()
	})
}

func (s *Store) scheduleSave() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scheduleSaveLocked()
}

func (s *Store) Flush() error {
	if s.saveTask != nil {
		s.saveTask.Stop()
		s.saveTask = nil
	}
	return s.save()
}

func (s *Store) GetConfig() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config
}

func (s *Store) ChannelByName(name string) *Channel {
	s.mu.RLock()
	defer s.mu.RUnlock()

	nameLower := strings.ToLower(strings.TrimSpace(name))
	for i := range s.config.Channels {
		if strings.ToLower(strings.TrimSpace(s.config.Channels[i].Name)) == nameLower {
			return &s.config.Channels[i]
		}
	}
	return nil
}

func (s *Store) ChannelByID(channelID string) *Channel {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for i := range s.config.Channels {
		if s.config.Channels[i].ID == channelID {
			return &s.config.Channels[i]
		}
	}
	return nil
}

func (s *Store) EnabledChannelNames() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var names []string
	for _, ch := range s.config.Channels {
		if ch.Enabled && ch.Name != "" {
			names = append(names, ch.Name)
		}
	}
	return names
}

func (s *Store) PlaybackGain() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return clampGain(s.config.PlaybackGain)
}

func (s *Store) DevicePlaybackGain(deviceID string) float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	device, exists := s.config.Devices[deviceID]
	if !exists {
		return clampGain(s.config.PlaybackGain)
	}

	if device.PlaybackGain == nil {
		return clampGain(s.config.PlaybackGain)
	}
	return clampGain(*device.PlaybackGain)
}

func clampGain(gain float64) float64 {
	if gain < 0.5 {
		return 0.5
	}
	if gain > 6.0 {
		return 6.0
	}
	return gain
}

func (s *Store) SetPlaybackGain(gain float64) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	gain = clampGain(gain)
	s.config.PlaybackGain = gain
	s.saveLocked()
	return gain
}

func (s *Store) SetDevicePlaybackGain(deviceID string, gain *float64) (Device, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if strings.TrimSpace(deviceID) == "" {
		return Device{}, errors.New("ID de dispositivo vacio")
	}

	deviceID = strings.TrimSpace(deviceID)
	now := nowISO()

	if _, exists := s.config.Devices[deviceID]; !exists {
		s.config.Devices[deviceID] = Device{
			FirstSeen: now,
			LastSeen:  now,
		}
	}

	entry := s.config.Devices[deviceID]
	if gain != nil {
		g := clampGain(*gain)
		entry.PlaybackGain = &g
	} else {
		entry.PlaybackGain = nil
	}
	entry.LastSeen = now

	s.config.Devices[deviceID] = entry
	s.saveLocked()

	return entry, nil
}

func (s *Store) TouchDevice(deviceID, username, ip, mac string) Device {
	s.mu.Lock()
	defer s.mu.Unlock()

	if deviceID == "" {
		return Device{}
	}

	now := nowISO()
	if _, exists := s.config.Devices[deviceID]; !exists {
		s.config.Devices[deviceID] = Device{
			Username: username,
			MAC:      mac,
			IPLast:   ip,
			FirstSeen: now,
			LastSeen:  now,
		}
		s.scheduleSaveLocked()
		return s.config.Devices[deviceID]
	}

	entry := s.config.Devices[deviceID]
	if username != "" {
		entry.Username = username
	}
	if ip != "" {
		entry.IPLast = ip
	}
	if mac != "" {
		entry.MAC = mac
	}
	entry.LastSeen = now

	s.config.Devices[deviceID] = entry
	s.scheduleSaveLocked()

	return entry
}

func (s *Store) RecordDeviceChannelAccess(deviceID, channelName string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ch := s.channelByNameLocked(channelName)
	if ch == nil || deviceID == "" {
		return
	}

	if _, exists := s.config.Devices[deviceID]; !exists {
		s.config.Devices[deviceID] = Device{
			FirstSeen: nowISO(),
			LastSeen:  nowISO(),
		}
	}

	entry := s.config.Devices[deviceID]
	entry.LastSeen = nowISO()
	s.config.Devices[deviceID] = entry

	s.scheduleSaveLocked()
}

func (s *Store) channelByNameLocked(name string) *Channel {
	nameLower := strings.ToLower(strings.TrimSpace(name))
	for i := range s.config.Channels {
		if strings.ToLower(strings.TrimSpace(s.config.Channels[i].Name)) == nameLower {
			return &s.config.Channels[i]
		}
	}
	return nil
}

func (s *Store) IsDeviceApprovedForChannel(deviceID, channelName string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ch := s.channelByNameLocked(channelName)
	if ch == nil {
		return false
	}

	device, exists := s.config.Devices[deviceID]
	if !exists {
		return false
	}

	for _, cid := range device.ApprovedChans {
		if cid == ch.ID {
			return true
		}
	}
	return false
}

func (s *Store) AddChannel(name, access string) (Channel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	name = strings.TrimSpace(name)
	if name == "" {
		return Channel{}, errors.New("Nombre no puede estar vacio")
	}

	if access != "open" && access != "approval" {
		return Channel{}, errors.New("Access debe ser 'open' o 'approval'")
	}

	id := strings.ToLower(strings.ReplaceAll(name, " ", "-"))
	id = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return -1
	}, id)

	entry := Channel{
		ID:      id,
		Name:    name,
		Enabled: true,
		Access:  access,
	}

	s.config.Channels = append(s.config.Channels, entry)
	s.saveLocked()

	return entry, nil
}

func (s *Store) UpdateChannel(channelID string, name *string, enabled, access *bool) (Channel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx := -1
	for i := range s.config.Channels {
		if s.config.Channels[i].ID == channelID {
			idx = i
			break
		}
	}

	if idx == -1 {
		return Channel{}, errors.New("Bloque no encontrado")
	}

	if name != nil {
		s.config.Channels[idx].Name = *name
	}
	if enabled != nil {
		s.config.Channels[idx].Enabled = *enabled
	}
	if access != nil {
		acc := "open"
		if *access {
			acc = "approval"
		}
		s.config.Channels[idx].Access = acc
	}

	s.saveLocked()
	return s.config.Channels[idx], nil
}

func (s *Store) DeleteChannel(channelID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx := -1
	for i := range s.config.Channels {
		if s.config.Channels[i].ID == channelID {
			idx = i
			break
		}
	}

	if idx == -1 {
		return errors.New("Bloque no encontrado")
	}

	s.config.Channels = append(s.config.Channels[:idx], s.config.Channels[idx+1:]...)

	for deviceID := range s.config.Devices {
		device := s.config.Devices[deviceID]
		device.ApprovedChans = filterRemove(device.ApprovedChans, channelID)
		s.config.Devices[deviceID] = device
	}

	s.config.PendingApprovals = filterPendingByChannel(s.config.PendingApprovals, channelID)

	s.saveLocked()
	return nil
}

func filterRemove(slice []string, item string) []string {
	var result []string
	for _, s := range slice {
		if s != item {
			result = append(result, s)
		}
	}
	return result
}

func filterPendingByChannel(pending []PendingApproval, channelID string) []PendingApproval {
	var result []PendingApproval
	for _, p := range pending {
		if p.ChannelID != channelID {
			result = append(result, p)
		}
	}
	return result
}

func (s *Store) UpsertPending(deviceID, username, ip, mac, channelID, channelName, sessionID string) PendingApproval {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.config.PendingApprovals {
		p := &s.config.PendingApprovals[i]
		if p.DeviceID == deviceID && p.ChannelID == channelID {
			p.Username = username
			p.IP = ip
			p.MAC = mac
			p.SessionID = sessionID
			p.RequestedAt = nowISO()
			s.scheduleSaveLocked()
			return *p
		}
	}

	entry := PendingApproval{
		ID:          uuid.New().String()[:8],
		DeviceID:    deviceID,
		Username:    username,
		IP:          ip,
		MAC:         mac,
		ChannelID:   channelID,
		ChannelName: channelName,
		SessionID:   sessionID,
		RequestedAt: nowISO(),
	}

	s.config.PendingApprovals = append(s.config.PendingApprovals, entry)
	s.scheduleSaveLocked()

	return entry
}

func (s *Store) RemovePending(pendingID string) *PendingApproval {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, p := range s.config.PendingApprovals {
		if p.ID == pendingID {
			s.config.PendingApprovals = append(s.config.PendingApprovals[:i], s.config.PendingApprovals[i+1:]...)
			result := p
			s.saveLocked()
			return &result
		}
	}
	return nil
}

func (s *Store) RemovePendingBySession(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if sessionID == "" {
		return
	}

	var result []PendingApproval
	for _, p := range s.config.PendingApprovals {
		if p.SessionID != sessionID {
			result = append(result, p)
		}
	}
	s.config.PendingApprovals = result
	s.scheduleSaveLocked()
}

func (s *Store) ApprovePending(pendingID string) *PendingApproval {
	s.mu.Lock()
	defer s.mu.Unlock()

	item := s.removePendingLocked(pendingID)
	if item == nil {
		return nil
	}

	if item.DeviceID != "" && item.ChannelID != "" {
		now := nowISO()
		if _, exists := s.config.Devices[item.DeviceID]; !exists {
			s.config.Devices[item.DeviceID] = Device{
				Username:       item.Username,
				MAC:            item.MAC,
				IPLast:         item.IP,
				ApprovedChans:  []string{},
				FirstSeen:      now,
				LastSeen:       now,
			}
		}

		device := s.config.Devices[item.DeviceID]
		device.ApprovedChans = append(device.ApprovedChans, item.ChannelID)
		device.LastSeen = now
		s.config.Devices[item.DeviceID] = device
	}

	s.saveLocked()
	return item
}

func (s *Store) removePendingLocked(pendingID string) *PendingApproval {
	for i, p := range s.config.PendingApprovals {
		if p.ID == pendingID {
			s.config.PendingApprovals = append(s.config.PendingApprovals[:i], s.config.PendingApprovals[i+1:]...)
			return &p
		}
	}
	return nil
}

func (s *Store) RevokeDeviceFromChannel(deviceID, channelID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	device, exists := s.config.Devices[deviceID]
	if !exists {
		return errors.New("Dispositivo no encontrado")
	}

	device.ApprovedChans = filterRemove(device.ApprovedChans, channelID)
	s.config.Devices[deviceID] = device
	s.saveLocked()

	return nil
}

type DeviceInfo struct {
	DeviceID           string   `json:"device_id"`
	Username           string   `json:"username"`
	MAC                string   `json:"mac"`
	IPLast             string   `json:"ip_last"`
	PlaybackGain       *float64 `json:"playback_gain"`
	EffectiveGain      float64  `json:"effective_gain"`
	ApprovedChannels   []string `json:"approved_channels"`
	ApprovedChanNames  []string `json:"approved_channel_names"`
	FirstSeen          string   `json:"first_seen"`
	LastSeen           string   `json:"last_seen"`
}

func (s *Store) ListDevices() []DeviceInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	channelNames := make(map[string]string)
	for _, ch := range s.config.Channels {
		channelNames[ch.ID] = ch.Name
	}

	var rows []DeviceInfo
	for deviceID, data := range s.config.Devices {
		var approvedNames []string
		for _, cid := range data.ApprovedChans {
			if name := channelNames[cid]; name != "" {
				approvedNames = append(approvedNames, name)
			}
		}

		rows = append(rows, DeviceInfo{
			DeviceID:          deviceID,
			Username:          data.Username,
			MAC:               data.MAC,
			IPLast:            data.IPLast,
			PlaybackGain:      data.PlaybackGain,
			EffectiveGain:     s.deviceGainLocked(deviceID),
			ApprovedChannels:  data.ApprovedChans,
			ApprovedChanNames: approvedNames,
			FirstSeen:         data.FirstSeen,
			LastSeen:          data.LastSeen,
		})
	}

	sort.Slice(rows, func(i, j int) bool {
		return rows[i].Username < rows[j].Username
	})

	return rows
}

func (s *Store) deviceGainLocked(deviceID string) float64 {
	device, exists := s.config.Devices[deviceID]
	if !exists {
		return clampGain(s.config.PlaybackGain)
	}
	if device.PlaybackGain == nil {
		return clampGain(s.config.PlaybackGain)
	}
	return clampGain(*device.PlaybackGain)
}

type ChannelGroup struct {
	ChannelID   string `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	Access      string `json:"access"`
	Members     []DeviceInfo `json:"members"`
	MemberCount int    `json:"member_count"`
	Online      []map[string]interface{} `json:"online"`
}

func (s *Store) DevicesByChannel() []ChannelGroup {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var rows []ChannelGroup
	for _, ch := range s.config.Channels {
		if !ch.Enabled {
			continue
		}

		var members []DeviceInfo
		for deviceID, data := range s.config.Devices {
			for _, cid := range data.ApprovedChans {
				if cid == ch.ID {
					members = append(members, DeviceInfo{
						DeviceID: deviceID,
						Username: data.Username,
						MAC:      data.MAC,
						IPLast:   data.IPLast,
					})
					break
				}
			}
		}

		sort.Slice(members, func(i, j int) bool {
			return members[i].Username < members[j].Username
		})

		rows = append(rows, ChannelGroup{
			ChannelID:   ch.ID,
			ChannelName: ch.Name,
			Access:      ch.Access,
			Members:     members,
			MemberCount: len(members),
			Online:      []map[string]interface{}{},
		})
	}

	return rows
}

func (s *Store) IsBlocked(username, deviceID, ip string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	usernameLower := strings.ToLower(strings.TrimSpace(username))
	deviceLower := strings.ToLower(strings.TrimSpace(deviceID))
	ip = strings.TrimSpace(ip)

	for _, entry := range s.config.Blocked {
		value := strings.ToLower(strings.TrimSpace(entry.Value))

		switch entry.Type {
		case "username":
			if usernameLower != "" && usernameLower == value {
				return true
			}
		case "device_id":
			if deviceLower != "" && deviceLower == value {
				return true
			}
		case "ip":
			if ip != "" && ip == entry.Value {
				return true
			}
		}
	}

	return false
}

func (s *Store) AddBlock(blockType, value, reason string) (BlockEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	value = strings.TrimSpace(value)
	if blockType != "username" && blockType != "device_id" && blockType != "ip" {
		return BlockEntry{}, errors.New("Tipo invalido")
	}
	if value == "" {
		return BlockEntry{}, errors.New("Valor vacio")
	}

	entry := BlockEntry{
		ID:        uuid.New().String()[:8],
		Type:      blockType,
		Value:     value,
		Reason:    strings.TrimSpace(reason),
		BlockedAt: nowISO(),
	}

	s.config.Blocked = append(s.config.Blocked, entry)
	s.saveLocked()

	return entry, nil
}

func (s *Store) RemoveBlock(blockID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx := -1
	for i, b := range s.config.Blocked {
		if b.ID == blockID {
			idx = i
			break
		}
	}

	if idx == -1 {
		return errors.New("Bloqueo no encontrado")
	}

	s.config.Blocked = append(s.config.Blocked[:idx], s.config.Blocked[idx+1:]...)
	s.saveLocked()

	return nil
}

func (s *Store) VerifyPassword(password string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return password == s.config.AdminPassword
}

func nowISO() string {
	return time.Now().UTC().Format(time.RFC3339)
}
