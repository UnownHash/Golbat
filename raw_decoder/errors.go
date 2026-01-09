package raw_decoder

import "errors"

var errRequestProtoNotAvailable = errors.New("request proto not available")

func NewErrRequestProtoNotAvailable() error {
	return errRequestProtoNotAvailable
}

func IsErrRequestProtoNotAvailable(err error) bool {
	return errors.Is(err, errRequestProtoNotAvailable)
}
