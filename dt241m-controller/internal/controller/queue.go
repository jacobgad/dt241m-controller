package controller

import "sync"

// keyedQueue runs functions for the same key strictly in call order; different keys run concurrently.
type keyedQueue struct {
	mu    sync.Mutex
	tails map[string]chan struct{}
	depth map[string]int
}

func newKeyedQueue() *keyedQueue {
	return &keyedQueue{tails: make(map[string]chan struct{}), depth: make(map[string]int)}
}

func (q *keyedQueue) run(key string, fn func()) {
	q.mu.Lock()
	prev := q.tails[key]
	done := make(chan struct{})
	q.tails[key] = done
	q.depth[key]++
	q.mu.Unlock()

	if prev != nil {
		<-prev
	}
	defer func() {
		close(done)
		q.mu.Lock()
		if q.tails[key] == done {
			delete(q.tails, key)
		}
		q.depth[key]--
		if q.depth[key] == 0 {
			delete(q.depth, key)
		}
		q.mu.Unlock()
	}()
	fn()
}

func (q *keyedQueue) pending(key string) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.depth[key]
}
