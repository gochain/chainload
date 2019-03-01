package chainload

import "testing"

func Test_distribute(t *testing.T) {
	for ti, test := range []struct {
		v     int
		s     []int
		after []int
	}{
		{
			v:     10,
			s:     make([]int, 1),
			after: []int{10},
		},
		{
			v:     10,
			s:     make([]int, 2),
			after: []int{5, 5},
		},
		{
			v:     10,
			s:     make([]int, 3),
			after: []int{4, 3, 3},
		},
		{
			v:     10,
			s:     make([]int, 4),
			after: []int{3, 3, 2, 2},
		},
		{
			v:     10,
			s:     make([]int, 5),
			after: []int{2, 2, 2, 2, 2},
		},
		{
			v:     2,
			s:     make([]int, 3),
			after: []int{1, 1, 0},
		},
	} {
		distribute(test.v, test.s)
		for i, e := range test.s {
			if test.after[i] != e {
				t.Error("test:", ti, test)
			}
		}
	}
}
