//go:build windows

package exec

import "errors"

func signalZero(int) error {
	return errors.New("windows uses tasklist")
}
