package controller

import "sync"

// keyedQueue serialises work per key. Slots are claimed at enqueue time, under the
// lock, so the order in which callers arrive is the order in which their work runs
// even though the work itself happens on background goroutines.
type keyedQueue struct {
	mu    sync.Mutex
	tails map[string]chan struct{}
	wg    sync.WaitGroup
}

func newKeyedQueue() *keyedQueue {
	return &keyedQueue{tails: make(map[string]chan struct{})}
}

func (q *keyedQueue) enqueue(key string, fn func()) <-chan struct{} {
	q.mu.Lock()
	prev := q.tails[key]
	done := make(chan struct{})
	q.tails[key] = done
	q.wg.Add(1)
	q.mu.Unlock()

	go func() {
		defer q.wg.Done()
		defer func() {
			close(done)
			q.mu.Lock()
			if q.tails[key] == done {
				delete(q.tails, key)
			}
			q.mu.Unlock()
		}()
		if prev != nil {
			<-prev
		}
		fn()
	}()
	return done
}

func (q *keyedQueue) wait() {
	q.wg.Wait()
}
