package interfaces

import "sylr.dev/rafty"

type Work[T any] = rafty.Work[T]

type Works[T any, T2 Work[T]] = rafty.Works[T, T2]
