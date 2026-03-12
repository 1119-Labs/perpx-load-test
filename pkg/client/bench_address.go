package client

import (
	"crypto/sha256"
	"fmt"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// DeriveBenchAddress returns the bech32 address for the bench worker with the given ID.
// Uses the same derivation as seed and PerpxBankClient so the receiver pool matches
// the funded sender accounts (no sequential bottleneck: different senders → different receivers).
func DeriveBenchAddress(workerID int) string {
	seedStr := fmt.Sprintf("bench worker %d seed phrase for load testing account", workerID)
	seed := sha256.Sum256([]byte(seedStr))
	adjustedSeed := sha256.Sum256(append(seed[:], byte(workerID)))
	privKeyBytes, _ := btcec.PrivKeyFromBytes(adjustedSeed[:])
	privKey := &secp256k1.PrivKey{Key: privKeyBytes.Serialize()}
	addr := sdk.AccAddress(privKey.PubKey().Address())
	return addr.String()
}
