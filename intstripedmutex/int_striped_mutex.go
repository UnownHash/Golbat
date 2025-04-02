package intstripedmutex

import (
	"sync"
)

// IntStripedMutex is an object that allows fine grained locking based on integer keys
//
// It ensures that if `key1 == key2` then lock associated with `key1` is the same as the one associated with `key2`
// It holds a stable number of locks in memory that use can control
//
// It is an integer version of https://github.com/nmvalera/striped-mutex
type IntStripedMutex struct {
	stripes []*sync.Mutex
}

// Lock acquire lock for a given key
func (m *IntStripedMutex) Lock(key uint64) {
	l, _ := m.GetLock(key)
	l.Lock()
}

// Unlock release lock for a given key
func (m *IntStripedMutex) Unlock(key uint64) {
	l, _ := m.GetLock(key)
	l.Unlock()
}

// GetLock retrieve a lock for a given key
func (m *IntStripedMutex) GetLock(key uint64) (*sync.Mutex, error) {
	return m.stripes[key%uint64(len(m.stripes))], nil
}

// New creates a IntStripedMutex
func New(stripes uint) *IntStripedMutex {
	m := &IntStripedMutex{
		make([]*sync.Mutex, stripes),
	}
	for i := 0; i < len(m.stripes); i++ {
		m.stripes[i] = &sync.Mutex{}
	}

	return m
}
