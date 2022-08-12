package main

import (
	"sylr.dev/rafty"
)

var _ rafty.Work[string] = (*RaftyMcRaftFaceWork[string])(nil)

type RaftyMcRaftFaceWork[T ~string] string

func (w RaftyMcRaftFaceWork[T]) ID() string {
	return string(w)
}

func (w RaftyMcRaftFaceWork[T]) Object() T {
	return T(w)
}

func (w RaftyMcRaftFaceWork[T]) Equals(t T) bool {
	return T(w) == t
}

// -----------------------------------------------------------------------------

var _ rafty.Foreman[string, RaftyMcRaftFaceWork[string]] = (*RaftyMcRaftFaceWorks[string])(nil)

type RaftyMcRaftFaceWorks[T ~string] struct {
	works []RaftyMcRaftFaceWork[T]
	ch    chan struct{}
}

func (w *RaftyMcRaftFaceWorks[T]) NewWorks() chan struct{} {
	return w.ch
}

func (w *RaftyMcRaftFaceWorks[T]) GetWorks() []RaftyMcRaftFaceWork[T] {
	return w.works
}

func (w *RaftyMcRaftFaceWorks[T]) Start() {

}

func (w *RaftyMcRaftFaceWorks[T]) Stop() {

}
