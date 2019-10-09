// Copyright 2017 The go-aerum Authors
// This file is part of the go-aerum library.
//
// The go-aerum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-aerum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-aerum library. If not, see <http://www.gnu.org/licenses/>.

// Package atmos implements the proof-of-authority consensus engine.
package atmos

import (
	"bytes"
	"errors"
	"io"
	"math"
	"math/big"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/AERUMTechnology/go-aerum/accounts"
	"github.com/AERUMTechnology/go-aerum/accounts/abi/bind"
	"github.com/AERUMTechnology/go-aerum/common"
	"github.com/AERUMTechnology/go-aerum/consensus"
	"github.com/AERUMTechnology/go-aerum/consensus/misc"
	guvnor "github.com/AERUMTechnology/go-aerum/contracts/atmosGovernance"
	"github.com/AERUMTechnology/go-aerum/core/state"
	"github.com/AERUMTechnology/go-aerum/core/types"
	"github.com/AERUMTechnology/go-aerum/crypto"
	"github.com/AERUMTechnology/go-aerum/ethclient"
	"github.com/AERUMTechnology/go-aerum/ethdb"
	"github.com/AERUMTechnology/go-aerum/log"
	"github.com/AERUMTechnology/go-aerum/params"
	"github.com/AERUMTechnology/go-aerum/rlp"
	"github.com/AERUMTechnology/go-aerum/rpc"
	lru "github.com/hashicorp/golang-lru"
	"golang.org/x/crypto/sha3"
)

const (
	inmemorySnapshots  = 128  // Number of recent vote snapshots to keep in memory
	inmemorySignatures = 4096 // Number of recent block signatures to keep in memory

	wiggleTime = 1000 * time.Millisecond // Random delay (per signer) to allow concurrent signers

	recentsTimeout  = 30 * time.Second // Timeout between signing blocks in case signer is recent
	numberOfSigners = 10               // Maximum number of signers available in epoch
)

// Atmos proof-of-authority protocol constants.
var (
	// Added by Aerum
	BlockReward = params.NewAtmosBlockRewards() // Block reward in wei for successfully mining a block

	epochLength = params.NewAtmosEpochInterval() // Default number of blocks after which to checkpoint and reset the pending votes
	blockPeriod = params.NewAtmosBlockInterval() // Default minimum difference between two consecutive block's timestamps

	extraVanity = 32 // Fixed number of extra-data prefix bytes reserved for signer vanity
	extraSeal   = 65 // Fixed number of extra-data suffix bytes reserved for signer seal

	uncleHash = types.CalcUncleHash(nil) // Always Keccak256(RLP([])) as uncles are meaningless outside of PoW.

	diffInTurn = big.NewInt(2) // Block difficulty for in-turn signatures
	diffNoTurn = big.NewInt(1) // Block difficulty for out-of-turn signatures
)

// Various error messages to mark blocks invalid. These should be private to
// prevent engine specific errors from being referenced in the remainder of the
// codebase, inherently breaking if the engine is swapped out. Please put common
// error types into the consensus package.
var (
	// errUnknownBlock is returned when the list of signers is requested for a block
	// that is not part of the local blockchain.
	errUnknownBlock = errors.New("unknown block")

	// errInvalidCheckpointBeneficiary is returned if a checkpoint/epoch transition
	// block has a beneficiary set to non-zeroes.
	errInvalidCheckpointBeneficiary = errors.New("beneficiary in checkpoint block non-zero")

	// errMissingVanity is returned if a block's extra-data section is shorter than
	// 32 bytes, which is required to store the signer vanity.
	errMissingVanity = errors.New("extra-data 32 byte vanity prefix missing")

	// errMissingSignature is returned if a block's extra-data section doesn't seem
	// to contain a 65 byte secp256k1 signature.
	errMissingSignature = errors.New("extra-data 65 byte signature suffix missing")

	// errExtraSigners is returned if non-checkpoint block contain signer data in
	// their extra-data fields.
	errExtraSigners = errors.New("non-checkpoint block contains extra signer list")

	// errInvalidCheckpointSigners is returned if a checkpoint block contains an
	// invalid list of signers (i.e. non divisible by 20 bytes).
	errInvalidCheckpointSigners = errors.New("invalid signer list on checkpoint block")

	// errMismatchingCheckpointSigners is returned if a checkpoint block contains a
	// list of signers different than the one the local node calculated.
	errMismatchingCheckpointSigners = errors.New("mismatching signer list on checkpoint block")

	// errInvalidMixDigest is returned if a block's mix digest is non-zero.
	errInvalidMixDigest = errors.New("non-zero mix digest")

	// errInvalidUncleHash is returned if a block contains an non-empty uncle list.
	errInvalidUncleHash = errors.New("non empty uncle hash")

	// errInvalidDifficulty is returned if the difficulty of a block neither 1 or 2.
	errInvalidDifficulty = errors.New("invalid difficulty")

	// errWrongDifficulty is returned if the difficulty of a block doesn't match the
	// turn of the signer.
	errWrongDifficulty = errors.New("wrong difficulty")

	// ErrInvalidTimestamp is returned if the timestamp of a block is lower than
	// the previous block's timestamp + the minimum block period.
	ErrInvalidTimestamp = errors.New("invalid timestamp")

	// errInvalidVotingChain is returned if an authorization list is attempted to
	// be modified via out-of-range or non-contiguous headers.
	errInvalidVotingChain = errors.New("invalid voting chain")

	// errUnauthorizedSigner is returned if a header is signed by a non-authorized entity.
	errUnauthorizedSigner = errors.New("unauthorized signer")

	// errRecentlySigned is returned if a header is signed by an authorized entity
	// that already signed a header recently, thus is temporarily not allowed to.
	errRecentlySigned = errors.New("recently signed")

	// Added by Aerum
	// errInvalidNumberOfSigners is returned if number of signers is less than 2.
	errInvalidNumberOfSigners = errors.New("invalid number of signers")
)

// SignerFn is a signer callback function to request a header to be signed by a
// backing account.
type SignerFn func(accounts.Account, string, []byte) ([]byte, error)

// ecrecover extracts the Ethereum account address from a signed header.
func ecrecover(header *types.Header, sigcache *lru.ARCCache) (common.Address, error) {
	// If the signature's already cached, return that
	hash := header.Hash()
	if address, known := sigcache.Get(hash); known {
		return address.(common.Address), nil
	}
	// Retrieve the signature from the header extra-data
	if len(header.Extra) < extraSeal {
		return common.Address{}, errMissingSignature
	}
	signature := header.Extra[len(header.Extra)-extraSeal:]

	// Recover the public key and the Ethereum address
	pubkey, err := crypto.Ecrecover(SealHash(header).Bytes(), signature)
	if err != nil {
		return common.Address{}, err
	}
	var signer common.Address
	copy(signer[:], crypto.Keccak256(pubkey[1:])[12:])

	sigcache.Add(hash, signer)
	return signer, nil
}

// Atmos is the proof-of-authority consensus engine proposed to support the
// Ethereum testnet following the Ropsten attacks.
type Atmos struct {
	config *params.AtmosConfig // Consensus engine configuration parameters
	db     ethdb.Database      // Database to store and retrieve snapshot checkpoints

	recents    *lru.ARCCache // Snapshots for recent block to speed up reorgs
	signatures *lru.ARCCache // Signatures of recent blocks to speed up mining

	signer common.Address // Ethereum address of the signing key
	signFn SignerFn       // Signer function to authorize hashes with
	lock   sync.RWMutex   // Protects the signer fields

	// The fields below are for testing only
	fakeDiff bool // Skip difficulty verifications
}

// New creates a Atmos proof-of-authority consensus engine with the initial
// signers set to the ones provided by the user.
func New(config *params.AtmosConfig, db ethdb.Database) *Atmos {
	// Set any missing consensus parameters to their defaults
	conf := *config
	if conf.Epoch == 0 {
		conf.Epoch = epochLength
	}
	// Allocate the snapshot caches and create the engine
	recents, _ := lru.NewARC(inmemorySnapshots)
	signatures, _ := lru.NewARC(inmemorySignatures)

	return &Atmos{
		config:     &conf,
		db:         db,
		recents:    recents,
		signatures: signatures,
	}
}

// Author implements consensus.Engine, returning the Ethereum address recovered
// from the signature in the header's extra-data section.
func (a *Atmos) Author(header *types.Header) (common.Address, error) {
	return ecrecover(header, a.signatures)
}

// VerifyHeader checks whether a header conforms to the consensus rules.
func (a *Atmos) VerifyHeader(chain consensus.ChainReader, header *types.Header, seal bool) error {
	return a.verifyHeader(chain, header, nil)
}

// VerifyHeaders is similar to VerifyHeader, but verifies a batch of headers. The
// method returns a quit channel to abort the operations and a results channel to
// retrieve the async verifications (the order is that of the input slice).
func (a *Atmos) VerifyHeaders(chain consensus.ChainReader, headers []*types.Header, seals []bool) (chan<- struct{}, <-chan error) {
	abort := make(chan struct{})
	results := make(chan error, len(headers))

	go func() {
		for i, header := range headers {
			err := a.verifyHeader(chain, header, headers[:i])

			select {
			case <-abort:
				return
			case results <- err:
			}
		}
	}()
	return abort, results
}

// verifyHeader checks whether a header conforms to the consensus rules.The
// caller may optionally pass in a batch of parents (ascending order) to avoid
// looking those up from the database. This is useful for concurrently verifying
// a batch of new headers.
func (a *Atmos) verifyHeader(chain consensus.ChainReader, header *types.Header, parents []*types.Header) error {
	if header.Number == nil {
		return errUnknownBlock
	}
	number := header.Number.Uint64()

	// Don't waste time checking blocks from the future
	if header.Time > uint64(time.Now().Unix()) {
		return consensus.ErrFutureBlock
	}
	// Checkpoint blocks need to enforce zero beneficiary
	checkpoint := (number % a.config.Epoch) == 0
	if checkpoint && header.Coinbase != (common.Address{}) {
		return errInvalidCheckpointBeneficiary
	}
	// Check that the extra-data contains both the vanity and signature
	if len(header.Extra) < extraVanity {
		return errMissingVanity
	}
	if len(header.Extra) < extraVanity+extraSeal {
		return errMissingSignature
	}
	// Ensure that the extra-data contains a signer list on checkpoint, but none otherwise
	signersBytes := len(header.Extra) - extraVanity - extraSeal
	if !checkpoint && signersBytes != 0 {
		return errExtraSigners
	}
	if checkpoint && signersBytes%common.AddressLength != 0 {
		return errInvalidCheckpointSigners
	}
	// Ensure that the mix digest is zero as we don't have fork protection currently
	if header.MixDigest != (common.Hash{}) {
		return errInvalidMixDigest
	}
	// Ensure that the block doesn't contain any uncles which are meaningless in PoA
	if header.UncleHash != uncleHash {
		return errInvalidUncleHash
	}
	// Ensure that the block's difficulty is meaningful (may not be correct at this point)
	if number > 0 {
		if header.Difficulty == nil || (header.Difficulty.Cmp(diffInTurn) != 0 && header.Difficulty.Cmp(diffNoTurn) != 0) {
			return errInvalidDifficulty
		}
	}
	// If all checks passed, validate any special fields for hard forks
	if err := misc.VerifyForkHashes(chain.Config(), header, false); err != nil {
		return err
	}
	// All basic checks passed, verify cascading fields
	return a.verifyCascadingFields(chain, header, parents)
}

// verifyCascadingFields verifies all the header fields that are not standalone,
// rather depend on a batch of previous headers. The caller may optionally pass
// in a batch of parents (ascending order) to avoid looking those up from the
// database. This is useful for concurrently verifying a batch of new headers.
func (a *Atmos) verifyCascadingFields(chain consensus.ChainReader, header *types.Header, parents []*types.Header) error {
	// The genesis block is the always valid dead-end
	number := header.Number.Uint64()
	if number == 0 {
		return nil
	}
	// Ensure that the block's timestamp isn't too close to it's parent
	parent := getParentHeader(chain, header, parents)
	if parent == nil {
		return consensus.ErrUnknownAncestor
	}
	if parent.Time+a.config.Period > header.Time {
		return ErrInvalidTimestamp
	}
	// Retrieve the snapshot needed to verify this header and cache it
	snap, err := a.snapshot(chain, number-1, header.ParentHash, parents)
	if err != nil {
		return err
	}
	// If the block is a checkpoint block, verify the signer list
	if number%a.config.Epoch == 0 {
		signers := make([]byte, len(snap.Signers)*common.AddressLength)
		for i, signer := range snap.signers() {
			copy(signers[i*common.AddressLength:], signer[:])
		}
		extraSuffix := len(header.Extra) - extraSeal
		if !bytes.Equal(header.Extra[extraVanity:extraSuffix], signers) {
			return errMismatchingCheckpointSigners
		}
	}
	// All basic checks passed, verify the seal and return
	return a.verifySeal(chain, header, parents)
}

// snapshot retrieves the authorization snapshot at a given point in time.
func (a *Atmos) snapshot(chain consensus.ChainReader, number uint64, hash common.Hash, parents []*types.Header) (*Snapshot, error) {
	// Search for a snapshot in memory or on disk for checkpoints
	var (
		headers []*types.Header
		snap    *Snapshot
	)
	for snap == nil {
		// If an in-memory snapshot was found, use that
		if s, ok := a.recents.Get(hash); ok {
			snap = s.(*Snapshot)
			break
		}
		// If we're at the genesis, snapshot the initial state. Alternatively if we're
		// at a checkpoint block without a parent (light client CHT), or we have piled
		// up more headers than allowed to be reorged (chain reinit from a freezer),
		// consider the checkpoint trusted and snapshot it.
		if number == 0 || (number%a.config.Epoch == 0 && (len(headers) > params.ImmutabilityThreshold || chain.GetHeaderByNumber(number-1) == nil)) {
			checkpoint := chain.GetHeaderByNumber(number)
			if checkpoint != nil {
				hash := checkpoint.Hash()

				signers := make([]common.Address, (len(checkpoint.Extra)-extraVanity-extraSeal)/common.AddressLength)
				for i := 0; i < len(signers); i++ {
					copy(signers[i][:], checkpoint.Extra[extraVanity+i*common.AddressLength:])
				}
				snap = newSnapshot(a.config, a.signatures, number, hash, signers)
				if err := snap.store(a.db); err != nil {
					return nil, err
				}
				log.Info("Stored checkpoint snapshot to disk", "number", number, "hash", hash)
				break
			}
		}
		// Added by Aerum
		// If epoch block load snapshot from or governance contract
		if number%a.config.Epoch == 0 {
			if s, err := loadSnapshot(a.config, a.signatures, a.db, hash); err == nil {
				log.Trace("Loaded voting snapshot from disk", "number", number, "hash", hash)
				snap = s
				break
			}
			// If snapshot not found in db load it from governance contract
			signers, err := getComposers(chain, a.config, number, parents)
			if err != nil {
				log.Error("Loaded snapshot from governance contract failed", "number", number, "hash", hash, "error", err)
				return nil, err
			}
			// Check number of signers returned from governance contract
			if len(signers) == 0 {
				log.Error("Loaded snapshot from governance contract contains no signers", "number", number, "hash", hash, "error", err)
				return nil, errInvalidNumberOfSigners
			}
			log.Trace("Loaded snapshot from governance contract", "number", number, "hash", hash)
			snap = newSnapshot(a.config, a.signatures, number, hash, signers)
			break
		}
		// No snapshot for this header, gather the header and move backward
		var header *types.Header
		if len(parents) > 0 {
			// If we have explicit parents, pick from there (enforced)
			header = parents[len(parents)-1]
			if header.Hash() != hash || header.Number.Uint64() != number {
				return nil, consensus.ErrUnknownAncestor
			}
			parents = parents[:len(parents)-1]
		} else {
			// No explicit parents (or no more left), reach out to the database
			header = chain.GetHeader(hash, number)
			if header == nil {
				return nil, consensus.ErrUnknownAncestor
			}
		}
		headers = append(headers, header)
		number, hash = number-1, header.ParentHash
	}
	// Previous snapshot found, apply any pending headers on top of it
	for i := 0; i < len(headers)/2; i++ {
		headers[i], headers[len(headers)-1-i] = headers[len(headers)-1-i], headers[i]
	}
	snap, err := snap.apply(headers)
	if err != nil {
		return nil, err
	}
	a.recents.Add(snap.Hash, snap)

	// If we've generated a new checkpoint snapshot, save to disk
	// Added by Aerum
	if snap.Number%a.config.Epoch == 0 && len(headers) > 0 {
		if err = snap.store(a.db); err != nil {
			return nil, err
		}
		log.Trace("Stored voting snapshot to disk", "number", snap.Number, "hash", snap.Hash)
	}
	return snap, err
}

// VerifyUncles implements consensus.Engine, always returning an error for any
// uncles as this consensus mechanism doesn't permit uncles.
func (a *Atmos) VerifyUncles(chain consensus.ChainReader, block *types.Block) error {
	if len(block.Uncles()) > 0 {
		return errors.New("uncles not allowed")
	}
	return nil
}

// VerifySeal implements consensus.Engine, checking whether the signature contained
// in the header satisfies the consensus protocol requirements.
func (a *Atmos) VerifySeal(chain consensus.ChainReader, header *types.Header) error {
	return a.verifySeal(chain, header, nil)
}

// verifySeal checks whether the signature contained in the header satisfies the
// consensus protocol requirements. The method accepts an optional list of parent
// headers that aren't yet part of the local blockchain to generate the snapshots
// from.
func (a *Atmos) verifySeal(chain consensus.ChainReader, header *types.Header, parents []*types.Header) error {
	// Verifying the genesis block is not supported
	number := header.Number.Uint64()
	if number == 0 {
		return errUnknownBlock
	}
	// Retrieve the snapshot needed to verify this header and cache it
	snap, err := a.snapshot(chain, number-1, header.ParentHash, parents)
	if err != nil {
		return err
	}

	// Resolve the authorization key and check against signers
	signer, err := ecrecover(header, a.signatures)
	if err != nil {
		return err
	}
	if _, ok := snap.Signers[signer]; !ok {
		return errUnauthorizedSigner
	}

	// NOTE: To be removed by Aerum. Disable recents updates for now
	for seen, recent := range snap.Recents {
		if recent == signer {
			// Signer is among recents, only fail if the current block doesn't shift it out
			if limit := uint64(len(snap.Signers)/2 + 1); seen > number-limit {
				return errRecentlySigned
			}
		}
	}

	//  NOTE: Added by Aerum. Disable recents updates for now
	//	for seen, recent := range snap.Recents {
	//		if recent == signer {
	//			// Signer is among recents, only fail if the current block doesn't shift it out
	//			if limit := uint64(len(snap.Signers)/2 + 1); seen > number-limit {
	//				// Ensure that the block's timestamp isn't too close to it's parent if it's recent
	//				parent := getParentHeader(chain, header, parents)
	//				if parent == nil {
	//					return consensus.ErrUnknownAncestor
	//				}
	//				if parent.Time+uint64(recentsTimeout.Seconds()) > header.Time {
	//					log.Error("Invalid block time. Recent signer is trying to sign block too fast", "parent time", parent.Time, "block time", header.Time, "block number", header.Number)
	//					return ErrInvalidTimestamp
	//				}
	//			}
	//		}
	//	}

	// Ensure that the difficulty corresponds to the turn-ness of the signer
	if !a.fakeDiff {
		inturn := snap.inturn(header.Number.Uint64(), signer)
		if inturn && header.Difficulty.Cmp(diffInTurn) != 0 {
			return errWrongDifficulty
		}
		if !inturn && header.Difficulty.Cmp(diffNoTurn) != 0 {
			return errWrongDifficulty
		}
	}
	return nil
}

// Prepare implements consensus.Engine, preparing all the consensus fields of the
// header for running the transactions on top.
func (a *Atmos) Prepare(chain consensus.ChainReader, header *types.Header) error {
	// If the block isn't a checkpoint, cast a random vote (good enough for now)
	header.Coinbase = common.Address{}
	header.Nonce = types.BlockNonce{}

	number := header.Number.Uint64()
	// Assemble the voting snapshot to check which votes make sense
	snap, err := a.snapshot(chain, number-1, header.ParentHash, nil)
	if err != nil {
		return err
	}

	// Set the correct difficulty
	header.Difficulty = CalcDifficulty(snap, a.signer)

	// Ensure the extra data has all it's components
	if len(header.Extra) < extraVanity {
		header.Extra = append(header.Extra, bytes.Repeat([]byte{0x00}, extraVanity-len(header.Extra))...)
	}
	header.Extra = header.Extra[:extraVanity]

	if number%a.config.Epoch == 0 {
		for _, signer := range snap.signers() {
			header.Extra = append(header.Extra, signer[:]...)
		}
	}
	header.Extra = append(header.Extra, make([]byte, extraSeal)...)

	// Mix digest is reserved for now, set to empty
	header.MixDigest = common.Hash{}

	// Ensure the timestamp has the correct delay
	parent := chain.GetHeader(header.ParentHash, number-1)
	if parent == nil {
		return consensus.ErrUnknownAncestor
	}
	header.Time = parent.Time + a.config.Period
	if header.Time < uint64(time.Now().Unix()) {
		header.Time = uint64(time.Now().Unix())
	}
	return nil
}

// Finalize implements consensus.Engine, ensuring no uncles are set, nor block
// rewards given.
func (a *Atmos) Finalize(chain consensus.ChainReader, header *types.Header, state *state.StateDB, txs []*types.Transaction, uncles []*types.Header) {
	// Added by Aerum
	// Accumulate any block rewards and commit the final state root
	accumulateRewards(a, state, header)

	header.Root = state.IntermediateRoot(chain.Config().IsEIP158(header.Number))
	header.UncleHash = types.CalcUncleHash(nil)
}

// FinalizeAndAssemble implements consensus.Engine, ensuring no uncles are set,
// nor block rewards given, and returns the final block.
func (a *Atmos) FinalizeAndAssemble(chain consensus.ChainReader, header *types.Header, state *state.StateDB, txs []*types.Transaction, uncles []*types.Header, receipts []*types.Receipt) (*types.Block, error) {
	// Added by Aerum
	// Accumulate any block rewards and commit the final state root
	accumulateRewards(a, state, header)

	header.Root = state.IntermediateRoot(chain.Config().IsEIP158(header.Number))
	header.UncleHash = types.CalcUncleHash(nil)

	// Assemble and return the final block for sealing
	return types.NewBlock(header, txs, nil, receipts), nil
}

// Authorize injects a private key into the consensus engine to mint new blocks
// with.
func (a *Atmos) Authorize(signer common.Address, signFn SignerFn) {
	a.lock.Lock()
	defer a.lock.Unlock()

	a.signer = signer
	a.signFn = signFn
}

// Seal implements consensus.Engine, attempting to create a sealed block using
// the local signing credentials.
func (a *Atmos) Seal(chain consensus.ChainReader, block *types.Block, results chan<- *types.Block, stop <-chan struct{}) error {
	header := block.Header()

	// Sealing the genesis block is not supported
	number := header.Number.Uint64()
	if number == 0 {
		return errUnknownBlock
	}
	// For 0-period chains, refuse to seal empty blocks (no reward but would spin sealing)
	if a.config.Period == 0 && len(block.Transactions()) == 0 {
		log.Info("Sealing paused, waiting for transactions")
		return nil
	}
	// Don't hold the signer fields for the entire sealing procedure
	a.lock.RLock()
	signer, signFn := a.signer, a.signFn
	a.lock.RUnlock()

	// Bail out if we're unauthorized to sign a block
	snap, err := a.snapshot(chain, number-1, header.ParentHash, nil)
	if err != nil {
		return err
	}
	if _, authorized := snap.Signers[signer]; !authorized {
		return errUnauthorizedSigner
	}

	// NOTE: To be removed by Aerum. Disable recents updates for now
	// If we're amongst the recent signers, wait for the next block
	for seen, recent := range snap.Recents {
		if recent == signer {
			// Signer is among recents, only wait if the current block doesn't shift it out
			if limit := uint64(len(snap.Signers)/2 + 1); number < limit || seen > number-limit {
				log.Info("Signed recently, must wait for others")
				return nil
			}
		}
	}

	// Sweet, the protocol permits us to sign the block, wait for our time
	delay := time.Unix(int64(header.Time), 0).Sub(time.Now()) // nolint: gosimple
	if header.Difficulty.Cmp(diffNoTurn) == 0 {
		// It's not our turn explicitly to sign, delay it a bit
		wiggle := time.Duration(len(snap.Signers)/2+1) * wiggleTime
		delay += time.Duration(rand.Int63n(int64(wiggle)))

		log.Trace("Out-of-turn signing requested", "wiggle", common.PrettyDuration(wiggle))
	}
	// Sign all the things!
	sighash, err := signFn(accounts.Account{Address: signer}, accounts.MimetypeAtmos, AtmosRLP(header))
	if err != nil {
		return err
	}
	copy(header.Extra[len(header.Extra)-extraSeal:], sighash)
	// Wait until sealing is terminated or delay timeout.
	log.Trace("Waiting for slot to sign and propagate", "delay", common.PrettyDuration(delay))

	//  NOTE: Added by Aerum. Disable recents updates for now
	//	for seen, recent := range snap.Recents {
	//		if recent == signer {
	//			// Signer is among recents, only wait if the current block doesn't shift it out
	//			if limit := uint64(len(snap.Signers)/2 + 1); number < limit || seen > number-limit {
	//
	//				// It's not our turn explicitly to sign, delay it a bit
	//				wiggle := time.Duration(len(snap.Signers)/2+1) * wiggleTime
	//				delay = recentsTimeout + time.Duration(rand.Int63n(int64(wiggle))) - (time.Duration(a.config.Period) * time.Second)
	//
	//				// Update header time to delayed one
	//				header.Time = header.Time+uint64(delay.Seconds())
	//
	//				log.Trace("Waiting for recent signer block signing", "delay", common.PrettyDuration(delay))
	//			}
	//		}
	//	}

	go func() {
		select {
		case <-stop:
			return
		case <-time.After(delay):
		}

		select {
		case results <- block.WithSeal(header):
		default:
			log.Warn("Sealing result is not read by miner", "sealhash", SealHash(header))
		}
	}()

	return nil
}

// CalcDifficulty is the difficulty adjustment algorithm. It returns the difficulty
// that a new block should have based on the previous blocks in the chain and the
// current signer.
func (a *Atmos) CalcDifficulty(chain consensus.ChainReader, time uint64, parent *types.Header) *big.Int {
	snap, err := a.snapshot(chain, parent.Number.Uint64(), parent.Hash(), nil)
	if err != nil {
		return nil
	}
	return CalcDifficulty(snap, a.signer)
}

// CalcDifficulty is the difficulty adjustment algorithm. It returns the difficulty
// that a new block should have based on the previous blocks in the chain and the
// current signer.
func CalcDifficulty(snap *Snapshot, signer common.Address) *big.Int {
	if snap.inturn(snap.Number+1, signer) {
		return new(big.Int).Set(diffInTurn)
	}
	return new(big.Int).Set(diffNoTurn)
}

// SealHash returns the hash of a block prior to it being sealed.
func (a *Atmos) SealHash(header *types.Header) common.Hash {
	return SealHash(header)
}

// Close implements consensus.Engine. It's a noop for atmos as there are no background threads.
func (a *Atmos) Close() error {
	return nil
}

// APIs implements consensus.Engine, returning the user facing RPC API to allow
// controlling the signer voting.
func (a *Atmos) APIs(chain consensus.ChainReader) []rpc.API {
	return []rpc.API{{
		Namespace: "atmos",
		Version:   "1.0",
		Service:   &API{chain: chain, atmos: a},
		Public:    false,
	}}
}

// SealHash returns the hash of a block prior to it being sealed.
func SealHash(header *types.Header) (hash common.Hash) {
	hasher := sha3.NewLegacyKeccak256()
	encodeSigHeader(hasher, header)
	hasher.Sum(hash[:0])
	return hash
}

// AtmosRLP returns the rlp bytes which needs to be signed for the proof-of-authority
// sealing. The RLP to sign consists of the entire header apart from the 65 byte signature
// contained at the end of the extra data.
//
// Note, the method requires the extra data to be at least 65 bytes, otherwise it
// panics. This is done to avoid accidentally using both forms (signature present
// or not), which could be abused to produce different hashes for the same header.
func AtmosRLP(header *types.Header) []byte {
	b := new(bytes.Buffer)
	encodeSigHeader(b, header)
	return b.Bytes()
}

func encodeSigHeader(w io.Writer, header *types.Header) {
	err := rlp.Encode(w, []interface{}{
		header.ParentHash,
		header.UncleHash,
		header.Coinbase,
		header.Root,
		header.TxHash,
		header.ReceiptHash,
		header.Bloom,
		header.Difficulty,
		header.Number,
		header.GasLimit,
		header.GasUsed,
		header.Time,
		header.Extra[:len(header.Extra)-65], // Yes, this will panic if extra is too short
		header.MixDigest,
		header.Nonce,
	})
	if err != nil {
		panic("can't encode: " + err.Error())
	}
}

// Added by Aerum
func getComposers(chain consensus.ChainReader, config *params.AtmosConfig, number uint64, parents []*types.Header) ([]common.Address, error) {
	ethereumApiEndpoint := getEthereumApiEndpoint(config)
	client, err := ethclient.Dial(ethereumApiEndpoint)
	if err != nil {
		return nil, err
	}

	governanceAddress := getGovernanceAddress(config)
	caller, err := guvnor.NewAtmosCaller(governanceAddress, client)
	if err != nil {
		return nil, err
	}

	composersCheckTimestamp := big.NewInt(0)
	if number > 0 {
		// Get previous block to get time from it
		prevHeader := getHeader(chain, parents, number-1)

		// Take composers for 20 minutes before now to make sure Ethereum syncs and there is no forks
		var ethereumSyncTimeoutInSeconds int64 = 20 * 60
		composersCheckTimestamp = big.NewInt(int64(prevHeader.Time) - ethereumSyncTimeoutInSeconds)
	}

	log.Info("Loading new headers", "number", number, "time", composersCheckTimestamp)
	addresses, _, err := caller.GetComposers(&bind.CallOpts{}, big.NewInt(int64(number)), composersCheckTimestamp)
	if err != nil {
		return nil, err
	}

	// We select only limited number of signers and shift them on every epoch
	selectedAddresses := signersProbabilisticSelection(config, addresses, number)

	// Log selected signers
	hexAddresses := make([]string, 0)
	for _, address := range selectedAddresses {
		hexAddresses = append(hexAddresses, address.Hex())
	}
	log.Info("New signers loaded", "signers", strings.Join(hexAddresses, ", "), "time", composersCheckTimestamp.String())

	return addresses, nil
}

// Added by Aerum
func signersProbabilisticSelection(config *params.AtmosConfig, addresses []common.Address, number uint64) []common.Address {
	actualNumberOfSigners := int(math.Min(float64(len(addresses)), numberOfSigners))
	start := int(number / config.Epoch) % actualNumberOfSigners
	log.Info("Selecting new signers", "actual number of signers", actualNumberOfSigners, "shift", start)

	// We select only limited number of signers and shift them on every epoch
	selectedAddresses := make([]common.Address, 0)
	for index := 0; index < actualNumberOfSigners; index++ {
		shiftIndex := (start + index) % len(addresses)
		selectedAddresses = append(selectedAddresses, addresses[shiftIndex])
	}

	return selectedAddresses
}

// Added by Aerum
func getHeader(chain consensus.ChainReader, parents []*types.Header, number uint64) *types.Header {
	// Check parents first
	if len(parents) > 0 {
		for _, p := range parents {
			if p.Number.Uint64() == number {
				return p
			}
		}
	}

	// If not found in parents try read from chain
	return chain.GetHeaderByNumber(number)
}

// Added by Aerum
func accumulateRewards(a *Atmos, state *state.StateDB, header *types.Header) {
	// Try to get block signer from the block header. Otherwise use atmos singer(on mining)
	signer, err := ecrecover(header, a.signatures)
	if err != nil {
		signer = a.signer
	}
	// Just add block rewards to signer
	state.AddBalance(signer, BlockReward)
}

// Added by Aerum
func getParentHeader(chain consensus.ChainReader, header *types.Header, parents []*types.Header) *types.Header {
	number := header.Number.Uint64()

	var parent *types.Header
	if len(parents) > 0 {
		parent = parents[len(parents)-1]
	} else {
		parent = chain.GetHeader(header.ParentHash, number-1)
	}
	if parent == nil || parent.Number.Uint64() != number-1 || parent.Hash() != header.ParentHash {
		return nil
	}

	return parent
}

// Added by Aerum
func getEthereumApiEndpoint(config *params.AtmosConfig) string {
	if config.EthereumApiEndpoint != "" {
		return config.EthereumApiEndpoint
	}
	if config.EnableTestNet {
		return params.NewAtmosTestEthereumRPCProvider()
	}
	return params.NewAtmosEthereumRPCProvider()
}

// Added by Aerum
func getGovernanceAddress(config *params.AtmosConfig) common.Address {
	if config.EthereumApiEndpoint != "" {
		return config.GovernanceAddress
	}
	if config.EnableTestNet {
		return params.NewAtmosTestGovernanceAddress()
	}
	return params.NewAtmosGovernanceAddress()
}
