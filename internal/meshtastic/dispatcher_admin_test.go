package meshtastic

import (
	"testing"

	"github.com/karamble/diginode-cc/internal/serial"
	"github.com/karamble/diginode-cc/internal/ws"
)

type capturedAck struct {
	from      uint32
	requestID uint32
	payload   []byte
}

type fakeAdminReplyHandler struct {
	admin   []capturedAck
	routing []capturedAck
}

func (f *fakeAdminReplyHandler) HandleAdminReply(from, requestID uint32, payload []byte) {
	f.admin = append(f.admin, capturedAck{from, requestID, payload})
}

func (f *fakeAdminReplyHandler) HandleRoutingAck(from, requestID uint32, payload []byte) {
	f.routing = append(f.routing, capturedAck{from, requestID, payload})
}

// TestDispatcher_RoutesAdminAndRoutingToHandler ensures the dispatcher's port
// switch funnels both ADMIN and ROUTING packets into the AdminReplyHandler
// with the correct (from, request_id, payload) tuple. This is the only way
// fleetsec.Service learns when an outbound admin transaction has been acked.
func TestDispatcher_RoutesAdminAndRoutingToHandler(t *testing.T) {
	d := NewDispatcher(ws.NewHub(8))
	h := &fakeAdminReplyHandler{}
	d.SetAdminReplyHandler(h)

	// MarkLocal so duplicate-suppression and other gating doesn't filter.
	d.localNodeNum = 0xdeadbeef
	d.localNodeSeen = true

	tests := []struct {
		name       string
		port       uint32
		from       uint32
		packetID   uint32
		requestID  uint32
		payload    []byte
		wantAdmin  int
		wantRoute  int
	}{
		{"routing ack", uint32(PortNumRouting), 0xa1b2c3d4, 0xabcdef00, 0x12345678, []byte{0x08, 0x00}, 0, 1},
		{"admin reply", uint32(PortNumAdmin), 0xa1b2c3d4, 0xabcdef01, 0x87654321, []byte{0x10, 0x01}, 1, 0},
		{"unrelated port", uint32(PortNumTraceroute), 0xa1b2c3d4, 0xabcdef02, 0, nil, 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h.admin = nil
			h.routing = nil
			mp := &serial.MeshPacketData{
				From:      tc.from,
				To:        d.localNodeNum,
				ID:        tc.packetID,
				PortNum:   tc.port,
				Payload:   tc.payload,
				RequestID: tc.requestID,
			}
			d.handleMeshPacket(mp)

			if got := len(h.admin); got != tc.wantAdmin {
				t.Errorf("HandleAdminReply calls = %d, want %d", got, tc.wantAdmin)
			}
			if got := len(h.routing); got != tc.wantRoute {
				t.Errorf("HandleRoutingAck calls = %d, want %d", got, tc.wantRoute)
			}
			if tc.wantAdmin == 1 {
				got := h.admin[0]
				if got.from != tc.from || got.requestID != tc.requestID || string(got.payload) != string(tc.payload) {
					t.Errorf("admin ack got %+v, want from=%x req=%x payload=%x", got, tc.from, tc.requestID, tc.payload)
				}
			}
			if tc.wantRoute == 1 {
				got := h.routing[0]
				if got.from != tc.from || got.requestID != tc.requestID || string(got.payload) != string(tc.payload) {
					t.Errorf("routing ack got %+v, want from=%x req=%x payload=%x", got, tc.from, tc.requestID, tc.payload)
				}
			}
		})
	}
}

// TestDispatcher_NilAdminReplyHandlerDoesNotPanic confirms the dispatcher
// stays alive when no fleetsec.Service is wired (relevant during startup or
// in test harnesses) -- ADMIN/ROUTING packets are silently dropped, never
// crash the process.
func TestDispatcher_NilAdminReplyHandlerDoesNotPanic(t *testing.T) {
	d := NewDispatcher(ws.NewHub(8))
	d.localNodeNum = 0xdeadbeef
	d.localNodeSeen = true

	mp := &serial.MeshPacketData{
		From:      0xa1b2c3d4,
		To:        d.localNodeNum,
		ID:        0xabcdef00,
		PortNum:   uint32(PortNumAdmin),
		Payload:   []byte{0x10, 0x01},
		RequestID: 0x12345678,
	}
	d.handleMeshPacket(mp)
}
