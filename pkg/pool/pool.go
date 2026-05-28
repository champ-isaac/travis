package pool

import "sync"

type Pool[T any] struct {
	sp *sync.Pool
}

func New[T any](newFn func() T) *Pool[T] {
	return &Pool[T]{
		sp: &sync.Pool{
			New: func() any { return newFn() },
		},
	}
}

func (p *Pool[T]) Get() any {
	return p.sp.Get().(T)
}

func (p *Pool[T]) Put(x T) {
	p.sp.Put(x)
}
