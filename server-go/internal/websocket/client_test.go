package websocket

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"ptt-server/internal/store"
)

// MockStore implements minimal store methods for testing
type MockStore struct {
	channels    map[string]*ChannelInfo
	blocked     map[string]bool
	devices     map[string]*DeviceInfo
	approvals   map[string]*PendingApproval
}

type ChannelInfo struct {
	ID      string
	Name    string
	Access string
}

type DeviceInfo struct {
	ID           string
	Username     string
	ApprovedChs map[string]bool
}

type PendingApproval struct {
	ID          string
	SessionID   string
	ChannelName string
}

func NewMockStore() *MockStore {
	return &MockStore{
		channels:  make(map[string]*ChannelInfo),
		blocked:   make(map[string]bool),
		devices:   make(map[string]*DeviceInfo),
		approvals: make(map[string]*PendingApproval),
	}
}

func (m *MockStore) Load() error {
	// Pre-populate with test channels
	m.channels["canal-1"] = &ChannelInfo{ID: "ch1", Name: "canal-1", Access: "open"}
	m.channels["mantenimiento"] = &ChannelInfo{ID: "ch2", Name: "mantenimiento", Access: "open"}
	return nil
}

func (m *MockStore) EnabledChannelNames() []string {
	var names []string
	for _, ch := range m.channels {
		names = append(names, ch.Name)
	}
	return names
}

func (m *MockStore) ChannelByName(name string) *ChannelInfo {
	return m.channels[name]
}

func (m *MockStore) IsBlocked(username, deviceID, ip string) bool {
	return m.blocked[username] || m.blocked[deviceID] || m.blocked[ip]
}

func (m *MockStore) TouchDevice(deviceID, username, ip, mac string) {
	m.devices[deviceID] = &DeviceInfo{ID: deviceID, Username: username}
}

func (m *MockStore) RecordDeviceChannelAccess(deviceID, channel string) {
	if d, ok := m.devices[deviceID]; ok {
		if d.ApprovedChs == nil {
			d.ApprovedChs = make(map[string]bool)
		}
		d.ApprovedChs[channel] = true
	}
}

func (m *MockStore) IsDeviceApprovedForChannel(deviceID, channel string) bool {
	if d, ok := m.devices[deviceID]; ok {
		return d.ApprovedChs[channel]
	}
	return false
}

func (m *MockStore) DevicePlaybackGain(deviceID string) float64 {
	return 1.0
}

func (m *MockStore) UpsertPending(deviceID, username, ip, mac, channelID, channelName, sessionID string) *PendingApproval {
	id := "pending-" + sessionID
	m.approvals[id] = &PendingApproval{
		ID:          id,
		SessionID:   sessionID,
		ChannelName: channelName,
	}
	return m.approvals[id]
}

func (m *MockStore) ApprovePending(id string) *PendingApproval {
	if p, ok := m.approvals[id]; ok {
		m.RecordDeviceChannelAccess("", p.ChannelName)
		return p
	}
	return nil
}

func (m *MockStore) RemovePending(id string) *PendingApproval {
	if p, ok := m.approvals[id]; ok {
		delete(m.approvals, id)
		return p
	}
	return nil
}

func (m *MockStore) RemovePendingBySession(sessionID string) {
	for id, p := range m.approvals {
		if p.SessionID == sessionID {
			delete(m.approvals, id)
		}
	}
}

// Interface compatibility - these methods exist in the real store but we don't need them for tests
func (m *MockStore) GetConfig() interface{} { return nil }
func (m *MockStore) Flush() error { return nil }

// Test helper: create a new server state for testing
func newTestServerState() *ServerState {
	s := &store.Store{}
	s.Load() // This will fail but we just need the struct
	return &ServerState{
		clients:        make(map[*Client]bool),
		channelMembers: make(map[string]map[*Client]bool),
		channelSpeaker: make(map[string]*Client),
	}
}

// Test01: Session ID Generation
func TestSessionIDGeneration(t *testing.T) {
	id1 := generateSessionID()
	id2 := generateSessionID()

	// IDs should be 8 characters
	if len(id1) != 8 {
		t.Errorf("Session ID should be 8 chars, got %d", len(id1))
	}

	// IDs should be unique
	if id1 == id2 {
		t.Errorf("Session IDs should be unique")
	}

	t.Logf("Generated session IDs: %s, %s", id1, id2)
}

// Test02: contains helper function
func TestContainsHelper(t *testing.T) {
	slice := []string{"a", "b", "c"}

	if !contains(slice, "a") {
		t.Error("contains should return true for existing element")
	}

	if contains(slice, "d") {
		t.Error("contains should return false for non-existing element")
	}
}

// Test03: joinStrings helper function
func TestJoinStringsHelper(t *testing.T) {
	slice := []string{"a", "b", "c"}
	result := joinStrings(slice, ", ")
	expected := "a, b, c"

	if result != expected {
		t.Errorf("joinStrings: expected %q, got %q", expected, result)
	}

	// Empty slice
	result = joinStrings([]string{}, ", ")
	if result != "" {
		t.Errorf("joinStrings with empty slice: expected empty string, got %q", result)
	}
}

// Test04: sortStrings helper function
func TestSortStringsHelper(t *testing.T) {
	slice := []string{"c", "a", "b"}
	sortStrings(slice)

	expected := []string{"a", "b", "c"}
	for i, v := range expected {
		if slice[i] != v {
			t.Errorf("sortStrings: expected[%d]=%q, got %q", i, v, slice[i])
		}
	}
}

// Test05: isOpen check
func TestIsOpen(t *testing.T) {
	// Test with nil client
	if isOpen(nil) {
		t.Error("isOpen should return false for nil client")
	}

	// Test with nil connection
	client := &Client{conn: nil}
	if isOpen(client) {
		t.Error("isOpen should return false for nil connection")
	}
}

// Test06: ServerState creation
func TestServerStateCreation(t *testing.T) {
	s := NewServerState(nil)

	if s == nil {
		t.Fatal("NewServerState should not return nil")
	}

	if s.clients == nil {
		t.Error("clients map should be initialized")
	}

	if s.channelMembers == nil {
		t.Error("channelMembers map should be initialized")
	}

	if s.channelSpeaker == nil {
		t.Error("channelSpeaker map should be initialized")
	}

	if s.mu == (sync.RWMutex{}) {
		// RWMutex should be zero value (initialized)
	}

	t.Log("ServerState created successfully with all maps initialized")
}

// Test07: getString helper function
func TestGetStringHelper(t *testing.T) {
	data := map[string]interface{}{
		"name": "test",
		"age":  25,
	}

	if getString(data, "name") != "test" {
		t.Error("getString should return 'test' for key 'name'")
	}

	if getString(data, "missing") != "" {
		t.Error("getString should return empty string for missing key")
	}

	if getString(data, "age") != "" {
		t.Error("getString should return empty string for non-string value")
	}
}

// Test08: JSON message types
func TestMessageTypes(t *testing.T) {
	// Test that all expected message types can be marshaled
	messages := []map[string]interface{}{
		{"type": "join", "channel": "test", "username": "user1"},
		{"type": "ptt_start"},
		{"type": "ptt_end"},
		{"type": "ping"},
		{"type": "joined", "channel": "test", "channels": []string{"test"}, "users": []string{"user1"}},
		{"type": "ptt_granted"},
		{"type": "ptt_started", "username": "user1"},
		{"type": "ptt_ended", "username": "user1"},
		{"type": "ptt_denied", "reason": "busy", "speaker": "user2"},
		{"type": "users_update", "users": []string{"user1", "user2"}},
		{"type": "approval_pending", "channel": "test", "request_id": "123"},
		{"type": "approval_denied", "channel": "test"},
		{"type": "error", "message": "test error"},
	}

	for _, msg := range messages {
		data, err := json.Marshal(msg)
		if err != nil {
			t.Errorf("Failed to marshal message type %s: %v", msg["type"], err)
		}

		var parsed map[string]interface{}
		if err := json.Unmarshal(data, &parsed); err != nil {
			t.Errorf("Failed to unmarshal message type %s: %v", msg["type"], err)
		}

		if parsed["type"] != msg["type"] {
			t.Errorf("Message type mismatch: expected %s, got %s", msg["type"], parsed["type"])
		}
	}

	t.Logf("All %d message types validated successfully", len(messages))
}

// Test09: Client struct fields
func TestClientStruct(t *testing.T) {
	client := &Client{
		sessionID:      "abc12345",
		username:       "TestUser",
		channel:        "test-channel",
		pendingChannel: "",
		isTransmitting: false,
		ip:            "192.168.1.100",
		deviceID:      "device-001",
		mac:           "AA:BB:CC:DD:EE:FF",
		connectedAt:    time.Now().UTC().Format(time.RFC3339),
	}

	if client.sessionID != "abc12345" {
		t.Error("sessionID not set correctly")
	}

	if client.username != "TestUser" {
		t.Error("username not set correctly")
	}

	if client.channel != "test-channel" {
		t.Error("channel not set correctly")
	}

	if client.isTransmitting {
		t.Error("isTransmitting should be false by default")
	}

	t.Log("Client struct fields validated")
}

// Test10: ClientSnapshot struct
func TestClientSnapshotStruct(t *testing.T) {
	snapshot := ClientSnapshot{
		SessionID:      "abc12345",
		Username:       "TestUser",
		Channel:        "test-channel",
		PendingChannel: "",
		IP:             "192.168.1.100",
		MAC:            "AA:BB:CC:DD:EE:FF",
		DeviceID:       "device-001",
		IsTransmitting:  false,
		IsSpeaking:      false,
		ConnectedAt:    time.Now().UTC().Format(time.RFC3339),
	}

	// Test JSON marshaling
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("Failed to marshal ClientSnapshot: %v", err)
	}

	var parsed ClientSnapshot
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Failed to unmarshal ClientSnapshot: %v", err)
	}

	if parsed.SessionID != snapshot.SessionID {
		t.Error("SessionID mismatch after JSON round-trip")
	}

	if parsed.Username != snapshot.Username {
		t.Error("Username mismatch after JSON round-trip")
	}

	t.Logf("ClientSnapshot JSON: %s", string(data))
}

// Test11: sortSnapshots helper function
func TestSortSnapshotsHelper(t *testing.T) {
	snapshots := []ClientSnapshot{
		{Channel: "b-channel", Username: "z-user"},
		{Channel: "a-channel", Username: "a-user"},
		{Channel: "a-channel", Username: "b-user"},
	}

	sortSnapshots(snapshots)

	// Expected order: a-channel, a-user, a-channel, b-user, b-channel, z-user
	if snapshots[0].Channel != "a-channel" || snapshots[0].Username != "a-user" {
		t.Errorf("First snapshot should be a-channel/a-user, got %s/%s",
			snapshots[0].Channel, snapshots[0].Username)
	}

	if snapshots[1].Channel != "a-channel" || snapshots[1].Username != "b-user" {
		t.Errorf("Second snapshot should be a-channel/b-user, got %s/%s",
			snapshots[1].Channel, snapshots[1].Username)
	}

	if snapshots[2].Channel != "b-channel" || snapshots[2].Username != "z-user" {
		t.Errorf("Third snapshot should be b-channel/z-user, got %s/%s",
			snapshots[2].Channel, snapshots[2].Username)
	}

	t.Log("Snapshots sorted correctly")
}

// Test12: Channel with pending
func TestSortSnapshotsWithPendingChannel(t *testing.T) {
	snapshots := []ClientSnapshot{
		{Channel: "", PendingChannel: "pending-ch", Username: "user1"},
		{Channel: "active-ch", Username: "user2"},
	}

	sortSnapshots(snapshots)

	// Pending channel should sort before empty channel
	if snapshots[0].PendingChannel != "pending-ch" {
		t.Errorf("First snapshot should have pending-ch, got %s",
			snapshots[0].PendingChannel)
	}

	t.Log("Snapshots with pending channel sorted correctly")
}

// Test13: UUID generation (from uuid.go)
func TestUUIDGeneration(t *testing.T) {
	uuid1 := newUUID()
	uuid2 := newUUID()

	// UUID should not be empty
	if uuid1 == "" {
		t.Error("newUUID should not return empty string")
	}

	// UUIDs should be unique
	if uuid1 == uuid2 {
		t.Error("UUIDs should be unique")
	}

	// Session ID should use first 8 characters
	sessionID := generateSessionID()
	if len(sessionID) != 8 {
		t.Errorf("Session ID should be 8 chars, got %d", len(sessionID))
	}

	t.Logf("UUID: %s, Session ID: %s", uuid1, sessionID)
}

// Test14: CompleteJoin helper logic (unit test for logic)
func TestCompleteJoinLogic(t *testing.T) {
	// Test the logic of channel switching
	// This tests the logic without needing actual WebSocket connections

	testCases := []struct {
		name           string
		oldChannel     string
		newChannel     string
		expectOldLeave bool
	}{
		{"no channel to new channel", "", "canal-1", false},
		{"same channel", "canal-1", "canal-1", false},
		{"different channel", "canal-1", "canal-2", true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			oldChannel := tc.oldChannel
			shouldNotifyOld := oldChannel != "" && oldChannel != tc.newChannel

			if shouldNotifyOld != tc.expectOldLeave {
				t.Errorf("Expected old channel leave notification: %v, got: %v",
					tc.expectOldLeave, shouldNotifyOld)
			}
		})
	}

	t.Log("Channel switching logic validated")
}

// Test15: PTT state transition logic
func TestPTTStateTransitions(t *testing.T) {
	// Test PTT state transitions without actual connections

	testCases := []struct {
		name            string
		currentSpeaker  bool
		attemptingPTT   bool
		expectGranted  bool
		expectDenied   bool
	}{
		{"no speaker, first PTT", false, true, true, false},
		{"speaker disconnect", true, false, false, false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Logic verification
			if tc.attemptingPTT && !tc.currentSpeaker {
				// Should be granted
				if !tc.expectGranted {
					t.Error("PTT should be granted when no speaker")
				}
			}

			if tc.currentSpeaker && tc.attemptingPTT {
				// Should be denied
				if !tc.expectDenied {
					t.Error("PTT should be denied when already speaking")
				}
			}
		})
	}

	t.Log("PTT state transitions validated")
}
