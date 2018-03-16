// Copyright 2018 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package observer

import (
	"bytes"
	"crypto/ecdsa"
	"errors"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto/sha3"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/trie"
)

// Errors of observer chain.
var (
	ErrNoBlock = errors.New("block not found in observer chain")
)

// -----
// CHAIN
// -----

// Chain represents the canonical observer chain given a database with a
// genesis block.
type Chain struct {
	mu           sync.RWMutex
	db           ethdb.Database
	privateKey   *ecdsa.PrivateKey
	genesisBlock *Block
	currentBlock *Block
	trie         *trie.Trie
	trieDB       *trie.Database
	trieRoot     common.Hash
	closeC       chan struct{}
}

// NewChain returns a fully initialised Observer chain
// using information available in the database
func NewChain(db ethdb.Database, privKey *ecdsa.PrivateKey) (*Chain, error) {
	c := &Chain{
		db:         db,
		privateKey: privKey,
		trieDB:     trie.NewDatabase(db),
	}
	// Check for genesis block, if needed generate it.
	genesisBlock := GetBlock(db, 0)
	if genesisBlock == nil {
		genesisBlock = NewBlock(privKey)
		if err := WriteBlock(db, genesisBlock); err != nil {
			return nil, err
		}
	}
	c.genesisBlock = genesisBlock
	// Now check for current block.
	currentBlock := GetHeadBlock(db)
	if currentBlock == nil {
		currentBlock = genesisBlock
	}
	c.currentBlock = currentBlock
	// Initialise trie.
	tr, err := trie.New(c.currentBlock.TrieRoot(), c.trieDB)
	if err != nil {
		return nil, err
	}
	c.trie = tr
	c.trieRoot = c.trie.Hash()
	return c, nil
}

// Block returns a single block by its number.
func (c *Chain) Block(number uint64) (*Block, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	b := GetBlock(c.db, number)
	if b == nil {
		return nil, ErrNoBlock
	}
	return b, nil
}

// GenesisBlock returns the first block of the observer chain.
func (c *Chain) GenesisBlock() *Block {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.genesisBlock
}

// CurrentBlock returns the current active block.
func (c *Chain) CurrentBlock() *Block {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.currentBlock
}

// TrieDo executes a function on the chains trie atomically.
func (c *Chain) TrieDo(f func(*trie.Trie) error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return f(c.trie)
}

// CreateBlock commits current trie and seals a new block.
func (c *Chain) CreateBlock() (*Block, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Commit trie.
	if c.trieChanged() {
		trieRoot, err := c.trie.Commit(nil)
		if err != nil {
			return nil, err
		}
		c.trieRoot = trieRoot
	}
	// Always create new block and persist.
	block := c.currentBlock.CreateSuccessor(c.trieRoot, c.privateKey)
	if err := WriteBlock(c.db, block); err != nil {
		return nil, err
	}
	c.currentBlock = block
	return c.currentBlock, nil
}

// AutoCreateBlocks starts a goroutine automatically creating blocks periodically until
// the chain is closed. It's non-blocking.
func (c *Chain) AutoCreateBlocks(period time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closeC != nil {
		return
	}
	c.closeC = make(chan struct{})
	go c.loop(period)
}

// Close closes the chain.
func (c *Chain) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Stop backend loop.
	if c.closeC != nil {
		close(c.closeC)
		c.closeC = nil
	}
	// Check for modified trie.
	if c.trieChanged() {
		trieRoot, err := c.trie.Commit(nil)
		if err != nil {
			log.Error("cannot commit trie", "err", err)
			return
		}
		c.trieRoot = trieRoot
		// Create block and persist.
		block := c.currentBlock.CreateSuccessor(c.trieRoot, c.privateKey)
		if err := WriteBlock(c.db, block); err != nil {
			log.Error("cannot write block", "err", err)
			return
		}
		c.currentBlock = block
	}
}

// loop realizes the chains backend goroutine.
func (c *Chain) loop(period time.Duration) {
	ticker := time.NewTicker(period)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			// Time to create a new block.
			if _, err := c.CreateBlock(); err != nil {
				log.Error("chain block creation failed", "err", err)
			}
		case <-c.closeC:
			// Close has been called.
			// TODO: Check for cleanup tasks like locked tries.
			return
		}
	}
}

// trieChanged checks if the current active trie changed.
func (c *Chain) trieChanged() bool {
	thb := c.trie.Hash().Bytes()
	trhb := c.trieRoot.Bytes()
	return !bytes.Equal(thb, trhb)
}

// -----
// HELPERS
// -----

// rlpHash calculates a hash out of the passed data.
func rlpHash(x interface{}) (h common.Hash) {
	hw := sha3.NewKeccak256()
	rlp.Encode(hw, x)
	hw.Sum(h[:0])
	return h
}

// writeCounter helps counting the written bytes in total.
type writeCounter common.StorageSize

// Write implements io.Writer and counts the written bytes
// in total.
func (c *writeCounter) Write(b []byte) (int, error) {
	*c += writeCounter(len(b))
	return len(b), nil
}
