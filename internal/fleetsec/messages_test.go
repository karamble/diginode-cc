package fleetsec

import (
	"bytes"
	"testing"

	pb "github.com/karamble/diginode-cc/internal/meshpb"
	"google.golang.org/protobuf/proto"
)

// roundTripAdmin marshals + unmarshals an AdminMessage so we exercise
// the actual wire format the firmware will see, not just the in-memory
// struct.
func roundTripAdmin(t *testing.T, m *pb.AdminMessage) *pb.AdminMessage {
	t.Helper()
	b, err := proto.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out pb.AdminMessage
	if err := proto.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return &out
}

func TestAdminGetChannel(t *testing.T) {
	m := roundTripAdmin(t, AdminGetChannel(2))
	if m.GetGetChannelRequest() != 2 {
		t.Errorf("get_channel_request = %d, want 2", m.GetGetChannelRequest())
	}
}

func TestAdminSetChannel(t *testing.T) {
	psk := bytes.Repeat([]byte{0xab}, 16)
	m := roundTripAdmin(t, AdminSetChannel(0, "halberd-data", pb.Channel_PRIMARY, psk))

	ch := m.GetSetChannel()
	if ch == nil {
		t.Fatal("set_channel variant missing")
	}
	if ch.GetIndex() != 0 {
		t.Errorf("index = %d", ch.GetIndex())
	}
	if ch.GetRole() != pb.Channel_PRIMARY {
		t.Errorf("role = %v", ch.GetRole())
	}
	if ch.GetSettings().GetName() != "halberd-data" {
		t.Errorf("name = %q", ch.GetSettings().GetName())
	}
	if !bytes.Equal(ch.GetSettings().GetPsk(), psk) {
		t.Error("psk mismatch")
	}
}

func TestAdminGetConfig(t *testing.T) {
	m := roundTripAdmin(t, AdminGetConfig(pb.AdminMessage_SECURITY_CONFIG))
	if m.GetGetConfigRequest() != pb.AdminMessage_SECURITY_CONFIG {
		t.Errorf("get_config_request = %v", m.GetGetConfigRequest())
	}
}

func TestAdminSetSecurity_OnlySetsProvidedFields(t *testing.T) {
	t.Run("admin_keys only", func(t *testing.T) {
		k1 := bytes.Repeat([]byte{0x11}, 32)
		k2 := bytes.Repeat([]byte{0x22}, 32)
		m := roundTripAdmin(t, AdminSetSecurity(SecurityConfigUpdate{
			AdminKeys: [][]byte{k1, k2},
		}))
		sec := m.GetSetConfig().GetSecurity()
		if sec == nil {
			t.Fatal("security config missing")
		}
		if got := sec.GetAdminKey(); len(got) != 2 || !bytes.Equal(got[0], k1) || !bytes.Equal(got[1], k2) {
			t.Errorf("admin_key = %v, want both", got)
		}
		if sec.GetIsManaged() {
			t.Error("is_managed should be false (default, not provided)")
		}
		if len(sec.GetPublicKey()) != 0 || len(sec.GetPrivateKey()) != 0 {
			t.Error("public/private keys should be empty when not provided")
		}
	})

	t.Run("is_managed true", func(t *testing.T) {
		yes := true
		m := roundTripAdmin(t, AdminSetSecurity(SecurityConfigUpdate{
			IsManaged: &yes,
		}))
		sec := m.GetSetConfig().GetSecurity()
		if !sec.GetIsManaged() {
			t.Error("is_managed false")
		}
	})

	t.Run("clear admin_keys", func(t *testing.T) {
		m := roundTripAdmin(t, AdminSetSecurity(SecurityConfigUpdate{
			AdminKeys: [][]byte{},
		}))
		sec := m.GetSetConfig().GetSecurity()
		if got := sec.GetAdminKey(); len(got) != 0 {
			t.Errorf("admin_key = %v, want empty", got)
		}
	})

	t.Run("local-only keypair set", func(t *testing.T) {
		priv := bytes.Repeat([]byte{0x33}, 32)
		pub := bytes.Repeat([]byte{0x44}, 32)
		m := roundTripAdmin(t, AdminSetSecurity(SecurityConfigUpdate{
			PublicKey:  pub,
			PrivateKey: priv,
		}))
		sec := m.GetSetConfig().GetSecurity()
		if !bytes.Equal(sec.GetPublicKey(), pub) || !bytes.Equal(sec.GetPrivateKey(), priv) {
			t.Error("keypair did not survive round-trip")
		}
	})
}

func TestAdminBeginCommitEditSettings(t *testing.T) {
	begin := roundTripAdmin(t, AdminBeginEditSettings())
	if !begin.GetBeginEditSettings() {
		t.Error("begin_edit_settings = false")
	}
	commit := roundTripAdmin(t, AdminCommitEditSettings())
	if !commit.GetCommitEditSettings() {
		t.Error("commit_edit_settings = false")
	}
}
