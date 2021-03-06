package endtoend

import (
	"bufio"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/bazelbuild/rules_go/go/tools/bazel"
	"github.com/ethereum/go-ethereum/common"
	"github.com/pkg/errors"
	ev "github.com/prysmaticlabs/prysm/endtoend/evaluators"
	"github.com/prysmaticlabs/prysm/shared/params"
)

type beaconNodeInfo struct {
	processID   int
	datadir     string
	rpcPort     uint64
	monitorPort uint64
	grpcPort    uint64
	multiAddr   string
}

type end2EndConfig struct {
	beaconFlags    []string
	validatorFlags []string
	tmpPath        string
	epochsToRun    uint64
	numValidators  uint64
	numBeaconNodes uint64
	contractAddr   common.Address
	evaluators     []ev.Evaluator
}

var beaconNodeLogFileName = "beacon-%d.log"

// startBeaconNodes starts the requested amount of beacon nodes, passing in the deposit contract given.
func startBeaconNodes(t *testing.T, config *end2EndConfig) []*beaconNodeInfo {
	numNodes := config.numBeaconNodes

	nodeInfo := []*beaconNodeInfo{}
	for i := uint64(0); i < numNodes; i++ {
		newNode := startNewBeaconNode(t, config, nodeInfo)
		nodeInfo = append(nodeInfo, newNode)
	}

	return nodeInfo
}

func startNewBeaconNode(t *testing.T, config *end2EndConfig, beaconNodes []*beaconNodeInfo) *beaconNodeInfo {
	tmpPath := config.tmpPath
	index := len(beaconNodes)
	binaryPath, found := bazel.FindBinary("beacon-chain", "beacon-chain")
	if !found {
		t.Log(binaryPath)
		t.Fatal("beacon chain binary not found")
	}

	stdOutFile, err := os.Create(path.Join(tmpPath, fmt.Sprintf(beaconNodeLogFileName, index)))
	if err != nil {
		t.Fatal(err)
	}

	args := []string{
		"--no-genesis-delay",
		"--force-clear-db",
		"--no-discovery",
		"--http-web3provider=http://127.0.0.1:8745",
		"--web3provider=ws://127.0.0.1:8746",
		fmt.Sprintf("--datadir=%s/eth2-beacon-node-%d", tmpPath, index),
		fmt.Sprintf("--deposit-contract=%s", config.contractAddr.Hex()),
		fmt.Sprintf("--rpc-port=%d", 4200+index),
		fmt.Sprintf("--p2p-udp-port=%d", 12200+index),
		fmt.Sprintf("--p2p-tcp-port=%d", 13200+index),
		fmt.Sprintf("--monitoring-port=%d", 8280+index),
		fmt.Sprintf("--grpc-gateway-port=%d", 3400+index),
		fmt.Sprintf("--contract-deployment-block=%d", 0),
		fmt.Sprintf("--rpc-max-page-size=%d", params.BeaconConfig().MinGenesisActiveValidatorCount),
	}
	args = append(args, config.beaconFlags...)

	// After the first node is made, have all following nodes connect to all previously made nodes.
	if index >= 1 {
		for p := 0; p < index; p++ {
			args = append(args, fmt.Sprintf("--peer=%s", beaconNodes[p].multiAddr))
		}
	}

	t.Logf("Starting beacon chain %d with flags: %s", index, strings.Join(args, " "))
	cmd := exec.Command(binaryPath, args...)
	cmd.Stdout = stdOutFile
	cmd.Stderr = stdOutFile
	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start beacon node: %v", err)
	}

	if err = waitForTextInFile(stdOutFile, "Node started p2p server"); err != nil {
		t.Fatalf("could not find multiaddr for node %d, this means the node had issues starting: %v", index, err)
	}

	multiAddr, err := getMultiAddrFromLogFile(stdOutFile.Name())
	if err != nil {
		t.Fatalf("could not get multiaddr for node %d: %v", index, err)
	}

	return &beaconNodeInfo{
		processID:   cmd.Process.Pid,
		datadir:     fmt.Sprintf("%s/eth2-beacon-node-%d", tmpPath, index),
		rpcPort:     4200 + uint64(index),
		monitorPort: 8280 + uint64(index),
		grpcPort:    3400 + uint64(index),
		multiAddr:   multiAddr,
	}
}

func getMultiAddrFromLogFile(name string) (string, error) {
	byteContent, err := ioutil.ReadFile(name)
	if err != nil {
		return "", err
	}
	contents := string(byteContent)

	searchText := "\"Node started p2p server\" multiAddr=\""
	startIdx := strings.Index(contents, searchText)
	if startIdx == -1 {
		return "", fmt.Errorf("did not find peer text in %s", contents)
	}
	startIdx += len(searchText)
	endIdx := strings.Index(contents[startIdx:], "\"")
	if endIdx == -1 {
		return "", fmt.Errorf("did not find peer text in %s", contents)
	}
	return contents[startIdx : startIdx+endIdx], nil
}

func waitForTextInFile(file *os.File, text string) error {
	wait := 0
	// Cap the wait in case there are issues starting.
	maxWait := 36
	for wait < maxWait {
		time.Sleep(2 * time.Second)
		// Rewind the file pointer to the start of the file so we can read it again.
		_, err := file.Seek(0, io.SeekStart)
		if err != nil {
			return errors.Wrap(err, "could not rewind file to start")
		}

		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			if strings.Contains(scanner.Text(), text) {
				return nil
			}
		}
		if err := scanner.Err(); err != nil {
			return err
		}
		wait += 2
	}
	contents, err := ioutil.ReadFile(file.Name())
	if err != nil {
		return err
	}
	return fmt.Errorf("could not find requested text \"%s\" in logs:\n%s", text, string(contents))
}
