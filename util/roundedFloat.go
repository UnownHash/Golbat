package util

import "strconv"

type RoundedFloat4 float64

func (r RoundedFloat4) MarshalJSON() ([]byte, error) {
	return []byte(strconv.FormatFloat(float64(r), 'f', 4, 64)), nil
}
