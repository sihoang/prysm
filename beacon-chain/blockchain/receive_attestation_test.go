package blockchain

import (
	"testing"
	"time"

	ethpb "github.com/prysmaticlabs/ethereumapis/eth/v1alpha1"
	"github.com/prysmaticlabs/go-ssz"
	testDB "github.com/prysmaticlabs/prysm/beacon-chain/db/testing"
	pb "github.com/prysmaticlabs/prysm/proto/beacon/p2p/v1"
	"github.com/prysmaticlabs/prysm/shared/testutil"
	logTest "github.com/sirupsen/logrus/hooks/test"
	"golang.org/x/net/context"
)

func TestReceiveAttestationNoPubsub_ProcessCorrectly(t *testing.T) {
	hook := logTest.NewGlobal()
	db := testDB.SetupDB(t)
	defer testDB.TeardownDB(t, db)
	ctx := context.Background()

	chainService := setupBeaconChain(t, db)
	r, _ := ssz.HashTreeRoot(&ethpb.BeaconBlock{})
	chainService.forkChoiceStoreOld = &store{headRoot: r[:]}

	b := &ethpb.SignedBeaconBlock{Block: &ethpb.BeaconBlock{}}
	if err := chainService.beaconDB.SaveBlock(ctx, b); err != nil {
		t.Fatal(err)
	}
	root, err := ssz.HashTreeRoot(b.Block)
	if err != nil {
		t.Fatal(err)
	}
	if err := chainService.beaconDB.SaveState(ctx, &pb.BeaconState{}, root); err != nil {
		t.Fatal(err)
	}

	a := &ethpb.Attestation{Data: &ethpb.AttestationData{
		Target: &ethpb.Checkpoint{Root: root[:]},
	}}
	if err := chainService.ReceiveAttestationNoPubsub(ctx, a); err != nil {
		t.Fatal(err)
	}

	testutil.AssertLogsContain(t, hook, "Saved new head info")
	testutil.AssertLogsDoNotContain(t, hook, "Broadcasting attestation")
}

func TestVerifyCheckpointEpoch_Ok(t *testing.T) {
	db := testDB.SetupDB(t)
	defer testDB.TeardownDB(t, db)

	chainService := setupBeaconChain(t, db)
	chainService.genesisTime = time.Now()

	if !chainService.verifyCheckpointEpoch(&ethpb.Checkpoint{}) {
		t.Error("Wanted true, got false")
	}

	if chainService.verifyCheckpointEpoch(&ethpb.Checkpoint{Epoch: 1}) {
		t.Error("Wanted false, got true")
	}
}
