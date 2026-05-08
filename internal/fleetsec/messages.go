package fleetsec

import pb "github.com/karamble/diginode-cc/internal/meshpb"

// Constructors for the AdminMessage variants the fleetsec service uses.
// Each returns a *meshpb.AdminMessage that the caller passes to one of
// serial.BuildAdminPacket or serial.BuildAdminPacketPKC, which wraps it
// in the MeshPacket+ToRadio envelope and returns the framed bytes plus
// the random packet id (for transaction-tracker correlation).
//
// Splitting the AdminMessage construction from the envelope keeps the
// envelope helpers fully reusable (e.g. for admin commands not in this
// file) and keeps the service code readable -- the call site reads as
// "build the message, address it, send".

// AdminGetChannel returns an AdminMessage requesting Channel[idx]'s
// current settings (name, role, PSK). Used as the first half of the
// "patch one PSK without clobbering name/role" pattern that PSK
// rotation uses.
//
// Wire encoding: the firmware's get_channel_request field is sent as
// (channel_index + 1) -- per the upstream proto comment, this avoids
// the protobuf-zero-value-is-absent gotcha that would otherwise make
// idx=0 indistinguishable from "field not set". Sending raw idx=0
// produces routing error BAD_REQUEST. The GetChannelResponse Channel
// proto's Index field is still 0-indexed (the actual slot).
func AdminGetChannel(idx uint32) *pb.AdminMessage {
	return &pb.AdminMessage{
		PayloadVariant: &pb.AdminMessage_GetChannelRequest{
			GetChannelRequest: idx + 1,
		},
	}
}

// AdminSetChannel pushes a fully-specified Channel: index + role +
// settings (name + PSK). Used as the second half of PSK rotation, with
// name and role read from a prior get_channel_response.
//
// PSK encoding follows Meshtastic spec (length 0/16/32). Caller is
// responsible for using ValidatePSK before calling -- this constructor
// trusts inputs.
func AdminSetChannel(idx int32, name string, role pb.Channel_Role, psk []byte) *pb.AdminMessage {
	return &pb.AdminMessage{
		PayloadVariant: &pb.AdminMessage_SetChannel{
			SetChannel: &pb.Channel{
				Index: idx,
				Role:  role,
				Settings: &pb.ChannelSettings{
					Name: name,
					Psk:  psk,
				},
			},
		},
	}
}

// AdminGetConfig requests the given Config sub-message. fleetsec uses
// this to read remote SecurityConfig (admin_key list + is_managed).
//
// configType is meshpb.AdminMessage_ConfigType -- common values:
//   - AdminMessage_DEVICE_CONFIG = 0
//   - AdminMessage_LORA_CONFIG = 5
//   - AdminMessage_BLUETOOTH_CONFIG = 6
//   - AdminMessage_SECURITY_CONFIG = 7  ← what fleetsec uses
func AdminGetConfig(configType pb.AdminMessage_ConfigType) *pb.AdminMessage {
	return &pb.AdminMessage{
		PayloadVariant: &pb.AdminMessage_GetConfigRequest{
			GetConfigRequest: configType,
		},
	}
}

// SecurityConfigUpdate captures the editable fields fleetsec writes to
// remote (or local) SecurityConfig. Pointer fields signal "leave
// unchanged" semantics -- a nil PrivateKey doesn't try to overwrite the
// node's existing private key, etc. AdminKeys is replace-in-full when
// non-nil; passing []byte{} clears the list.
//
// PrivateKey is only ever set when pushing to the LOCAL Heltec via
// BuildAdminPacket (never PKC). Remote PKC admin must not set
// PrivateKey; the SetSecurity helper enforces this.
type SecurityConfigUpdate struct {
	PublicKey            []byte    // nil = leave unchanged
	PrivateKey           []byte    // nil = leave unchanged; LOCAL admin only
	AdminKeys            [][]byte  // nil = leave unchanged; non-nil = replace in full
	IsManaged            *bool     // nil = leave unchanged
	AdminChannelEnabled  *bool     // nil = leave unchanged
}

// AdminSetSecurity returns an AdminMessage carrying a Config message
// with SecurityConfig populated only for the fields the operator is
// actually changing. Unchanged fields are omitted -- protobuf zero
// values would be misinterpreted by the firmware as "set to empty".
//
// The Meshtastic SecurityConfig design is a full-replace at the field
// level (not at the message level): if you push admin_key=[k1,k2] the
// firmware replaces the whole admin_key list with those two entries.
func AdminSetSecurity(u SecurityConfigUpdate) *pb.AdminMessage {
	sec := &pb.Config_SecurityConfig{}
	if u.PublicKey != nil {
		sec.PublicKey = u.PublicKey
	}
	if u.PrivateKey != nil {
		sec.PrivateKey = u.PrivateKey
	}
	if u.AdminKeys != nil {
		sec.AdminKey = u.AdminKeys
	}
	if u.IsManaged != nil {
		sec.IsManaged = *u.IsManaged
	}
	if u.AdminChannelEnabled != nil {
		sec.AdminChannelEnabled = *u.AdminChannelEnabled
	}
	return &pb.AdminMessage{
		PayloadVariant: &pb.AdminMessage_SetConfig{
			SetConfig: &pb.Config{
				PayloadVariant: &pb.Config_Security{Security: sec},
			},
		},
	}
}

// AdminBeginEditSettings opens a multi-step admin transaction on the
// target. While open, the firmware defers the implicit save+reboot
// behaviour after each set_config until CommitEditSettings closes the
// transaction. Required when batching admin_key + is_managed + channel
// changes that would otherwise each trigger their own save.
func AdminBeginEditSettings() *pb.AdminMessage {
	return &pb.AdminMessage{
		PayloadVariant: &pb.AdminMessage_BeginEditSettings{
			BeginEditSettings: true,
		},
	}
}

// AdminCommitEditSettings closes a multi-step admin transaction opened
// by BeginEditSettings.
func AdminCommitEditSettings() *pb.AdminMessage {
	return &pb.AdminMessage{
		PayloadVariant: &pb.AdminMessage_CommitEditSettings{
			CommitEditSettings: true,
		},
	}
}
