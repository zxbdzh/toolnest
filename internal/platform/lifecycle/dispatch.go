package lifecycle

import "sync"

// UIQueue buffers activation before readiness and serializes dispatch without blocking callers.
// NewUIQueue(mygo.RunOnMain) is suitable for Hooks.Post; call Ready only after window + tray Start.
type UIQueue struct {
	mu                   sync.Mutex
	pending              []func()
	ready                chan struct{}
	wake                 chan struct{}
	stop                 chan struct{}
	readyOnce, closeOnce sync.Once
	closed               bool
	dispatch             func(func())
}

func NewUIQueue(dispatch func(func())) *UIQueue {
	if dispatch == nil {
		panic("UI dispatcher required")
	}
	q := &UIQueue{ready: make(chan struct{}), wake: make(chan struct{}, 1), stop: make(chan struct{}), dispatch: dispatch}
	go q.run()
	return q
}
func (q *UIQueue) Ready() { q.readyOnce.Do(func() { close(q.ready) }) }
func (q *UIQueue) Post(fn func()) {
	if fn == nil {
		return
	}
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return
	}
	q.pending = append(q.pending, fn)
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
}
func (q *UIQueue) run() {
	select {
	case <-q.stop:
		return
	case <-q.ready:
	}
	for {
		select {
		case <-q.stop:
			return
		case <-q.wake:
		}
		q.mu.Lock()
		batch := q.pending
		q.pending = nil
		q.mu.Unlock()
		q.dispatch(func() {
			for _, fn := range batch {
				q.mu.Lock()
				closed := q.closed
				q.mu.Unlock()
				if closed {
					return
				}
				fn()
			}
		})
	}
}

// Close drops pending callbacks. It deliberately does not wait on a potentially stopped UI loop.
func (q *UIQueue) Close() {
	q.closeOnce.Do(func() { q.mu.Lock(); q.closed = true; q.pending = nil; q.mu.Unlock(); close(q.stop) })
}
