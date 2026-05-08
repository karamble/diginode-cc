package fleetsec

import (
	"context"
	"errors"
	"sync"
	"time"

	pb "github.com/karamble/diginode-cc/internal/meshpb"
	"google.golang.org/protobuf/proto"
)

// Transaction defaults. Plan §3.3 calls for 10s timeout for local admin,
// 30s for remote PKC; service code picks per-call.
const (
	DefaultLocalAdminTimeout  = 10 * time.Second
	DefaultRemoteAdminTimeout = 30 * time.Second
)

// ReplyKind identifies which kind of inbound packet completed a transaction.
type ReplyKind int

const (
	ReplyRoutingAck ReplyKind = iota + 1
	ReplyAdminMsg
	ReplyTimeout
	ReplyCancelled
)

// Reply is the message delivered on a transaction's reply channel exactly
// once. Only one of Routing or Admin is populated, depending on Kind.
//
// For ReplyRoutingAck:
//   - From is the node that sent the ack.
//   - Routing is the decoded meshpb.Routing payload (carries
//     Routing.error_reason; ErrorReason_NONE / 0 means success).
//
// For ReplyAdminMsg:
//   - From is the responder.
//   - Admin is the decoded meshpb.AdminMessage with one of the
//     get_*_response variants populated.
//
// For ReplyTimeout / ReplyCancelled: From=0, both pointers nil, Err set.
type Reply struct {
	Kind    ReplyKind
	From    uint32
	Routing *pb.Routing
	Admin   *pb.AdminMessage
	Err     error
}

// Successful reports whether a Routing ack carried error_reason=NONE.
// Returns false for Admin or non-ack replies (use Kind to distinguish
// them when needed).
func (r Reply) Successful() bool {
	return r.Kind == ReplyRoutingAck && r.Routing != nil &&
		r.Routing.GetErrorReason() == pb.Routing_NONE
}

// Tracker correlates inbound Routing acks and AdminMessage replies to
// outbound packets identified by their MeshPacket.id. Implements the
// fleetsec side of the meshtastic.AdminReplyHandler interface.
//
// Lifecycle: caller invokes Begin(id, timeout) before SendToRadio, gets
// a one-shot reply channel back, then waits on the channel. Either:
//
//   - Dispatcher delivers a Routing ack or AdminMessage with matching
//     request_id → Reply lands on the channel, transaction is removed.
//   - Timeout fires → ReplyTimeout lands on the channel, transaction is
//     removed.
//   - Caller's context is cancelled → ReplyCancelled lands on the channel,
//     transaction is removed.
//
// Channel is buffered (cap 1) so delivery never blocks the dispatcher.
type Tracker struct {
	mu           sync.Mutex
	transactions map[uint32]*pendingTx
}

type pendingTx struct {
	id     uint32
	kind   string
	reply  chan Reply
	cancel context.CancelFunc
}

// NewTracker constructs an empty Tracker.
func NewTracker() *Tracker {
	return &Tracker{transactions: make(map[uint32]*pendingTx)}
}

// Begin registers a new in-flight transaction for packet id, with the
// given timeout. Returns a one-shot reply channel that closes after the
// single Reply is sent (so a `for r := range ch` is safe).
//
// kind is purely diagnostic (e.g. "local-admin" / "remote-pkc-set-channel");
// it shows up in tests and audit logs but doesn't affect routing.
//
// If id is already pending, returns an error and does not overwrite.
func (t *Tracker) Begin(ctx context.Context, id uint32, kind string, timeout time.Duration) (<-chan Reply, error) {
	if id == 0 {
		return nil, errors.New("transaction id must be non-zero")
	}
	t.mu.Lock()
	if _, dup := t.transactions[id]; dup {
		t.mu.Unlock()
		return nil, errors.New("duplicate transaction id")
	}
	txCtx, cancel := context.WithTimeout(ctx, timeout)
	tx := &pendingTx{
		id:     id,
		kind:   kind,
		reply:  make(chan Reply, 1),
		cancel: cancel,
	}
	t.transactions[id] = tx
	t.mu.Unlock()

	// Watcher goroutine: fires on timeout or caller-context cancel,
	// delivers the appropriate Reply, and unregisters the transaction.
	go func() {
		<-txCtx.Done()
		// Race-safe removal: if a real reply already landed, the entry is
		// gone and removeAndClose returns nil; we don't double-deliver.
		if existing := t.removeAndClose(id); existing != nil {
			r := Reply{}
			if errors.Is(txCtx.Err(), context.DeadlineExceeded) {
				r.Kind = ReplyTimeout
				r.Err = errors.New("transaction timeout")
			} else {
				r.Kind = ReplyCancelled
				r.Err = ctx.Err()
			}
			existing.reply <- r
			close(existing.reply)
		}
	}()

	return tx.reply, nil
}

// removeAndClose looks up id, removes it from the map, cancels the
// timeout watcher's context, and returns the entry. Returns nil if id
// wasn't pending (i.e. it already completed). Caller is responsible for
// sending on entry.reply and closing the channel.
func (t *Tracker) removeAndClose(id uint32) *pendingTx {
	t.mu.Lock()
	tx, ok := t.transactions[id]
	if ok {
		delete(t.transactions, id)
	}
	t.mu.Unlock()
	if !ok {
		return nil
	}
	tx.cancel()
	return tx
}

// HandleRoutingAck implements meshtastic.AdminReplyHandler. Decodes the
// Routing payload via meshpb and delivers it to whichever transaction
// matches request_id, if any. Unmatched acks (request_id=0 or unknown)
// are dropped silently -- they may belong to a transaction that already
// timed out.
func (t *Tracker) HandleRoutingAck(from, requestID uint32, payload []byte) {
	if requestID == 0 {
		return
	}
	tx := t.removeAndClose(requestID)
	if tx == nil {
		return
	}
	r := Reply{Kind: ReplyRoutingAck, From: from}
	var routing pb.Routing
	if err := proto.Unmarshal(payload, &routing); err != nil {
		r.Err = err
	} else {
		r.Routing = &routing
	}
	tx.reply <- r
	close(tx.reply)
}

// HandleAdminReply implements meshtastic.AdminReplyHandler. Decodes the
// AdminMessage payload via meshpb and delivers it to the matching
// transaction. AdminMessage replies (get_*_response) carry data the
// service needs (current channel/config state); the variant is on the
// returned msg.GetPayloadVariant() switch.
func (t *Tracker) HandleAdminReply(from, requestID uint32, payload []byte) {
	if requestID == 0 {
		return
	}
	tx := t.removeAndClose(requestID)
	if tx == nil {
		return
	}
	r := Reply{Kind: ReplyAdminMsg, From: from}
	var admin pb.AdminMessage
	if err := proto.Unmarshal(payload, &admin); err != nil {
		r.Err = err
	} else {
		r.Admin = &admin
	}
	tx.reply <- r
	close(tx.reply)
}

// Pending reports the number of in-flight transactions. Used by tests
// and by the /admin/health endpoint.
func (t *Tracker) Pending() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.transactions)
}
