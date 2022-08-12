package rafty

type Work[T any] interface {
	ID() string
	Object() T
	Equals(T) bool
}

type Works[T any, T2 Work[T]] interface {
	GetWorks() []T2
}
