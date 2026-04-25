package security

import "sync"

// memMu hides sync.Mutex behind named methods so the surrounding code
// reads naturally (lock/unlock instead of Lock/Unlock on a public
// field). It is internal to the in-memory refresh store.
type memMu struct{ m sync.Mutex }

func (mu *memMu) lock()   { mu.m.Lock() }
func (mu *memMu) unlock() { mu.m.Unlock() }
