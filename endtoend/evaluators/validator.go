package evaluators

import (
	"context"
	"fmt"

	"github.com/pkg/errors"
	eth "github.com/prysmaticlabs/ethereumapis/eth/v1alpha1"
	"github.com/prysmaticlabs/prysm/shared/params"
)

// Evaluator defines the structure of the evaluators used to
// conduct the current beacon state during the E2E.
type Evaluator struct {
	Name       string
	Policy     func(currentEpoch uint64) bool
	Evaluation func(client eth.BeaconChainClient) error
}

// ValidatorsAreActive ensures the expected amount of validators are active.
var ValidatorsAreActive = Evaluator{
	Name:       "validators_active_epoch_%d",
	Policy:     onGenesisEpoch,
	Evaluation: validatorsAreActive,
}

// ValidatorsParticipating ensures the expected amount of validators are active.
var ValidatorsParticipating = Evaluator{
	Name:       "validators_participating_epoch_%d",
	Policy:     afterNthEpoch(3),
	Evaluation: validatorsParticipating,
}

func onGenesisEpoch(currentEpoch uint64) bool {
	return currentEpoch < 2
}

// Not including first epoch because of issues with genesis.
func afterNthEpoch(afterEpoch uint64) func(uint64) bool {
	return func(currentEpoch uint64) bool {
		return currentEpoch > afterEpoch
	}
}

func validatorsAreActive(client eth.BeaconChainClient) error {
	// Balances actually fluctuate but we just want to check initial balance.
	validatorRequest := &eth.ListValidatorsRequest{
		PageSize: int32(params.BeaconConfig().MinGenesisActiveValidatorCount),
	}
	validators, err := client.ListValidators(context.Background(), validatorRequest)
	if err != nil {
		return errors.Wrap(err, "failed to get validators")
	}

	expectedCount := params.BeaconConfig().MinGenesisActiveValidatorCount
	receivedCount := uint64(len(validators.ValidatorList))
	if expectedCount != receivedCount {
		return fmt.Errorf("expected validator count to be %d, recevied %d", expectedCount, receivedCount)
	}

	effBalanceLowCount := 0
	activeEpochWrongCount := 0
	exitEpochWrongCount := 0
	withdrawEpochWrongCount := 0
	for _, item := range validators.ValidatorList {
		if item.Validator.EffectiveBalance < params.BeaconConfig().MaxEffectiveBalance {
			effBalanceLowCount++
		}
		if item.Validator.ActivationEpoch != 0 {
			activeEpochWrongCount++
		}
		if item.Validator.ExitEpoch != params.BeaconConfig().FarFutureEpoch {
			exitEpochWrongCount++
		}
		if item.Validator.WithdrawableEpoch != params.BeaconConfig().FarFutureEpoch {
			withdrawEpochWrongCount++
		}
	}

	if effBalanceLowCount > 0 {
		return fmt.Errorf(
			"%d validators did not have genesis validator effective balance of %d",
			effBalanceLowCount,
			params.BeaconConfig().MaxEffectiveBalance,
		)
	} else if activeEpochWrongCount > 0 {
		return fmt.Errorf("%d validators did not have genesis validator epoch of 0", activeEpochWrongCount)
	} else if exitEpochWrongCount > 0 {
		return fmt.Errorf("%d validators did not have genesis validator exit epoch of far future epoch", exitEpochWrongCount)
	} else if activeEpochWrongCount > 0 {
		return fmt.Errorf("%d validators did not have genesis validator withdrawable epoch of far future epoch", activeEpochWrongCount)
	}

	return nil
}

// validatorsParticipating ensures the validators have an acceptable participation rate.
func validatorsParticipating(client eth.BeaconChainClient) error {
	validatorRequest := &eth.GetValidatorParticipationRequest{}
	participation, err := client.GetValidatorParticipation(context.Background(), validatorRequest)
	if err != nil {
		return errors.Wrap(err, "failed to get validator participation")
	}

	partRate := participation.Participation.GlobalParticipationRate
	expected := float32(1)
	if partRate < expected {
		return fmt.Errorf(
			"validator participation was below for epoch %d, expected %f, received: %f",
			participation.Epoch,
			expected,
			partRate,
		)
	}
	return nil
}
