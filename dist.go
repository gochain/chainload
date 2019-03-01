package chainload

// distribute sets the slice elements to nearly even values which sum to v.
func distribute(v int, s []int) {
	base := v / len(s)
	for i := range s {
		s[i] = base
	}
	left := v % len(s)
	for i := 0; i < left; i++ {
		s[i] += 1
	}
}
