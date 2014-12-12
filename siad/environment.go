package main

import (
	"fmt"
	"html/template"
	"os"
	"sync"
	"time"

	"github.com/NebulousLabs/Andromeda/network"
	"github.com/NebulousLabs/Andromeda/siacore"
	"github.com/mitchellh/go-homedir"
)

// Environment is the struct that serves as the state for siad. It contains a
// pointer to the state, as things like a wallet, a friend list, etc. Each
// environment should have its own state.
type Environment struct {
	state *siacore.State

	server       *network.TCPServer
	host         *Host
	hostDatabase *HostDatabase
	renter       *Renter
	wallet       *Wallet

	friends map[string]siacore.CoinAddress

	// Channels for incoming blocks and transactions to be processed
	blockChan       chan siacore.Block
	transactionChan chan siacore.Transaction

	// Mining variables. The mining variables are protected by the miningLock.
	// Any time that you read from or write to any of the mining variables, you
	// need to be under a lock.
	mining        bool         // true when mining
	miningThreads int          // number of processes mining at once
	miningLock    sync.RWMutex // prevents benign race conditions

	// Envrionment directories.
	template    *template.Template
	hostDir     string
	styleDir    string
	downloadDir string
}

// createEnvironment creates a server, host, miner, renter and wallet and
// puts it all in a single environment struct that's used as the state for the
// main package.
func CreateEnvironment(config Config) (e *Environment, err error) {
	// Expand the input directories, replacing '~' with the home path.
	expandedHostDir, err := homedir.Expand(config.Siad.HostDirectory)
	if err != nil {
		err = fmt.Errorf("problem with hostDir: %v", err)
		return
	}
	expandedStyleDir, err := homedir.Expand(config.Siad.StyleDirectory)
	if err != nil {
		err = fmt.Errorf("problem with styleDir: %v", err)
		return
	}
	expandedDownloadDir, err := homedir.Expand(config.Siad.DownloadDirectory)
	if err != nil {
		err = fmt.Errorf("problem with downloadDir: %v", err)
		return
	}

	// Check that template.html exists.
	if _, err = os.Stat(expandedStyleDir + "template.html"); err != nil {
		err = fmt.Errorf("template.html not found! Please put the styles/ folder into '%v'", expandedStyleDir)
		return
	}

	e = &Environment{
		state:           siacore.CreateGenesisState(),
		friends:         make(map[string]siacore.CoinAddress),
		blockChan:       make(chan siacore.Block, 100),
		transactionChan: make(chan siacore.Transaction, 100),
		hostDir:         expandedHostDir,
		styleDir:        expandedStyleDir,
		downloadDir:     expandedDownloadDir,
	}
	e.hostDatabase = CreateHostDatabase()
	e.host = CreateHost()
	e.renter = CreateRenter()
	e.wallet = CreateWallet(e.state)

	// Bootstrap to the network.
	err = e.initializeNetwork(config.Siad.RpcPort, config.Siad.NoBootstrap)
	if err != nil {
		return
	}
	e.host.Settings.IPAddress = e.server.NetAddress()

	// create downloads directory and host directory.
	err = os.MkdirAll(e.downloadDir, os.ModeDir|os.ModePerm)
	if err != nil {
		return
	}
	err = os.MkdirAll(e.hostDir, os.ModeDir|os.ModePerm)
	if err != nil {
		return
	}

	// Create the web interface template.
	e.template = template.Must(template.ParseFiles(e.styleDir + "template.html"))

	// Begin listening for requests on the api.
	e.setUpHandlers(config.Siad.ApiPort)

	return
}

// Close does any finishing maintenence before the environment can be garbage
// collected. Right now that just means closing the server.
func (e *Environment) Close() {
	e.server.Close()
}

// initializeNetwork registers the rpcs and bootstraps to the network,
// downlading all of the blocks and establishing a peer list.
func (e *Environment) initializeNetwork(rpcPort uint16, nobootstrap bool) (err error) {
	e.server, err = network.NewTCPServer(rpcPort)
	if err != nil {
		return
	}

	e.server.Register("AcceptBlock", e.AcceptBlock)
	e.server.Register("AcceptTransaction", e.AcceptTransaction)
	e.server.Register("SendBlocks", e.SendBlocks)
	e.server.Register("NegotiateContract", e.NegotiateContract)
	e.server.Register("RetrieveFile", e.RetrieveFile)

	if nobootstrap {
		go e.listen()
		return
	}

	// establish an initial peer list
	if err = e.server.Bootstrap(); err != nil {
		return
	}

	// Download the blockchain, getting blocks one batch at a time until an
	// empty batch is sent.
	go func() {
		// Catch up the first time.
		go e.CatchUp(e.server.RandomPeer())

		// Every 2 minutes call CatchUp() on a random peer. This will help to
		// resolve synchronization issues and keep everybody on the same page
		// with regards to the longest chain. It's a bit of a hack but will
		// make the network substantially more robust.
		for {
			time.Sleep(time.Minute * 2)
			go e.CatchUp(e.RandomPeer())
		}
	}()

	go e.listen()

	return nil
}

// AcceptBlock sends the input block down a channel, where it will be dealt
// with by the Environment's listener.
func (e *Environment) AcceptBlock(b siacore.Block) error {
	e.blockChan <- b
	return nil
}

// AcceptTransaction sends the input transaction down a channel, where it will
// be dealt with by the Environment's listener.
func (e *Environment) AcceptTransaction(t siacore.Transaction) error {
	e.transactionChan <- t
	return nil
}

// processBlock is called by the environment's listener.
func (e *Environment) processBlock(b siacore.Block) {
	e.state.Lock()
	e.hostDatabase.Lock()
	e.host.Lock()
	defer e.state.Unlock()
	defer e.hostDatabase.Unlock()
	defer e.host.Unlock()

	initialStateHeight := e.state.Height()
	rewoundBlocks, appliedBlocks, err := e.state.AcceptBlock(b)

	// Perform error handling.
	if err == siacore.BlockKnownErr || err == siacore.KnownOrphanErr {
		return
	} else if err != nil {
		// Call CatchUp() if an unknown orphan is sent.
		if err == siacore.UnknownOrphanErr {
			go e.CatchUp(e.server.RandomPeer())
		}
		return
	}

	e.updateHostDB(rewoundBlocks, appliedBlocks)
	e.storageProofMaintenance(initialStateHeight, rewoundBlocks, appliedBlocks)

	// Broadcast all valid blocks.
	go e.server.Broadcast("AcceptBlock", b, nil)
}

// processTransaction sends a transaction to the state.
func (e *Environment) processTransaction(t siacore.Transaction) {
	e.state.Lock()
	defer e.state.Unlock()

	err := e.state.AcceptTransaction(t)
	if err != nil {
		if err != siacore.ConflictingTransactionErr {
			// TODO: Change this println to a logging statement.
			fmt.Println("AcceptTransaction Error:", err)
		}
		return
	}

	go e.server.Broadcast("AcceptTransaction", t, nil)
}

// listen waits until a new block or transaction arrives, then attempts to
// process and rebroadcast it.
func (e *Environment) listen() {
	for {
		select {
		case b := <-e.blockChan:
			e.processBlock(b)

		case t := <-e.transactionChan:
			e.processTransaction(t)
		}
	}
}