package rafty

func serie(start, length, step int) []int {
	a := make([]int, length)
	for i := range a {
		if i == 0 {
			a[i] = start
		} else {
			a[i] = a[i-1] + step
		}
	}
	return a
}
