package client

import (
	"crypto/sha256"
	"fmt"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

func GenerateDeterministicKeyAndAddress(workerID int) (cryptotypes.PrivKey, sdk.AccAddress) {
	seedStr := fmt.Sprintf("bench worker %d seed phrase for load testing account", workerID)
	seed := sha256.Sum256([]byte(seedStr))
	adjustedSeed := sha256.Sum256(append(seed[:], byte(workerID)))
	privKeyBytes, _ := btcec.PrivKeyFromBytes(adjustedSeed[:])
	privKey := &secp256k1.PrivKey{Key: privKeyBytes.Serialize()}
	addr := sdk.AccAddress(privKey.PubKey().Address())
	return privKey, addr
}
