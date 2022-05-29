package util

func BoolToInt[T int8 | int16 | int64](b bool) T {
	if b {
		return 1
	}
	return 0
}
