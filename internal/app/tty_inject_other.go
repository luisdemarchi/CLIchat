//go:build !darwin

package app

import "errors"

func injectIntoTTY(_ string, _ string) error {
	return errors.New("tty injection nao suportado nesta plataforma")
}
