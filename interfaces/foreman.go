package interfaces

import "sylr.dev/rafty"

type Foreman[T any, T2 Work[T]] = rafty.Foreman[T, T2]
