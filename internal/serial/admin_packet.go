package serial

import (
	"fmt"

	pb "github.com/karamble/diginode-cc/internal/meshpb"
	"google.golang.org/protobuf/proto"
)

// BuildAdminPacket marshals a meshpb.AdminMessage into a ToRadio packet
// addressed to the local Heltec, for local admin operations (no PKC).
// Returns the encoded ToRadio bytes (caller passes to Manager.SendToRadio,
// which wraps them in a frame) and the packet ID so the caller can
// correlate the eventual Routing ack and AdminMessage reply.
//
// Data.want_response=true tells the firmware to follow the transport
// Routing ack with a get_*_response AdminMessage carrying the queried
// data. Without it the firmware processes the admin request and only
// emits a Routing ack -- Get*Request transactions then time out waiting
// for a payload that never arrives.
func BuildAdminPacket(localNodeNum uint32, msg *pb.AdminMessage) ([]byte, uint32, error) {
	if msg == nil {
		return nil, 0, fmt.Errorf("nil AdminMessage")
	}
	payload, err := proto.Marshal(msg)
	if err != nil {
		return nil, 0, fmt.Errorf("marshal AdminMessage: %w", err)
	}
	id := randomPacketID()
	mp := &pb.MeshPacket{
		To:       localNodeNum,
		Channel:  0,
		Id:       id,
		HopLimit: 3,
		WantAck:  true,
		PayloadVariant: &pb.MeshPacket_Decoded{
			Decoded: &pb.Data{
				Portnum:      pb.PortNum_ADMIN_APP,
				Payload:      payload,
				WantResponse: true,
			},
		},
	}
	tr := &pb.ToRadio{
		PayloadVariant: &pb.ToRadio_Packet{Packet: mp},
	}
	out, err := proto.Marshal(tr)
	if err != nil {
		return nil, 0, fmt.Errorf("marshal ToRadio: %w", err)
	}
	return out, id, nil
}

// BuildAdminPacketPKC marshals an AdminMessage into a remote-admin ToRadio
// packet. Sets PkiEncrypted=true so the local Heltec encrypts the payload to
// the recipient's pubkey from its NodeDB (Curve25519 ECDH + AES-CCM, all
// done on-device); the diginode-cc backend never touches Curve25519 itself.
// WantAck=true so the caller learns whether the remote applied the change.
// WantResponse=true so Get*Request variants get a get_*_response payload
// back from the remote (firmware suppresses it otherwise).
func BuildAdminPacketPKC(remoteNodeNum uint32, msg *pb.AdminMessage) ([]byte, uint32, error) {
	if msg == nil {
		return nil, 0, fmt.Errorf("nil AdminMessage")
	}
	if remoteNodeNum == 0 || remoteNodeNum == BroadcastAddr {
		return nil, 0, fmt.Errorf("remote admin requires a unicast destination, got %d", remoteNodeNum)
	}
	payload, err := proto.Marshal(msg)
	if err != nil {
		return nil, 0, fmt.Errorf("marshal AdminMessage: %w", err)
	}
	id := randomPacketID()
	mp := &pb.MeshPacket{
		To:           remoteNodeNum,
		Channel:      0,
		Id:           id,
		HopLimit:     3,
		WantAck:      true,
		PkiEncrypted: true,
		PayloadVariant: &pb.MeshPacket_Decoded{
			Decoded: &pb.Data{
				Portnum:      pb.PortNum_ADMIN_APP,
				Payload:      payload,
				WantResponse: true,
			},
		},
	}
	tr := &pb.ToRadio{
		PayloadVariant: &pb.ToRadio_Packet{Packet: mp},
	}
	out, err := proto.Marshal(tr)
	if err != nil {
		return nil, 0, fmt.Errorf("marshal ToRadio: %w", err)
	}
	return out, id, nil
}
