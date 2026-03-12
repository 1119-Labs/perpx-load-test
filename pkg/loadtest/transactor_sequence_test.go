package loadtest

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/1119-Labs/perpx-load-test/internal/logging"
	"github.com/gorilla/websocket"
)

type fakeSequencedClient struct {
	mtx      sync.Mutex
	seq      uint64
	accepted int
}

func (c *fakeSequencedClient) GenerateTxWithSequenceAndEffects() ([]byte, uint64, func(), error) {
	c.mtx.Lock()
	seq := c.seq
	c.mtx.Unlock()
	// Tx bytes don't matter to the chain in this unit test; we just need stable bytes.
	tx := []byte{byte(seq)}
	return tx, seq, func() {
		c.mtx.Lock()
		c.accepted++
		c.mtx.Unlock()
	}, nil
}

func (c *fakeSequencedClient) CommitSequence(next uint64) {
	c.mtx.Lock()
	c.seq = next
	c.mtx.Unlock()
}

func (c *fakeSequencedClient) RecoverSequenceTo(next uint64) error {
	c.CommitSequence(next)
	return nil
}

func (c *fakeSequencedClient) RecoverSequence() error { return nil }

func (c *fakeSequencedClient) GenerateTx() ([]byte, error) { return []byte("unused"), nil }

func newWSServer(t *testing.T, responder func(tx []byte) (code int, log string)) (*httptest.Server, string) {
	t.Helper()
	up := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		for {
			var req RPCRequest
			if err := conn.ReadJSON(&req); err != nil {
				return
			}
			var p struct {
				Tx string `json:"tx"`
			}
			if err := json.Unmarshal(req.Params, &p); err != nil {
				return
			}
			txBytes, err := base64.StdEncoding.DecodeString(p.Tx)
			if err != nil {
				return
			}

			sum := sha256.Sum256(txBytes)
			hash := fmt.Sprintf("%X", sum[:])
			code, log := responder(txBytes)

			resBytes, _ := json.Marshal(map[string]any{
				"hash":      hash,
				"code":      code,
				"codespace": "sdk",
				"log":       log,
			})
			_ = conn.WriteJSON(RPCResponse{
				JSONRPC: "2.0",
				ID:      jsonRPCID,
				Result:  resBytes,
			})
		}
	}))

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/websocket"
	return srv, wsURL
}

func dialWS(t *testing.T, wsURL string) *websocket.Conn {
	t.Helper()
	c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	return c
}

func TestTransactor_StrictSequencing_CommitsSequenceFromZero(t *testing.T) {
	srv, wsURL := newWSServer(t, func(tx []byte) (int, string) {
		return 0, ""
	})
	defer srv.Close()

	cfg := &Config{
		BroadcastTxMethod: "sync",
		Rate:              1,
		SendPeriod:        1,
		Time:              1,
	}
	client := &fakeSequencedClient{seq: 0}

	tr := &Transactor{
		remoteAddr:        wsURL,
		config:            cfg,
		client:            client,
		logger:            logging.NewLogrusLogger("test-transactor"),
		conn:              dialWS(t, wsURL),
		broadcastTxMethod: "broadcast_tx_sync",
		checkTxResults:    make(chan checkTxResult, 128),
	}
	defer tr.conn.Close()

	tr.wg.Add(1)
	go tr.receiveLoop()

	if err := tr.sendTransactions(); err != nil {
		t.Fatalf("sendTransactions: %v", err)
	}

	client.mtx.Lock()
	defer client.mtx.Unlock()
	if client.seq != 1 {
		t.Fatalf("expected sequence to be committed to 1, got %d", client.seq)
	}
	if client.accepted != 1 {
		t.Fatalf("expected onAccept to run once, got %d", client.accepted)
	}
}

func TestTransactor_StrictSequencing_ImmediateRecoverOnCode32(t *testing.T) {
	srv, wsURL := newWSServer(t, func(tx []byte) (int, string) {
		// If tx[0] == 0, force a sequence mismatch with expected=5.
		if len(tx) > 0 && tx[0] == 0 {
			return 32, "account sequence mismatch, expected 5, got 0: incorrect account sequence"
		}
		return 0, ""
	})
	defer srv.Close()

	cfg := &Config{
		BroadcastTxMethod: "sync",
		Rate:              2,
		SendPeriod:        1,
		Time:              1,
	}
	client := &fakeSequencedClient{seq: 0}

	tr := &Transactor{
		remoteAddr:        wsURL,
		config:            cfg,
		client:            client,
		logger:            logging.NewLogrusLogger("test-transactor"),
		conn:              dialWS(t, wsURL),
		broadcastTxMethod: "broadcast_tx_sync",
		checkTxResults:    make(chan checkTxResult, 128),
	}
	defer tr.conn.Close()

	tr.wg.Add(1)
	go tr.receiveLoop()

	// sendTransactions will attempt Rate txs in this batch. First fails with code=32,
	// transactor should immediately RecoverSequenceTo(5) and continue, so the second
	// tx should succeed and commit sequence to 6.
	if err := tr.sendTransactions(); err != nil {
		t.Fatalf("sendTransactions: %v", err)
	}

	// Allow goroutine scheduling to finish any onAccept callbacks.
	time.Sleep(10 * time.Millisecond)

	client.mtx.Lock()
	defer client.mtx.Unlock()
	if client.seq != 6 {
		t.Fatalf("expected sequence to be 6 after recover+commit, got %d", client.seq)
	}
	if client.accepted != 1 {
		t.Fatalf("expected exactly one accepted tx, got %d", client.accepted)
	}
}

