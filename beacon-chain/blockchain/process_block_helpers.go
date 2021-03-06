package blockchain

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/gogo/protobuf/proto"
	"github.com/pkg/errors"
	ethpb "github.com/prysmaticlabs/ethereumapis/eth/v1alpha1"
	"github.com/prysmaticlabs/go-ssz"
	"github.com/prysmaticlabs/prysm/beacon-chain/core/helpers"
	"github.com/prysmaticlabs/prysm/beacon-chain/db/filters"
	pb "github.com/prysmaticlabs/prysm/proto/beacon/p2p/v1"
	"github.com/prysmaticlabs/prysm/shared/bytesutil"
	"github.com/prysmaticlabs/prysm/shared/featureconfig"
	"github.com/prysmaticlabs/prysm/shared/params"
	"github.com/prysmaticlabs/prysm/shared/traceutil"
	"github.com/sirupsen/logrus"
	"go.opencensus.io/trace"
)

// getBlockPreState returns the pre state of an incoming block. It uses the parent root of the block
// to retrieve the state in DB. It verifies the pre state's validity and the incoming block
// is in the correct time window.
func (s *Service) getBlockPreState(ctx context.Context, b *ethpb.BeaconBlock) (*pb.BeaconState, error) {
	ctx, span := trace.StartSpan(ctx, "forkchoice.getBlockPreState")
	defer span.End()

	// Verify incoming block has a valid pre state.
	preState, err := s.verifyBlkPreState(ctx, b)
	if err != nil {
		return nil, err
	}

	// Verify block slot time is not from the feature.
	if err := helpers.VerifySlotTime(preState.GenesisTime, b.Slot); err != nil {
		return nil, err
	}

	// Verify block is a descendent of a finalized block.
	if err := s.verifyBlkDescendant(ctx, bytesutil.ToBytes32(b.ParentRoot), b.Slot); err != nil {
		return nil, err
	}

	// Verify block is later than the finalized epoch slot.
	if err := s.verifyBlkFinalizedSlot(b); err != nil {
		return nil, err
	}

	return preState, nil
}

// verifyBlkPreState validates input block has a valid pre-state.
func (s *Service) verifyBlkPreState(ctx context.Context, b *ethpb.BeaconBlock) (*pb.BeaconState, error) {
	preState, err := s.beaconDB.State(ctx, bytesutil.ToBytes32(b.ParentRoot))
	if err != nil {
		return nil, errors.Wrapf(err, "could not get pre state for slot %d", b.Slot)
	}
	if preState == nil {
		return nil, fmt.Errorf("pre state of slot %d does not exist", b.Slot)
	}
	return preState, nil
}

// verifyBlkDescendant validates input block root is a descendant of the
// current finalized block root.
func (s *Service) verifyBlkDescendant(ctx context.Context, root [32]byte, slot uint64) error {
	ctx, span := trace.StartSpan(ctx, "forkchoice.verifyBlkDescendant")
	defer span.End()

	finalizedBlkSigned, err := s.beaconDB.Block(ctx, bytesutil.ToBytes32(s.finalizedCheckpt.Root))
	if err != nil || finalizedBlkSigned == nil || finalizedBlkSigned.Block == nil {
		return errors.Wrap(err, "could not get finalized block")
	}
	finalizedBlk := finalizedBlkSigned.Block

	bFinalizedRoot, err := s.ancestor(ctx, root[:], finalizedBlk.Slot)
	if err != nil {
		return errors.Wrap(err, "could not get finalized block root")
	}
	if !bytes.Equal(bFinalizedRoot, s.finalizedCheckpt.Root) {
		err := fmt.Errorf("block from slot %d is not a descendent of the current finalized block slot %d, %#x != %#x",
			slot, finalizedBlk.Slot, bytesutil.Trunc(bFinalizedRoot), bytesutil.Trunc(s.finalizedCheckpt.Root))
		traceutil.AnnotateError(span, err)
		return err
	}
	return nil
}

// verifyBlkFinalizedSlot validates input block is not less than or equal
// to current finalized slot.
func (s *Service) verifyBlkFinalizedSlot(b *ethpb.BeaconBlock) error {
	finalizedSlot := helpers.StartSlot(s.finalizedCheckpt.Epoch)
	if finalizedSlot >= b.Slot {
		return fmt.Errorf("block is equal or earlier than finalized block, slot %d < slot %d", b.Slot, finalizedSlot)
	}
	return nil
}

// saveNewValidators saves newly added validator indices from the state to db.
// Does nothing if validator count has not changed.
func (s *Service) saveNewValidators(ctx context.Context, preStateValidatorCount int, postState *pb.BeaconState) error {
	postStateValidatorCount := len(postState.Validators)
	if preStateValidatorCount != postStateValidatorCount {
		indices := make([]uint64, 0)
		pubKeys := make([][]byte, 0)
		for i := preStateValidatorCount; i < postStateValidatorCount; i++ {
			indices = append(indices, uint64(i))
			pubKeys = append(pubKeys, postState.Validators[i].PublicKey)
		}
		if err := s.beaconDB.SaveValidatorIndices(ctx, pubKeys, indices); err != nil {
			return errors.Wrapf(err, "could not save activated validators: %v", indices)
		}
		log.WithFields(logrus.Fields{
			"indices":             indices,
			"totalValidatorCount": postStateValidatorCount - preStateValidatorCount,
		}).Info("Validator indices saved in DB")
	}
	return nil
}

// rmStatesOlderThanLastFinalized deletes the states in db since last finalized check point.
func (s *Service) rmStatesOlderThanLastFinalized(ctx context.Context, startSlot uint64, endSlot uint64) error {
	ctx, span := trace.StartSpan(ctx, "forkchoice.rmStatesBySlots")
	defer span.End()

	// Make sure start slot is not a skipped slot
	for i := startSlot; i > 0; i-- {
		filter := filters.NewFilter().SetStartSlot(i).SetEndSlot(i)
		b, err := s.beaconDB.Blocks(ctx, filter)
		if err != nil {
			return err
		}
		if len(b) > 0 {
			startSlot = i
			break
		}
	}

	// Make sure finalized slot is not a skipped slot.
	for i := endSlot; i > 0; i-- {
		filter := filters.NewFilter().SetStartSlot(i).SetEndSlot(i)
		b, err := s.beaconDB.Blocks(ctx, filter)
		if err != nil {
			return err
		}
		if len(b) > 0 {
			endSlot = i - 1
			break
		}
	}

	// Do not remove genesis state
	if startSlot == 0 {
		startSlot++
	}
	// If end slot comes less than start slot
	if endSlot < startSlot {
		endSlot = startSlot
	}

	filter := filters.NewFilter().SetStartSlot(startSlot).SetEndSlot(endSlot)
	roots, err := s.beaconDB.BlockRoots(ctx, filter)
	if err != nil {
		return err
	}

	roots, err = s.filterBlockRoots(ctx, roots)
	if err != nil {
		return err
	}

	if err := s.beaconDB.DeleteStates(ctx, roots); err != nil {
		return err
	}

	return nil
}

// shouldUpdateCurrentJustified prevents bouncing attack, by only update conflicting justified
// checkpoints in the fork choice if in the early slots of the epoch.
// Otherwise, delay incorporation of new justified checkpoint until next epoch boundary.
// See https://ethresear.ch/t/prevention-of-bouncing-attack-on-ffg/6114 for more detailed analysis and discussion.
func (s *Service) shouldUpdateCurrentJustified(ctx context.Context, newJustifiedCheckpt *ethpb.Checkpoint) (bool, error) {
	if helpers.SlotsSinceEpochStarts(s.currentSlot()) < params.BeaconConfig().SafeSlotsToUpdateJustified {
		return true, nil
	}
	newJustifiedBlockSigned, err := s.beaconDB.Block(ctx, bytesutil.ToBytes32(newJustifiedCheckpt.Root))
	if err != nil {
		return false, err
	}
	if newJustifiedBlockSigned == nil || newJustifiedBlockSigned.Block == nil {
		return false, errors.New("nil new justified block")
	}
	newJustifiedBlock := newJustifiedBlockSigned.Block
	if newJustifiedBlock.Slot <= helpers.StartSlot(s.justifiedCheckpt.Epoch) {
		return false, nil
	}
	justifiedBlockSigned, err := s.beaconDB.Block(ctx, bytesutil.ToBytes32(s.justifiedCheckpt.Root))
	if err != nil {
		return false, err
	}
	if justifiedBlockSigned == nil || justifiedBlockSigned.Block == nil {
		return false, errors.New("nil justified block")
	}
	justifiedBlock := justifiedBlockSigned.Block
	b, err := s.ancestor(ctx, newJustifiedCheckpt.Root, justifiedBlock.Slot)
	if err != nil {
		return false, err
	}
	if !bytes.Equal(b, s.justifiedCheckpt.Root) {
		return false, nil
	}
	return true, nil
}

func (s *Service) updateJustified(ctx context.Context, state *pb.BeaconState) error {
	if state.CurrentJustifiedCheckpoint.Epoch > s.bestJustifiedCheckpt.Epoch {
		s.bestJustifiedCheckpt = state.CurrentJustifiedCheckpoint
	}
	canUpdate, err := s.shouldUpdateCurrentJustified(ctx, state.CurrentJustifiedCheckpoint)
	if err != nil {
		return err
	}
	if canUpdate {
		s.justifiedCheckpt = state.CurrentJustifiedCheckpoint
	}

	if featureconfig.Get().InitSyncCacheState {
		justifiedRoot := bytesutil.ToBytes32(state.CurrentJustifiedCheckpoint.Root)
		justifiedState := s.initSyncState[justifiedRoot]
		if err := s.beaconDB.SaveState(ctx, justifiedState, justifiedRoot); err != nil {
			return errors.Wrap(err, "could not save justified state")
		}
	}

	return s.beaconDB.SaveJustifiedCheckpoint(ctx, state.CurrentJustifiedCheckpoint)
}

// currentSlot returns the current slot based on time.
func (s *Service) currentSlot() uint64 {
	return uint64(time.Now().Unix()-s.genesisTime.Unix()) / params.BeaconConfig().SecondsPerSlot
}

// This receives cached state in memory for initial sync only during initial sync.
func (s *Service) cachedPreState(ctx context.Context, b *ethpb.BeaconBlock) (*pb.BeaconState, error) {
	if featureconfig.Get().InitSyncCacheState {
		preState := s.initSyncState[bytesutil.ToBytes32(b.ParentRoot)]
		var err error
		if preState == nil {
			preState, err = s.beaconDB.State(ctx, bytesutil.ToBytes32(b.ParentRoot))
			if err != nil {
				return nil, errors.Wrapf(err, "could not get pre state for slot %d", b.Slot)
			}
			if preState == nil {
				return nil, fmt.Errorf("pre state of slot %d does not exist", b.Slot)
			}
		}
		return proto.Clone(preState).(*pb.BeaconState), nil
	}

	preState, err := s.beaconDB.State(ctx, bytesutil.ToBytes32(b.ParentRoot))
	if err != nil {
		return nil, errors.Wrapf(err, "could not get pre state for slot %d", b.Slot)
	}
	if preState == nil {
		return nil, fmt.Errorf("pre state of slot %d does not exist", b.Slot)
	}

	return preState, nil
}

// This saves every finalized state in DB during initial sync, needed as part of optimization to
// use cache state during initial sync in case of restart.
func (s *Service) saveInitState(ctx context.Context, state *pb.BeaconState) error {
	if !featureconfig.Get().InitSyncCacheState {
		return nil
	}
	finalizedRoot := bytesutil.ToBytes32(state.FinalizedCheckpoint.Root)
	fs := s.initSyncState[finalizedRoot]

	if err := s.beaconDB.SaveState(ctx, fs, finalizedRoot); err != nil {
		return errors.Wrap(err, "could not save state")
	}
	for r, oldState := range s.initSyncState {
		if oldState.Slot < state.FinalizedCheckpoint.Epoch*params.BeaconConfig().SlotsPerEpoch {
			delete(s.initSyncState, r)
		}
	}
	return nil
}

// This filters block roots that are not known as head root and finalized root in DB.
// It serves as the last line of defence before we prune states.
func (s *Service) filterBlockRoots(ctx context.Context, roots [][32]byte) ([][32]byte, error) {
	f, err := s.beaconDB.FinalizedCheckpoint(ctx)
	if err != nil {
		return nil, err
	}
	fRoot := f.Root
	h, err := s.beaconDB.HeadBlock(ctx)
	if err != nil {
		return nil, err
	}
	hRoot, err := ssz.SigningRoot(h)
	if err != nil {
		return nil, err
	}

	filtered := make([][32]byte, 0, len(roots))
	for _, root := range roots {
		if bytes.Equal(root[:], fRoot[:]) || bytes.Equal(root[:], hRoot[:]) {
			continue
		}
		filtered = append(filtered, root)
	}

	return filtered, nil
}

// ancestor returns the block root of an ancestry block from the input block root.
//
// Spec pseudocode definition:
//   def get_ancestor(store: Store, root: Hash, slot: Slot) -> Hash:
//    block = store.blocks[root]
//    if block.slot > slot:
//      return get_ancestor(store, block.parent_root, slot)
//    elif block.slot == slot:
//      return root
//    else:
//      return Bytes32()  # root is older than queried slot: no results.
func (s *Service) ancestor(ctx context.Context, root []byte, slot uint64) ([]byte, error) {
	ctx, span := trace.StartSpan(ctx, "forkchoice.ancestor")
	defer span.End()

	// Stop recursive ancestry lookup if context is cancelled.
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	signed, err := s.beaconDB.Block(ctx, bytesutil.ToBytes32(root))
	if err != nil {
		return nil, errors.Wrap(err, "could not get ancestor block")
	}
	if signed == nil || signed.Block == nil {
		return nil, errors.New("nil block")
	}
	b := signed.Block

	// If we dont have the ancestor in the DB, simply return nil so rest of fork choice
	// operation can proceed. This is not an error condition.
	if b == nil || b.Slot < slot {
		return nil, nil
	}

	if b.Slot == slot {
		return root, nil
	}

	return s.ancestor(ctx, b.ParentRoot, slot)
}
